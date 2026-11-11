package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ig1 "github.com/huin/goupnp/dcps/internetgateway1"
	ig2 "github.com/huin/goupnp/dcps/internetgateway2"
)

// Automatic inbound port mapping (UPnP-IGD / NAT-PMP / PCP).
//
// A peer behind an endpoint-dependent (symmetric) NAT can never be reached by
// hole punching: its outbound per-destination mappings are meaningless as
// inbound endpoints. The only zero-configuration way to give such a peer a
// stable inbound endpoint is to ask its gateway for one with NAT-PMP/PCP or
// UPnP-IGD. Once one side of a pair holds a stable mapping, the other side
// (even a symmetric-NAT peer) can punch to it directly: the initiator's
// outbound mapping toward that endpoint stays valid for the conversation, so
// "call the mapped peer" works where "punch the symmetric peer" cannot.
//
// natManager owns these mappings for the daemon. The daemon only advertises a
// mapping when the gateway's external IP equals the public IP the relay
// observes for this node: if they differ, the gateway itself sits behind
// carrier-grade NAT and the mapping would never be reachable from outside, so
// the observed endpoint is advertised instead and traffic keeps using relay
// fallback as before.

const (
	// natMapLeaseSec is the requested lease for an automatic mapping. Finite
	// so a crashed client never leaves a permanent forward rule on the
	// router; the manager renews well within the lease.
	natMapLeaseSec = 5 * 60
	// natRenewInterval is how often mappings are re-confirmed and their lease
	// extended. Roughly 2-3 renewals per lease means one lost renewal is
	// survivable.
	natRenewInterval = 2 * time.Minute
	// natDiscoveryTimeout bounds the parallel SSDP/service-discovery phase so a
	// router without a NAT-mapping protocol never stalls a daemon tick.
	natDiscoveryTimeout = 4 * time.Second
	// natProtoTimeout bounds each individual mapping or teardown exchange.
	natProtoTimeout = 2 * time.Second
	// natErrorLogInterval is the minimum gap between repeated identical
	// mapping failures for one port. Routers without a mapping protocol are
	// retried every renewal tick; logging each attempt would drown the log.
	natErrorLogInterval = 10 * time.Minute
)

// natResult describes an established inbound port mapping on a gateway.
type natResult struct {
	externalIP   string // gateway WAN IP the mapping is reachable at
	externalPort int    // external UDP port of the mapping
	via          string // "upnp" | "natpmp" | "pcp"
	gateway      string // gateway host (for NAT-PMP/PCP renewals)
	lease        uint32 // granted lease in seconds (0 = permanent/unknown)
	// upnpURL/upnpKind identify the exact UPnP service that owns the rule so
	// teardown can delete it through the same generation (IGDv1, IGDv2 or
	// WANPPPConnection). NAT-PMP/PCP rules expire with their lease.
	upnpURL  string
	upnpKind string
}

type natErr struct {
	msg    string
	at     time.Time // last attempt
	logged time.Time // last time this error was written to the log
}

// natManager runs automatic port mappings for every claimed WireGuard port,
// refreshing them on a fixed interval and on demand when a port is claimed.
// Safe for concurrent use; all state is guarded by mu.
type natManager struct {
	cfg     *Config
	ctx     context.Context
	on      atomic.Bool
	mu      sync.Mutex
	claimed map[int]struct{}
	maps    map[int]natResult
	errs    map[int]natErr
	// pending serializes a port's teardown before a re-claim can map it again.
	// Without it, a slow router teardown racing a fast re-join could delete the
	// newly established rule for the same external port.
	pending map[int]chan struct{}
	wake    chan struct{}
	// startOnce keeps repeated daemon Start calls from launching duplicate
	// mapping loops (Android reuses the daemon across VPN restarts).
	startOnce sync.Once
}

func newNatManager(cfg *Config, ctx context.Context) *natManager {
	n := &natManager{
		cfg:     cfg,
		ctx:     ctx,
		claimed: make(map[int]struct{}),
		maps:    make(map[int]natResult),
		errs:    make(map[int]natErr),
		pending: make(map[int]chan struct{}),
		wake:    make(chan struct{}, 1),
	}
	n.on.Store(cfg.NATMapping == nil || *cfg.NATMapping)
	return n
}

// enabled reports whether automatic NAT mapping is on (default on; a nil
// toggle means enabled so zero-config devices get mappings without setup).
func (n *natManager) enabled() bool {
	return n.on.Load()
}

// setEnabled toggles automatic mapping at runtime. Disabling tears every
// established rule down immediately instead of waiting for the next tick.
func (n *natManager) setEnabled(enabled bool) {
	n.on.Store(enabled)
	if enabled {
		n.wakeUp()
		return
	}
	n.teardownAll()
}

func (n *natManager) wakeUp() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

func (n *natManager) start() {
	n.startOnce.Do(func() { go n.loop() })
}

// claim requests an inbound mapping for a WireGuard port. Called whenever a
// network tunnel comes up on that port.
func (n *natManager) claim(port int) {
	n.mu.Lock()
	n.claimed[port] = struct{}{}
	n.mu.Unlock()
	n.wakeUp()
}

// unclaim drops the mapping for a port (e.g. a network was left/removed).
func (n *natManager) unclaim(port int) {
	n.mu.Lock()
	delete(n.claimed, port)
	res, ok := n.maps[port]
	delete(n.maps, port)
	delete(n.errs, port)
	if !ok {
		n.mu.Unlock()
		return
	}
	done := n.pending[port]
	if done == nil {
		done = make(chan struct{})
		n.pending[port] = done
	}
	n.mu.Unlock()

	// Teardown is network I/O and must not run under Daemon.mu (leaveLocked
	// calls unclaim while holding it). A later claim waits on pending before
	// mapping the port again, so the old delete cannot remove the new rule.
	go func() {
		teardownMapping(port, res)
		n.mu.Lock()
		if n.pending[port] == done {
			delete(n.pending, port)
		}
		n.mu.Unlock()
		close(done)
	}()
}

// waitPending waits for any in-flight teardown of port to finish. Called
// without holding n.mu, before establishing a new mapping.
func (n *natManager) waitPending(port int) {
	n.mu.Lock()
	done := n.pending[port]
	n.mu.Unlock()
	if done != nil {
		<-done
	}
}

// mapping returns the currently active mapping for a port, if the gate passed.
func (n *natManager) mapping(port int) (natResult, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	res, ok := n.maps[port]
	return res, ok
}

func (n *natManager) loop() {
	ticker := time.NewTicker(natRenewInterval)
	defer ticker.Stop()
	for {
		n.syncAll()
		select {
		case <-n.ctx.Done():
			n.teardownAll()
			return
		case <-ticker.C:
		case <-n.wake:
		}
	}
}

func (n *natManager) syncAll() {
	if !n.enabled() {
		return
	}
	n.mu.Lock()
	ports := make([]int, 0, len(n.claimed))
	for p := range n.claimed {
		ports = append(ports, p)
	}
	n.mu.Unlock()
	// Ports are independent; syncing them concurrently keeps a router that
	// answers one protocol slowly from multiplying the delay by network count.
	var wg sync.WaitGroup
	for _, p := range ports {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			n.syncPort(port)
		}(p)
	}
	wg.Wait()
}

func (n *natManager) syncPort(port int) {
	if !n.enabled() || !n.claimedPort(port) {
		return
	}
	n.waitPending(port)
	if !n.enabled() || !n.claimedPort(port) {
		return
	}
	lanIP, err := LocalIPForServer(n.cfg.EffectiveServerAddr())
	if err != nil || lanIP == "" {
		if err == nil {
			err = fmt.Errorf("empty LAN IP")
		}
		if n.noteError(port, fmt.Errorf("cannot determine LAN IP: %w", err)) {
			log.Printf("nat: mapping port %d: %v", port, err)
		}
		return
	}
	res, err := mapNATPort(n.ctx, port, lanIP)
	if err != nil {
		_, existed := n.mapping(port)
		if existed {
			n.setPort(port, natResult{}, false)
		}
		if n.noteError(port, err) {
			if existed {
				log.Printf("nat: mapping %d lost (%v)", port, err)
			} else {
				log.Printf("nat: no mapping for port %d: %v", port, err)
			}
		}
		return
	}
	unchanged, stored := n.storeMapping(port, res)
	if !stored {
		// The network was left/removed or mapping was switched off while
		// discovery was in flight. Remove the fresh gateway rule instead of
		// leaving an orphan behind.
		teardownMapping(port, res)
		return
	}
	n.noteOK(port)
	if !unchanged {
		log.Printf("nat: mapped %s:%d via %s (lease %ds) for wg port %d", res.externalIP, res.externalPort, res.via, res.lease, port)
	}
}

// noteError records the latest failure and reports whether it should be
// logged now (first failure, changed error, or after the throttling window).
func (n *natManager) noteError(port int, err error) bool {
	now := time.Now()
	msg := err.Error()
	n.mu.Lock()
	prev, ok := n.errs[port]
	logNow := !ok || prev.msg != msg || now.Sub(prev.at) >= natErrorLogInterval
	if logNow {
		n.errs[port] = natErr{msg: msg, at: now}
	} else if !ok {
		n.errs[port] = natErr{msg: msg, at: now}
	}
	n.mu.Unlock()
	return logNow
}

func (n *natManager) noteOK(port int) {
	n.mu.Lock()
	delete(n.errs, port)
	n.mu.Unlock()
}

// claimedPort reports whether port is still wanted by an active network.
func (n *natManager) claimedPort(port int) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.claimed[port]
	return ok
}

// storeMapping records a mapping only while the port is still claimed. It
// returns unchanged=true when the same mapping was already present and
// stored=false when the port was unclaimed while the mapping was in flight.
func (n *natManager) storeMapping(port int, res natResult) (unchanged, stored bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.enabled() {
		return false, false
	}
	if _, ok := n.claimed[port]; !ok {
		return false, false
	}
	if prev, ok := n.maps[port]; ok && prev == res {
		return true, true
	}
	n.maps[port] = res
	return false, true
}

// setPort stores (active) or clears the mapping; returns true when the state
// was unchanged.
func (n *natManager) setPort(port int, res natResult, active bool) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if active {
		prev, existed := n.maps[port]
		if existed && prev == res {
			return true
		}
		n.maps[port] = res
		return false
	}
	_, existed := n.maps[port]
	delete(n.maps, port)
	return !existed
}

func (n *natManager) teardownAll() {
	n.mu.Lock()
	res := make(map[int]natResult, len(n.maps))
	for p, r := range n.maps {
		res[p] = r
	}
	n.maps = make(map[int]natResult)
	n.errs = make(map[int]natErr)
	n.mu.Unlock()
	for p, r := range res {
		teardownMapping(p, r)
	}
}

// status returns a UI-safe snapshot of mapping state and recent failures.
func (n *natManager) status() map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	mappings := make([]map[string]any, 0, len(n.maps))
	ports := make([]int, 0, len(n.maps))
	for p := range n.maps {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	for _, p := range ports {
		r := n.maps[p]
		mappings = append(mappings, map[string]any{
			"port":         p,
			"externalIp":   r.externalIP,
			"externalPort": r.externalPort,
			"via":          r.via,
			"lease":        r.lease,
		})
	}
	errs := make([]map[string]any, 0, len(n.errs))
	ports = ports[:0]
	for p := range n.errs {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	for _, p := range ports {
		e := n.errs[p]
		errs = append(errs, map[string]any{
			"port":    p,
			"error":   e.msg,
			"lastTry": e.at.UTC().Format(time.RFC3339),
		})
	}
	return map[string]any{
		"enabled":  n.enabled(),
		"mappings": mappings,
		"errors":   errs,
	}
}

// mapNATPort establishes an inbound UDP mapping for port. It prefers the
// gateway's UPnP-IGD service (whose discovery also reveals the gateway host
// for NAT-PMP/PCP), then falls back to NAT-PMP and PCP towards the gateway.
func mapNATPort(ctx context.Context, port int, lanIP string) (natResult, error) {
	return mapNATPortWith(ctx, port, lanIP, defaultGatewayIP, upnpMap, natpmpMap, pcpMap)
}

func mapNATPortWith(ctx context.Context, port int, lanIP string, defaultGW func() string,
	upnp func(context.Context, int, string) (string, natResult, error),
	natpmp func(context.Context, int, string, string) (natResult, error),
	pcp func(context.Context, int, string, string) (natResult, error),
) (natResult, error) {
	gw, res, err := upnp(ctx, port, lanIP)
	if err == nil {
		return res, nil
	}
	upnpMsg := fmt.Sprintf("upnp: %v", err)
	if gw == "" && defaultGW != nil {
		gw = defaultGW()
	}
	if gw == "" {
		return natResult{}, fmt.Errorf("%s; no gateway discoverable for nat-pmp/pcp", upnpMsg)
	}
	if res, err := natpmp(ctx, port, lanIP, gw); err == nil {
		return res, nil
	}
	if res, err := pcp(ctx, port, lanIP, gw); err == nil {
		return res, nil
	}
	return natResult{}, fmt.Errorf("%s; gateway %s gives no nat-pmp/pcp mapping", upnpMsg, gw)
}

// upnpMapper is the common service surface of goupnp's WAN connection clients.
// Context variants are used so a router that accepts the SSDP query but then
// stalls on the SOAP request cannot hang the daemon's renewal loop.
type upnpMapper interface {
	AddPortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string, internalPort uint16, internalClient string, enabled bool, desc string, lease uint32) error
	GetExternalIPAddressCtx(ctx context.Context) (string, error)
	DeletePortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string) error
}

// upnpMap tries every discovered WAN connection service in generation order
// (WANIPConnection:2, :1, WANPPPConnection:1) and returns the first that maps
// the port. It also returns the gateway host (from the service location URL)
// so the caller can try NAT-PMP/PCP when UPnP is unhelpful.
func upnpMap(ctx context.Context, port int, lanIP string) (string, natResult, error) {
	type svc struct {
		m       upnpMapper
		gateway string
		url     string
		kind    string
	}
	// Discover all three service generations in parallel under one shared
	// budget. Sequential discovery made a router that only answers IGDv1 wait
	// up to 12s (and, if the first generation consumed the whole budget, never
	// get probed at all).
	actx, cancel := context.WithTimeout(ctx, natDiscoveryTimeout)
	defer cancel()

	var (
		mu     sync.Mutex
		groups [3][]svc
		wg     sync.WaitGroup
	)
	collect := func(i int, cs []svc) {
		if len(cs) == 0 {
			return
		}
		mu.Lock()
		groups[i] = cs
		mu.Unlock()
	}
	wg.Add(3)
	go func() {
		defer wg.Done()
		clients, _, _ := ig2.NewWANIPConnection2ClientsCtx(actx)
		out := make([]svc, 0, len(clients))
		for _, c := range clients {
			out = append(out, svc{m: c, gateway: locHost(c.Location), url: locString(c.Location), kind: "ig2"})
		}
		collect(0, out)
	}()
	go func() {
		defer wg.Done()
		clients, _, _ := ig1.NewWANIPConnection1ClientsCtx(actx)
		out := make([]svc, 0, len(clients))
		for _, c := range clients {
			out = append(out, svc{m: c, gateway: locHost(c.Location), url: locString(c.Location), kind: "ig1"})
		}
		collect(1, out)
	}()
	go func() {
		defer wg.Done()
		clients, _, _ := ig1.NewWANPPPConnection1ClientsCtx(actx)
		out := make([]svc, 0, len(clients))
		for _, c := range clients {
			out = append(out, svc{m: c, gateway: locHost(c.Location), url: locString(c.Location), kind: "ppp"})
		}
		collect(2, out)
	}()
	wg.Wait()

	gw := ""
	total := 0
	for _, group := range groups {
		for _, s := range group {
			total++
			if gw == "" {
				gw = s.gateway
			}
			mctx, mcancel := context.WithTimeout(ctx, natProtoTimeout)
			res, err := mapUpnp(mctx, s.m, s.url, s.kind, port, lanIP)
			mcancel()
			if err == nil {
				return gw, res, nil
			}
		}
	}
	if gw != "" {
		return gw, natResult{}, fmt.Errorf("all %d upnp services failed", total)
	}
	return "", natResult{}, fmt.Errorf("no upnp igd service discovered")
}

func mapUpnp(ctx context.Context, m upnpMapper, serviceURL, serviceKind string, port int, lanIP string) (natResult, error) {
	if err := m.AddPortMappingCtx(ctx, "", uint16(port), "UDP", uint16(port), lanIP, true, "snet", natMapLeaseSec); err != nil {
		return natResult{}, err
	}
	drop := func() {
		dctx, dcancel := context.WithTimeout(context.Background(), natProtoTimeout)
		defer dcancel()
		_ = m.DeletePortMappingCtx(dctx, "", uint16(port), "UDP")
	}
	// The mapping is only useful if the gateway reports its WAN IP; refuse
	// mappings we cannot verify so a half-open rule never leaves us assuming.
	ip, err := m.GetExternalIPAddressCtx(ctx)
	if err != nil {
		drop()
		return natResult{}, err
	}
	if net.ParseIP(ip) == nil {
		drop()
		return natResult{}, fmt.Errorf("upnp gateway external IP %q not an IP", ip)
	}
	return natResult{
		externalIP:   ip,
		externalPort: port,
		via:          "upnp",
		gateway:      locHostFromString(serviceURL),
		lease:        natMapLeaseSec,
		upnpURL:      serviceURL,
		upnpKind:     serviceKind,
	}, nil
}

func locString(loc *url.URL) string {
	if loc == nil {
		return ""
	}
	return loc.String()
}

func locHostFromString(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return locHost(u)
}

func locHost(loc *url.URL) string {
	if loc == nil {
		return ""
	}
	h := loc.Hostname()
	if h == "" {
		return ""
	}
	return strings.Trim(h, "[]")
}

// natpmpMap is a minimal NAT-PMP (RFC 6886) client: public-address request
// followed by a Map UDP request, both to the gateway's port 5351.
func natpmpMap(ctx context.Context, port int, lanIP, gateway string) (natResult, error) {
	return natpmpMapAt(ctx, port, lanIP, gateway, net.JoinHostPort(gateway, "5351"))
}

func natpmpMapAt(ctx context.Context, port int, lanIP, gateway, target string) (natResult, error) {
	_ = lanIP // NAT-PMP derives the internal client from the source address.
	d := net.Dialer{Timeout: natProtoTimeout}
	conn, err := d.DialContext(ctx, "udp", target)
	if err != nil {
		return natResult{}, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(natProtoTimeout)); err != nil {
		return natResult{}, err
	}
	if n, err := conn.Write([]byte{0, 0}); err != nil || n != 2 {
		return natResult{}, fmt.Errorf("nat-pmp public-address request: %v", err)
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return natResult{}, fmt.Errorf("nat-pmp public-address: %v", err)
	}
	pubIP, err := parseNatPMPPublic(buf[:n])
	if err != nil {
		return natResult{}, fmt.Errorf("nat-pmp public-address: %v", err)
	}
	if _, err := conn.Write(natpmpMapReq(port)); err != nil {
		return natResult{}, err
	}
	n, err = conn.Read(buf)
	if err != nil {
		return natResult{}, fmt.Errorf("nat-pmp map: %v", err)
	}
	extPort, lease, err := parseNatPMPMap(buf[:n])
	if err != nil {
		return natResult{}, fmt.Errorf("nat-pmp map: %v", err)
	}
	return natResult{
		externalIP:   pubIP,
		externalPort: extPort,
		via:          "natpmp",
		gateway:      gateway,
		lease:        lease,
	}, nil
}

// natpmpMapReq builds a NAT-PMP Map UDP request (version 0, opcode 2):
// internal port, suggested external port (same), suggested lifetime.
func natpmpMapReq(port int) []byte {
	req := make([]byte, 12)
	req[1] = 2
	binary.BigEndian.PutUint16(req[4:6], uint16(port))
	binary.BigEndian.PutUint16(req[6:8], uint16(port))
	binary.BigEndian.PutUint32(req[8:12], natMapLeaseSec)
	return req
}

// parseNatPMPPublic parses a public-address response (12 bytes: version,
// opcode 0x80, result, epoch, external IPv4).
func parseNatPMPPublic(b []byte) (string, error) {
	if len(b) < 12 {
		return "", fmt.Errorf("short reply (%d bytes)", len(b))
	}
	if b[0] != 0 || b[1] != 0x80 {
		return "", fmt.Errorf("bad header %x%x", b[0], b[1])
	}
	if res := binary.BigEndian.Uint16(b[2:4]); res != 0 {
		return "", natpmpError(res)
	}
	return net.IPv4(b[8], b[9], b[10], b[11]).String(), nil
}

// parseNatPMPMap parses a Map UDP response (16 bytes: version, opcode 0x82,
// result, epoch, internal port, external port, lifetime).
func parseNatPMPMap(b []byte) (extPort int, lease uint32, err error) {
	if len(b) < 16 {
		return 0, 0, fmt.Errorf("short reply (%d bytes)", len(b))
	}
	if b[0] != 0 || b[1] != 0x82 {
		return 0, 0, fmt.Errorf("bad header %x%x", b[0], b[1])
	}
	if res := binary.BigEndian.Uint16(b[2:4]); res != 0 {
		return 0, 0, natpmpError(res)
	}
	// Layout (RFC 6886 section 3.4): epoch 4:8, internal port 8:10,
	// external port 10:12, lifetime 12:16.
	return int(binary.BigEndian.Uint16(b[10:12])), binary.BigEndian.Uint32(b[12:16]), nil
}

func natpmpError(res uint16) error {
	// RFC 6886 result codes: 0 success, 1 unsupported version, 2 not
	// authorized, 3 network failure, 4 out of resources, 5 unsupported opcode.
	names := []string{"success", "unsupported-version", "not-authorized", "network-failure", "out-of-resources", "unsupported-opcode"}
	if int(res) < len(names) {
		return fmt.Errorf("nat-pmp refused (%s)", names[res])
	}
	return fmt.Errorf("nat-pmp refused (result %d)", res)
}

// pcpMap is a minimal PCP (RFC 6887) MAP client. PCP is rare on consumer
// routers but trivial to detect, so the fallback stays cheap.
func pcpMap(ctx context.Context, port int, lanIP, gateway string) (natResult, error) {
	return pcpMapAt(ctx, port, lanIP, gateway, net.JoinHostPort(gateway, "5351"))
}

func pcpMapAt(ctx context.Context, port int, lanIP, gateway, target string) (natResult, error) {
	d := net.Dialer{Timeout: natProtoTimeout}
	conn, err := d.DialContext(ctx, "udp", target)
	if err != nil {
		return natResult{}, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(natProtoTimeout)); err != nil {
		return natResult{}, err
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return natResult{}, err
	}
	req, err := pcpMapReq(port, nonce, lanIP)
	if err != nil {
		return natResult{}, err
	}
	if _, err := conn.Write(req); err != nil {
		return natResult{}, err
	}
	resp := make([]byte, 512)
	n, err := conn.Read(resp)
	if err != nil {
		return natResult{}, fmt.Errorf("pcp map: %v", err)
	}
	if n < 36 || !bytes.Equal(resp[24:36], nonce[:]) {
		return natResult{}, fmt.Errorf("pcp map: response nonce mismatch")
	}
	extIP, extPort, lease, err := parsePCPMapResp(resp[:n])
	if err != nil {
		return natResult{}, fmt.Errorf("pcp map: %v", err)
	}
	return natResult{
		externalIP:   extIP,
		externalPort: extPort,
		via:          "pcp",
		gateway:      gateway,
		lease:        lease,
	}, nil
}

// pcpMapReq builds a PCP (RFC 6887) MAP request: 24-byte common header
// (version 2, opcode 1, lifetime, client IP) followed by the MAP
// opcode-specific block (nonce, protocol=UDP, internal port, suggested
// external port, suggested external IP of zero).
func pcpMapReq(port int, nonce [12]byte, clientIP string) ([]byte, error) {
	ip := net.ParseIP(clientIP).To16()
	if ip == nil {
		return nil, fmt.Errorf("pcp: invalid client IP %q", clientIP)
	}
	req := make([]byte, 60)
	req[0] = 2 // version
	req[1] = 1 // R=0, opcode MAP=1
	binary.BigEndian.PutUint32(req[4:8], natMapLeaseSec)
	copy(req[8:24], ip) // PCP Client's IP Address (v4-mapped for IPv4)
	copy(req[24:36], nonce[:])
	req[36] = 17 // IPPROTO_UDP
	binary.BigEndian.PutUint16(req[40:42], uint16(port))
	binary.BigEndian.PutUint16(req[42:44], uint16(port))
	return req, nil
}

// parsePCPMapResp parses a MAP response. The common response header is 24
// bytes; result code is at 3, lifetime at 4:8, then the MAP payload carries
// the internal port at 40:42, assigned external port at 42:44 and assigned
// external IP at 44:60 (RFC 6887 section 11.1).
func parsePCPMapResp(b []byte) (extIP string, extPort int, lease uint32, err error) {
	if len(b) < 60 {
		return "", 0, 0, fmt.Errorf("short reply (%d bytes)", len(b))
	}
	if b[0] != 2 {
		return "", 0, 0, fmt.Errorf("bad version %d", b[0])
	}
	if b[1]&0x80 == 0 || b[1]&0x7f != 1 {
		return "", 0, 0, fmt.Errorf("bad response/opcode %#x", b[1])
	}
	if res := b[3]; res != 0 {
		return "", 0, 0, pcpError(res)
	}
	ip := net.IP(b[44:60])
	if ip.IsUnspecified() {
		return "", 0, 0, fmt.Errorf("no assigned external IP")
	}
	p := binary.BigEndian.Uint16(b[42:44])
	if p == 0 {
		return "", 0, 0, fmt.Errorf("no assigned external port")
	}
	return ip.String(), int(p), binary.BigEndian.Uint32(b[4:8]), nil
}

func pcpError(res byte) error {
	// RFC 6887 section 7.4 result codes.
	names := []string{
		"success", "unsupported-version", "not-authorized", "malformed-request",
		"unsupported-opcode", "unsupported-option", "malformed-option",
		"network-failure", "no-resources", "unsupported-protocol",
		"user-ex-quota", "cannot-provide-external", "address-mismatch",
		"excessive-remote-peers",
	}
	if int(res) < len(names) {
		return fmt.Errorf("pcp refused (%s)", names[res])
	}
	return fmt.Errorf("pcp refused (result %d)", res)
}

// teardownMapping best-effort removes a mapping from its gateway. Failures are
// tolerable: lease expiry cleans the rule up on the router anyway.
func teardownMapping(port int, res natResult) {
	if res.via != "upnp" {
		// NAT-PMP/PCP rules expire when their lease lapses. Sending a
		// zero-lifetime delete is optional and easy to get wrong, so leave it
		// to the gateway.
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), natProtoTimeout)
	defer cancel()

	// Prefer the exact service URL captured at mapping time. Older or
	// partially-populated results fall back to the gateway host.
	loc := res.upnpURL
	if loc == "" && res.gateway != "" {
		loc = (&url.URL{Scheme: "http", Host: res.gateway}).String()
	}
	if loc == "" {
		return
	}
	u, err := url.Parse(loc)
	if err != nil {
		return
	}
	switch res.upnpKind {
	case "ig2":
		if cs, err := ig2.NewWANIPConnection2ClientsByURLCtx(ctx, u); err == nil {
			for _, c := range cs {
				_ = c.DeletePortMappingCtx(ctx, "", uint16(port), "UDP")
			}
		}
	case "ig1":
		if cs, err := ig1.NewWANIPConnection1ClientsByURLCtx(ctx, u); err == nil {
			for _, c := range cs {
				_ = c.DeletePortMappingCtx(ctx, "", uint16(port), "UDP")
			}
		}
	case "ppp":
		if cs, err := ig1.NewWANPPPConnection1ClientsByURLCtx(ctx, u); err == nil {
			for _, c := range cs {
				_ = c.DeletePortMappingCtx(ctx, "", uint16(port), "UDP")
			}
		}
	default:
		// Unknown generation: try IGDv2 then IGDv1; one of them may own the
		// rule even if the stored metadata predates this field.
		if cs, err := ig2.NewWANIPConnection2ClientsByURLCtx(ctx, u); err == nil {
			for _, c := range cs {
				_ = c.DeletePortMappingCtx(ctx, "", uint16(port), "UDP")
			}
		}
		if cs, err := ig1.NewWANIPConnection1ClientsByURLCtx(ctx, u); err == nil {
			for _, c := range cs {
				_ = c.DeletePortMappingCtx(ctx, "", uint16(port), "UDP")
			}
		}
	}
}
