package client

import (
	"context"
	"encoding/hex"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/S-shidi/snet/shared/protocol"
)

const (
	directRegisterInterval = 20 * time.Second
	directLookupInterval   = 60 * time.Second
	directLookupRetry      = 15 * time.Second
	directProbeInterval    = 3 * time.Second
	directUpgradeInterval  = 10 * time.Second
	directPingInterval     = 5 * time.Second
	directDeadAfter        = 15 * time.Second
	relayFallbackAfter     = 30 * time.Second

	// directPortWindow is how many ports on each side of a peer's
	// relay-observed public port are probed in one burst. CGNAT allocates
	// ports sequentially per destination, so the mapping toward us is often a
	// small delta away from the mapping toward the relay.
	directPortWindow = 8
	// directProbeLog throttles the per-peer probe summary.
	directProbeLog = 30 * time.Second
	// directProbeAcks is how many copies of a probe ack are sent back; the
	// first one is often lost while a NAT mapping is still forming.
	directProbeAcks = 3
)

// PathManager owns the relay-first data plane for one network. It presents a
// loopback UDP shim per peer to WireGuard, so every peer endpoint is stable
// (127.0.0.1:Pi) regardless of how packets actually travel. The shim forwards
// opaque WireGuard ciphertext over a direct UDP path when one is healthy and
// falls back to Relay v2 otherwise, without touching WireGuard's endpoint
// state.
type PathManager struct {
	ctx        context.Context
	nid        string
	nodeID     string
	token      string
	serverAddr string
	wgAddr     *net.UDPAddr

	mu         sync.RWMutex
	ticket     protocol.RelayTicketResp
	sessionID  [16]byte
	ownKey     uint64
	nextFetch  time.Time
	relayLinks []*relayLink
	shims      map[string]*peerShim
	peerKeys   map[uint64]string
	peers      map[string]protocol.Node

	directConn  *net.UDPConn
	directConn6 *net.UDPConn
	directPeers map[uint64]*directPeer

	// directPort4/6 are the preferred local ports for the direct sockets. A
	// stable local port keeps the CGNAT mapping alive across daemon or VPN
	// restarts; zero means "let the OS choose".
	directPort4 int
	directPort6 int

	natType      string
	localNets    []*net.IPNet
	selfPublicIP net.IP
	mappedAddr   *net.UDPAddr
	mappedAt     time.Time

	// Diagnostic state (maintained by the direct loop; not used for routing).
	peerSig      map[uint64]string
	lastV6Logged string
	lastMapLog   string
	dropLogged   map[string]time.Time

	sendDrops  atomic.Uint64
	recvDrops  atomic.Uint64
	sendFrames atomic.Uint64
	recvFrames atomic.Uint64

	directSendFrames atomic.Uint64
	directRecvFrames atomic.Uint64

	closeOnce sync.Once
	closed    chan struct{}
}

// relayLink is one Relay v2 instance connection. Every advertised instance is
// kept registered so a peer can reach us through whichever relay it selects;
// outgoing relay traffic prefers the first ready link.
type relayLink struct {
	endpoint     string
	addr         *net.UDPAddr
	conn         *net.UDPConn
	ready        bool
	attempt      time.Time
	nextDial     time.Time
	nextRegister time.Time
	lastHello    time.Time
	lastRecv     atomic.Int64
	lastErrLog   time.Time
}

type directCandidate struct {
	kind string
	addr *net.UDPAddr
}

type directPeer struct {
	addr         *net.UDPAddr
	hintIP       net.IP
	kind         string
	lanAddr      *net.UDPAddr
	directAddr   *net.UDPAddr
	v6Addr       *net.UDPAddr
	relayAddr    *net.UDPAddr
	online       bool
	ready        bool
	lookupMisses int
	probeIdx     int
	lastRecv     time.Time
	lastProbe    time.Time
	lastLookup   time.Time
	lastPing     time.Time
	lastProbeLog time.Time
	// pathSince is when the peer entered its current non-direct state. It
	// lets Status report a relay fallback once hole punching has timed out
	// instead of showing "discovering" forever.
	pathSince time.Time
}

// NewPathManager builds a relay-first path manager for one network. Run must
// be started before traffic is expected.
func NewPathManager(ctx context.Context, nid string, nc *NetworkCfg, serverAddr string) *PathManager {
	pm := &PathManager{
		ctx:         ctx,
		nid:         nid,
		nodeID:      nc.NodeID,
		token:       nc.Token,
		serverAddr:  serverAddr,
		wgAddr:      &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: nc.Port},
		ownKey:      protocol.RelayNodeKey(nc.NodeID),
		shims:       make(map[string]*peerShim),
		peerKeys:    make(map[uint64]string),
		peers:       make(map[string]protocol.Node),
		directPeers: make(map[uint64]*directPeer),
		closed:      make(chan struct{}),
		peerSig:     make(map[uint64]string),
		dropLogged:  make(map[string]time.Time),
	}
	if nc.Port > 0 && nc.Port <= 65533 {
		pm.directPort4 = nc.Port + 2
		pm.directPort6 = nc.Port + 3
	}
	return pm
}

// Run starts ticket refresh, relay heartbeat, and relay receive processing.
func (pm *PathManager) Run() {
	pm.refreshRelay()
	pm.maintainRelays(time.Now())
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	watchdog := time.NewTicker(time.Second)
	defer watchdog.Stop()
	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-pm.closed:
			return
		case <-watchdog.C:
			pm.mu.RLock()
			next := pm.nextFetch
			pm.mu.RUnlock()
			now := time.Now()
			if now.After(next) {
				pm.refreshRelay()
			}
			pm.maintainRelays(now)
			pm.maintainDirect(now)
		case <-heartbeat.C:
			pm.sendPings()
		}
	}
}

// UpdatePeers reconciles the shim/peer set. It is safe to call from a control
// loop; shims are only bound once per peer identity.
func (pm *PathManager) UpdatePeers(peers []protocol.Node) {
	pm.UpdatePeersWithContext(peers, nil, "")
}

// UpdatePeersWithContext also refreshes the local LAN prefixes and public IP
// used to classify direct candidates, so a peer's advertised private address
// is only dialled when it is actually on one of our connected subnets.
func (pm *PathManager) UpdatePeersWithContext(peers []protocol.Node, localSubnets []string, selfPublicIP string) {
	nets := parseLocalNets(localSubnets)
	selfIP := net.ParseIP(selfPublicIP)
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.localNets = nets
	if selfIP != nil {
		pm.selfPublicIP = selfIP
	}
	nextPeers := make(map[string]protocol.Node, len(peers))
	nextKeys := make(map[uint64]string, len(peers))
	nextDirect := make(map[uint64]*directPeer, len(peers))
	for _, p := range peers {
		if p.ID == "" || p.PublicKey == "" {
			continue
		}
		key := protocol.RelayNodeKey(p.ID)
		nextPeers[p.ID] = p
		nextKeys[key] = p.ID
		dp := pm.directPeers[key]
		if dp == nil {
			dp = &directPeer{pathSince: time.Now()}
		} else if dp.pathSince.IsZero() {
			dp.pathSince = time.Now()
		}
		dp.directAddr = peerPrivateAddr(p.DirectEndpoint)
		dp.lanAddr = peerLANAddr(p.DirectEndpoint, nets)
		if dp.lanAddr == nil && dp.directAddr != nil && dp.relayAddr != nil &&
			selfIP != nil && dp.relayAddr.IP.Equal(selfIP) {
			// Persist the same-egress fallback so the ready LAN path is not
			// cleared on the next peer refresh.
			dp.lanAddr = cloneUDPAddr(dp.directAddr)
		}
		dp.v6Addr = peerV6Addr(p.DirectEndpointV6)
		sig := p.DirectEndpoint + "|" + p.DirectEndpointV6 + "|" + strconv.FormatBool(p.Online)
		if pm.peerSig[key] != sig {
			pm.peerSig[key] = sig
			dp.online = p.Online
			log.Printf("path %s: peer=%s online=%v directV4=%q directV6=%q v6accepted=%v",
				pm.nid, p.ID, p.Online, p.DirectEndpoint, p.DirectEndpointV6, dp.v6Addr != nil)
		}
		dp.online = p.Online
		if dp.ready && dp.addr != nil {
			switch dp.kind {
			case "lan":
				if dp.lanAddr == nil || !sameUDPAddr(dp.addr, dp.lanAddr) {
					dp.ready, dp.addr, dp.lastProbe = false, nil, time.Time{}
					dp.pathSince = time.Now()
				}
			case "ipv6":
				if dp.v6Addr == nil || !sameUDPAddr(dp.addr, dp.v6Addr) {
					dp.ready, dp.addr, dp.lastProbe = false, nil, time.Time{}
					dp.pathSince = time.Now()
				}
			}
		}
		nextDirect[key] = dp
		if pm.shims[p.ID] == nil {
			shim, err := newPeerShim(pm, p.ID)
			if err != nil {
				log.Printf("path %s: shim for peer %s: %v", pm.nid, p.ID, err)
				continue
			}
			pm.shims[p.ID] = shim
		}
	}
	for id, shim := range pm.shims {
		if _, ok := nextPeers[id]; !ok {
			shim.close()
			delete(pm.shims, id)
		}
	}
	pm.peers = nextPeers
	pm.peerKeys = nextKeys
	pm.directPeers = nextDirect
}

func parseLocalNets(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func peerPrivateAddr(raw string) *net.UDPAddr {
	if raw == "" {
		return nil
	}
	addr, err := net.ResolveUDPAddr("udp", raw)
	if err != nil || addr.Port == 0 || addr.IP == nil || addr.IP.To4() == nil || !privateIPv4(addr.IP.To4()) {
		return nil
	}
	return addr
}

func peerLANAddr(raw string, nets []*net.IPNet) *net.UDPAddr {
	if raw == "" || len(nets) == 0 {
		return nil
	}
	addr, err := net.ResolveUDPAddr("udp", raw)
	if err != nil || addr.Port == 0 || addr.IP == nil || addr.IP.To4() == nil {
		return nil
	}
	for _, n := range nets {
		if n.Contains(addr.IP) {
			return addr
		}
	}
	return nil
}

func peerV6Addr(raw string) *net.UDPAddr {
	if raw == "" {
		return nil
	}
	addr, err := net.ResolveUDPAddr("udp", raw)
	if err != nil || addr.Port == 0 || addr.IP == nil || addr.IP.To4() != nil {
		return nil
	}
	if !isPublicIPv6(addr.IP) {
		return nil
	}
	return addr
}

// PreparePeers returns a copy of peers whose WireGuard endpoint is the stable
// loopback shim. The caller must pass an empty relay endpoint to ApplyPeers so
// no legacy endpoint fallback leaks through.
func (pm *PathManager) PreparePeers(peers []protocol.Node) []protocol.Node {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]protocol.Node, len(peers))
	copy(out, peers)
	for i := range out {
		if shim := pm.shims[out[i].ID]; shim != nil {
			out[i].Endpoint = shim.endpoint()
		} else {
			out[i].Endpoint = ""
		}
	}
	return out
}

// EndpointFor returns the loopback shim endpoint for a peer, or empty when no
// shim exists.
func (pm *PathManager) EndpointFor(nodeID string) string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if s := pm.shims[nodeID]; s != nil {
		return s.endpoint()
	}
	return ""
}

// SetToken updates the node token used for relay tickets. The coordination
// server rotates the node token on every DeviceNetworks call, so a long-lived
// PathManager created before the sync must be told about the new token or
// every relay-ticket request keeps returning 401.
func (pm *PathManager) SetToken(token string) {
	if token == "" {
		return
	}
	pm.mu.Lock()
	pm.token = token
	pm.nextFetch = time.Time{} // force a ticket refresh on the next tick
	pm.mu.Unlock()
}

// Close tears down every shim and the relay socket.
func (pm *PathManager) Close() {
	pm.closeOnce.Do(func() {
		close(pm.closed)
		pm.mu.Lock()
		for id, shim := range pm.shims {
			shim.close()
			delete(pm.shims, id)
		}
		for _, l := range pm.relayLinks {
			if l.conn != nil {
				_ = l.conn.Close()
				l.conn = nil
			}
			l.ready = false
		}
		if pm.directConn != nil {
			_ = pm.directConn.Close()
			pm.directConn = nil
		}
		if pm.directConn6 != nil {
			_ = pm.directConn6.Close()
			pm.directConn6 = nil
		}
		pm.mu.Unlock()
	})
}

// Status returns a small observable snapshot for the control API.
func (pm *PathManager) Status() map[string]any {
	pm.mu.RLock()
	active := pm.pickRelayLocked()
	ready := active != nil
	relayEP := ""
	relayIndex := 0
	if active != nil {
		relayEP = active.endpoint
		for i, l := range pm.relayLinks {
			if l == active {
				relayIndex = i
				break
			}
		}
	}
	relayReady := 0
	for _, l := range pm.relayLinks {
		if l.ready {
			relayReady++
		}
	}
	shims := len(pm.shims)
	directSocket := pm.directConn != nil
	directSocket6 := pm.directConn6 != nil
	directPeers := len(pm.directPeers)
	directReady := 0
	natType := pm.natType
	publicIP := ""
	if pm.mappedAddr != nil {
		if v4 := pm.mappedAddr.IP.To4(); v4 != nil {
			publicIP = v4.String()
		}
	}
	relayCount := len(pm.relayLinks)
	localSubnets := make([]string, 0, len(pm.localNets))
	for _, n := range pm.localNets {
		localSubnets = append(localSubnets, n.String())
	}
	now := time.Now()
	peerDirect := make(map[string]any, len(pm.directPeers))
	for key, dp := range pm.directPeers {
		id := pm.peerKeys[key]
		if id == "" {
			continue
		}
		path := "discovering"
		switch {
		case dp.ready && dp.addr != nil:
			directReady++
			path = "direct"
		case dp.addr != nil:
			path = "probing"
		}
		// If hole punching has been stuck for the direct dead timeout and a
		// relay is ready, report the path actually carrying traffic instead of
		// leaving the UI on "discovering"/"probing" forever.
		if path != "direct" && relayReady > 0 && !dp.pathSince.IsZero() && now.Sub(dp.pathSince) >= relayFallbackAfter {
			path = "relay"
		}
		state := map[string]any{"path": path}
		if path == "direct" {
			state["kind"] = dp.kind
		}
		if dp.addr != nil {
			state["addr"] = dp.addr.String()
		}
		if dp.lanAddr != nil {
			state["lan"] = dp.lanAddr.String()
		}
		if dp.v6Addr != nil {
			state["ipv6"] = dp.v6Addr.String()
		}
		if dp.relayAddr != nil {
			state["public"] = dp.relayAddr.String()
		}
		if !dp.lastRecv.IsZero() {
			state["lastRecvSec"] = int(now.Sub(dp.lastRecv).Seconds())
		}
		peerDirect[id] = state
	}
	pm.mu.RUnlock()
	return map[string]any{
		"mode":             "relay-v2",
		"relay":            relayEP,
		"relayCandidates":  relayCount,
		"relayReady":       relayReady,
		"relayIndex":       relayIndex,
		"ready":            ready,
		"natType":          natType,
		"publicIP":         publicIP,
		"localSubnets":     localSubnets,
		"shims":            shims,
		"sendFrames":       pm.sendFrames.Load(),
		"recvFrames":       pm.recvFrames.Load(),
		"sendDrops":        pm.sendDrops.Load(),
		"recvDrops":        pm.recvDrops.Load(),
		"directSocket":     directSocket,
		"directSocket6":    directSocket6,
		"directPeers":      directPeers,
		"directReady":      directReady,
		"peerDirect":       peerDirect,
		"directSendFrames": pm.directSendFrames.Load(),
		"directRecvFrames": pm.directRecvFrames.Load(),
	}
}

func (pm *PathManager) refreshRelay() {
	pm.mu.RLock()
	token := pm.token
	pm.mu.RUnlock()
	api := newAPIClient(pm.serverAddr, pm.ctx)
	ticket, err := api.RelayTicket(pm.nid, token)
	if err != nil {
		log.Printf("path %s: relay ticket: %v", pm.nid, err)
		pm.mu.Lock()
		pm.nextFetch = time.Now().Add(30 * time.Second)
		pm.mu.Unlock()
		return
	}
	var sid [16]byte
	raw, err := hex.DecodeString(ticket.SessionID)
	if err != nil || len(raw) != len(sid) {
		log.Printf("path %s: bad relay session id %q", pm.nid, ticket.SessionID)
		pm.mu.Lock()
		pm.nextFetch = time.Now().Add(30 * time.Second)
		pm.mu.Unlock()
		return
	}
	copy(sid[:], raw)
	endpoints := append([]string(nil), ticket.Endpoints...)
	if len(endpoints) == 0 {
		endpoints = []string{ticket.Endpoint}
	}
	clean := make([]string, 0, len(endpoints))
	seen := make(map[string]struct{}, len(endpoints))
	for _, ep := range endpoints {
		ep = strings.TrimSpace(ep)
		if ep == "" {
			continue
		}
		if _, ok := seen[ep]; ok {
			continue
		}
		seen[ep] = struct{}{}
		clean = append(clean, ep)
	}
	endpoints = clean
	if len(endpoints) == 0 {
		log.Printf("path %s: relay ticket has no endpoints", pm.nid)
		pm.mu.Lock()
		pm.nextFetch = time.Now().Add(30 * time.Second)
		pm.mu.Unlock()
		return
	}
	var toClose []*net.UDPConn
	var hellos []*relayLink
	pm.mu.Lock()
	select {
	case <-pm.closed:
		pm.mu.Unlock()
		return
	default:
	}
	sameSession := pm.sessionID == sid
	existing := make(map[string]*relayLink, len(pm.relayLinks))
	for _, l := range pm.relayLinks {
		existing[l.endpoint] = l
	}
	links := make([]*relayLink, 0, len(endpoints))
	keep := make(map[*relayLink]bool, len(endpoints))
	for _, ep := range endpoints {
		if l := existing[ep]; l != nil {
			links = append(links, l)
			keep[l] = true
			continue
		}
		links = append(links, &relayLink{endpoint: ep})
	}
	for _, l := range pm.relayLinks {
		if keep[l] {
			continue
		}
		if l.conn != nil {
			toClose = append(toClose, l.conn)
			l.conn = nil
		}
		l.ready = false
	}
	pm.ticket = ticket
	pm.sessionID = sid
	pm.relayLinks = links
	pm.nextFetch = nextTicketRefresh(ticket.ExpiresAt)
	for _, l := range links {
		if sameSession {
			if l.conn == nil {
				l.nextDial = time.Time{}
			}
			continue
		}
		l.ready = false
		l.nextRegister = time.Time{}
		l.lastRecv.Store(0)
		if l.conn != nil {
			l.attempt = time.Now()
			hellos = append(hellos, l)
		} else {
			l.attempt = time.Time{}
			l.nextDial = time.Time{}
		}
	}
	pm.mu.Unlock()
	for _, conn := range toClose {
		_ = conn.Close()
	}
	for _, l := range hellos {
		pm.sendHello(l)
	}
}

// maintainRelays reconnects pending or failed Relay v2 links and retries HELLO
// until each advertises a working session. Every link stays live so peers can
// reach this node through any advertised relay.
func (pm *PathManager) maintainRelays(now time.Time) {
	var dials, hellos []*relayLink
	var dead []*net.UDPConn
	pm.mu.Lock()
	for _, l := range pm.relayLinks {
		if l.conn == nil {
			if l.nextDial.IsZero() || !now.Before(l.nextDial) {
				l.nextDial = now.Add(5 * time.Second)
				dials = append(dials, l)
			}
			continue
		}
		last := l.lastRecv.Load()
		if last > 0 && now.Unix()-last > 30 {
			log.Printf("path %s: relay %s silent for %ds, reconnecting", pm.nid, l.endpoint, now.Unix()-last)
			dead = append(dead, l.conn)
			l.conn = nil
			l.ready = false
			l.nextDial = now.Add(5 * time.Second)
			continue
		}
		if !l.ready && !l.attempt.IsZero() && now.Sub(l.attempt) > 10*time.Second {
			log.Printf("path %s: relay %s hello timeout, reconnecting", pm.nid, l.endpoint)
			dead = append(dead, l.conn)
			l.conn = nil
			l.ready = false
			l.nextDial = now.Add(5 * time.Second)
			continue
		}
		if !l.ready && now.Unix()-l.lastHello.Unix() >= 3 {
			hellos = append(hellos, l)
		}
	}
	pm.mu.Unlock()
	for _, conn := range dead {
		_ = conn.Close()
	}
	for _, l := range dials {
		pm.dialRelayLink(l)
	}
	for _, l := range hellos {
		pm.sendHello(l)
	}
}

func (pm *PathManager) dialRelayLink(l *relayLink) {
	raddr, err := net.ResolveUDPAddr("udp", l.endpoint)
	if err != nil {
		log.Printf("path %s: relay endpoint %s: %v", pm.nid, l.endpoint, err)
		pm.relayLinkRetry(l)
		return
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		log.Printf("path %s: dial relay %s: %v", pm.nid, l.endpoint, err)
		pm.relayLinkRetry(l)
		return
	}
	pm.mu.Lock()
	select {
	case <-pm.closed:
		pm.mu.Unlock()
		_ = conn.Close()
		return
	default:
	}
	if !pm.hasRelayLinkLocked(l) || l.conn != nil {
		pm.mu.Unlock()
		_ = conn.Close()
		return
	}
	l.conn = conn
	l.addr = raddr
	l.ready = false
	l.attempt = time.Now()
	l.lastHello = time.Time{}
	l.nextRegister = time.Time{}
	l.lastRecv.Store(0)
	pm.mu.Unlock()
	go pm.relayReadLoop(l, conn)
	pm.sendHello(l)
}

func (pm *PathManager) relayLinkRetry(l *relayLink) {
	pm.mu.Lock()
	if pm.hasRelayLinkLocked(l) {
		l.nextDial = time.Now().Add(5 * time.Second)
	}
	pm.mu.Unlock()
}

func (pm *PathManager) hasRelayLinkLocked(l *relayLink) bool {
	for _, cur := range pm.relayLinks {
		if cur == l {
			return true
		}
	}
	return false
}

func (pm *PathManager) pickRelayLocked() *relayLink {
	now := time.Now().Unix()
	var fallback *relayLink
	for _, l := range pm.relayLinks {
		if l.conn == nil || !l.ready {
			continue
		}
		last := l.lastRecv.Load()
		if last > 0 && now-last <= 30 {
			return l
		}
		if fallback == nil {
			fallback = l
		}
	}
	return fallback
}

func nextTicketRefresh(expiresAt int64) time.Time {
	now := time.Now()
	ttl := time.Until(time.Unix(expiresAt, 0))
	if ttl <= 0 {
		return now
	}
	refresh := ttl - 15*time.Minute
	if refresh < ttl/2 {
		refresh = ttl / 2
	}
	if refresh < 30*time.Second {
		refresh = 30 * time.Second
	}
	return now.Add(refresh)
}

func (pm *PathManager) sendHello(l *relayLink) {
	pm.mu.RLock()
	ticket := pm.ticket
	sid := pm.sessionID
	conn := l.conn
	pm.mu.RUnlock()
	if conn == nil {
		return
	}
	f := protocol.RelayFrame{
		Type:      protocol.RelayTypeHello,
		SessionID: sid,
		SenderID:  pm.ownKey,
		Payload:   []byte(ticket.Ticket),
	}
	pm.writeFrame(conn, f)
	pm.mu.Lock()
	if l.conn == conn {
		l.lastHello = time.Now()
	}
	pm.mu.Unlock()
}

func (pm *PathManager) sendPings() {
	type ping struct {
		conn *net.UDPConn
		sid  [16]byte
	}
	pm.mu.RLock()
	pings := make([]ping, 0, len(pm.relayLinks))
	sid := pm.sessionID
	for _, l := range pm.relayLinks {
		if l.conn != nil && l.ready {
			pings = append(pings, ping{conn: l.conn, sid: sid})
		}
	}
	pm.mu.RUnlock()
	for _, p := range pings {
		pm.writeFrame(p.conn, protocol.RelayFrame{
			Type:      protocol.RelayTypePing,
			SessionID: p.sid,
			SenderID:  pm.ownKey,
		})
	}
}

// sendToPeer forwards one opaque WireGuard packet over the best available
// path. Direct is preferred while it has recent proof of life; otherwise the
// packet falls back to Relay v2.
func (pm *PathManager) sendToPeer(nodeID string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	if len(payload) > protocol.RelayMaxPayload {
		pm.sendDrops.Add(1)
		return
	}
	key := protocol.RelayNodeKey(nodeID)
	pm.mu.RLock()
	dp := pm.directPeers[key]
	var directAddr *net.UDPAddr
	var dconn *net.UDPConn
	useDirect := dp != nil && dp.ready && dp.addr != nil &&
		time.Since(dp.lastRecv) <= directDeadAfter
	if useDirect {
		directAddr = cloneUDPAddr(dp.addr)
		if directAddr.IP.To4() != nil {
			dconn = pm.directConn
		} else {
			dconn = pm.directConn6
		}
		useDirect = dconn != nil
	}
	link := pm.pickRelayLocked()
	sid := pm.sessionID
	pm.mu.RUnlock()

	if useDirect {
		f := protocol.RelayFrame{
			Type:        protocol.RelayTypeData,
			SessionID:   sid,
			SenderID:    pm.ownKey,
			RecipientID: key,
			Payload:     payload,
		}
		if pm.writeFrameTo(dconn, f, directAddr) {
			return
		}
		pm.markDirectFailed(key)
	}
	if link == nil {
		pm.sendDrops.Add(1)
		return
	}
	pm.writeFrame(link.conn, protocol.RelayFrame{
		Type:        protocol.RelayTypeData,
		SessionID:   sid,
		SenderID:    pm.ownKey,
		RecipientID: key,
		Payload:     payload,
	})
}

func (pm *PathManager) writeFrame(conn *net.UDPConn, f protocol.RelayFrame) {
	b, err := f.MarshalBinary()
	if err != nil {
		pm.sendDrops.Add(1)
		return
	}
	if _, err := conn.Write(b); err != nil {
		pm.sendDrops.Add(1)
		return
	}
	pm.sendFrames.Add(1)
}

func (pm *PathManager) writeFrameTo(conn *net.UDPConn, f protocol.RelayFrame, addr *net.UDPAddr) bool {
	if conn == nil || addr == nil {
		pm.sendDrops.Add(1)
		return false
	}
	b, err := f.MarshalBinary()
	if err != nil {
		pm.sendDrops.Add(1)
		return false
	}
	if _, err := conn.WriteToUDP(b, addr); err != nil {
		pm.sendDrops.Add(1)
		return false
	}
	pm.sendFrames.Add(1)
	pm.directSendFrames.Add(1)
	return true
}

func (pm *PathManager) markDirectFailed(key uint64) {
	pm.mu.Lock()
	if dp := pm.directPeers[key]; dp != nil {
		dp.ready = false
		dp.lastProbe = time.Time{}
		dp.pathSince = time.Now()
	}
	pm.mu.Unlock()
}

// maintainDirect keeps the direct UDP sockets registered with the relay,
// looks up peer mappings, and probes/keeps alive active direct paths. Relay
// v2 remains the fallback at every step.
func (pm *PathManager) maintainDirect(now time.Time) {
	pm.mu.RLock()
	dconn := pm.directConn
	dconn6 := pm.directConn6
	needV6 := false
	for _, dp := range pm.directPeers {
		if dp.v6Addr != nil || (dp.addr != nil && dp.addr.IP.To4() == nil) {
			needV6 = true
			break
		}
	}
	pm.mu.RUnlock()

	localV6 := firstGlobalIPv6()
	hasV6 := dconn6 != nil || localV6 != ""
	if localV6 != pm.lastV6Logged {
		pm.lastV6Logged = localV6
		log.Printf("path %s: local IPv6=%q v6socket=%v", pm.nid, localV6, dconn6 != nil)
	}
	if dconn == nil {
		dconn = pm.ensureDirectConn(false)
	}
	// Open the v6 socket whenever a peer advertises a v6 candidate, even if
	// local enumeration found no address: the OS picks the source and an
	// injected address (Android) may arrive later.
	if needV6 && dconn6 == nil {
		dconn6 = pm.ensureDirectConn(true)
	}
	hasV6 = dconn6 != nil || localV6 != ""
	if dconn == nil && dconn6 == nil {
		return
	}

	type target struct {
		key  uint64
		id   string
		addr *net.UDPAddr
		kind string
	}
	type registration struct {
		conn *net.UDPConn
		addr *net.UDPAddr
	}
	var lookups, probes, pings []target
	var registers []registration

	pm.mu.Lock()
	ticket := pm.ticket.Ticket
	sid := pm.sessionID
	for _, l := range pm.relayLinks {
		if !l.ready || l.conn == nil || l.addr == nil || ticket == "" {
			continue
		}
		if !l.nextRegister.IsZero() && now.Before(l.nextRegister) {
			continue
		}
		regConn := dconn
		if l.addr.IP.To4() == nil {
			regConn = dconn6
		}
		if regConn == nil {
			continue
		}
		l.nextRegister = now.Add(directRegisterInterval)
		registers = append(registers, registration{conn: regConn, addr: l.addr})
	}
	lookupLink := pm.pickRelayLocked()
	for key, dp := range pm.directPeers {
		peerID := pm.peerKeys[key]
		if !dp.online {
			continue
		}
		if lookupLink != nil {
			interval := directLookupInterval
			if dp.addr == nil || !dp.ready {
				interval = directLookupRetry
			}
			if dp.lastLookup.IsZero() || now.Sub(dp.lastLookup) >= interval {
				lookups = append(lookups, target{key: key, id: peerID})
				dp.lastLookup = now
			}
		}
		if dp.ready && dp.addr != nil {
			if now.Sub(dp.lastRecv) > directDeadAfter {
				dp.ready = false
				dp.addr = nil
				dp.lastProbe = time.Time{}
				dp.pathSince = now
				continue
			}
			if dp.lastPing.IsZero() || now.Sub(dp.lastPing) >= directPingInterval {
				pings = append(pings, target{key: key, id: peerID, addr: cloneUDPAddr(dp.addr), kind: dp.kind})
				dp.lastPing = now
			}
			// Keep probing better candidates (LAN > IPv6 > public) so a path
			// that appears after the current one was established can upgrade
			// without waiting for the active path to fail.
			if dp.lastProbe.IsZero() || now.Sub(dp.lastProbe) >= directUpgradeInterval {
				for _, c := range pm.candidatesLocked(dp) {
					if c.kind == "ipv6" && !hasV6 {
						continue
					}
					if pathRank(c.kind) > pathRank(dp.kind) {
						probes = append(probes, target{key: key, id: peerID, addr: cloneUDPAddr(c.addr), kind: c.kind})
						dp.lastProbe = now
						break
					}
				}
			}
			continue
		}
		rawCands := pm.candidatesLocked(dp)
		cands := rawCands[:0]
		for _, c := range rawCands {
			if c.kind == "ipv6" && !hasV6 {
				continue
			}
			cands = append(cands, c)
		}
		if len(cands) == 0 {
			continue
		}
		if dp.lastProbe.IsZero() || now.Sub(dp.lastProbe) >= directProbeInterval {
			packets := 0
			for _, c := range cands {
				// IPv4 public candidates get a port window: the mapping toward
				// us differs from the mapping toward the relay on CGNAT and
				// port-restricted NATs, and allocations are usually sequential.
				if c.kind == "public" && c.addr.IP.To4() != nil {
					for _, a := range expandPortWindow(c.addr, directPortWindow) {
						probes = append(probes, target{key: key, id: peerID, addr: a, kind: c.kind})
						packets++
					}
					continue
				}
				probes = append(probes, target{key: key, id: peerID, addr: cloneUDPAddr(c.addr), kind: c.kind})
				packets++
			}
			dp.lastProbe = now
			dp.probeIdx++
			if now.Sub(dp.lastProbeLog) >= directProbeLog {
				dp.lastProbeLog = now
				log.Printf("path %s: DIRECT_PROBE peer=%s candidates=%d packets=%d window=%d",
					pm.nid, peerID, len(cands), packets, directPortWindow)
			}
		}
	}
	pm.mu.Unlock()

	for _, reg := range registers {
		pm.writeFrameTo(reg.conn, protocol.RelayFrame{
			Type:      protocol.RelayTypeDirectRegister,
			SessionID: sid,
			SenderID:  pm.ownKey,
			Payload:   []byte(ticket),
		}, reg.addr)
	}
	if lookupLink != nil {
		for _, l := range lookups {
			pm.writeFrame(lookupLink.conn, protocol.RelayFrame{
				Type:        protocol.RelayTypeDirectLookup,
				SessionID:   sid,
				SenderID:    pm.ownKey,
				RecipientID: l.key,
			})
		}
	}
	for _, p := range probes {
		pm.writeCandidate(p.key, p.addr, protocol.RelayTypeDirectProbe, sid, dconn, dconn6)
	}
	for _, p := range pings {
		pm.writeCandidate(p.key, p.addr, protocol.RelayTypeDirectPing, sid, dconn, dconn6)
	}
}

func (pm *PathManager) candidatesLocked(dp *directPeer) []directCandidate {
	var out []directCandidate
	seen := make(map[string]struct{}, 3)
	add := func(kind string, addr *net.UDPAddr) {
		if addr == nil {
			return
		}
		k := addr.String()
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, directCandidate{kind: kind, addr: addr})
	}
	lanAddr := dp.lanAddr
	// If local-subnet enumeration missed the Wi-Fi prefix but both peers sit
	// behind the same public address, the peer's private direct endpoint is
	// still a valid LAN candidate. Keep the fallback behind the same public
	// egress check so unrelated private networks are never probed.
	if lanAddr == nil && dp.directAddr != nil && dp.relayAddr != nil &&
		pm.selfPublicIP != nil && dp.relayAddr.IP.Equal(pm.selfPublicIP) {
		lanAddr = dp.directAddr
	}
	add("lan", lanAddr)
	add("ipv6", dp.v6Addr)
	if dp.relayAddr != nil {
		hairpin := pm.selfPublicIP != nil && dp.relayAddr.IP.Equal(pm.selfPublicIP)
		// Same-LAN peers share the public egress. Keep the public mapping as
		// a last-resort probe: some routers permit hairpin forwarding even
		// when direct LAN UDP is filtered by the host firewall.
		if !hairpin || lanAddr != nil || dp.directAddr != nil {
			add("public", dp.relayAddr)
		}
	}
	return out
}

// expandPortWindow returns addr first, then the nearby ports in both
// directions. CGNAT and port-restricted NATs allocate the mapping toward a new
// destination close to the mapping toward the relay, so a small window raises
// the chance that one probe lands on the peer's real port.
func expandPortWindow(addr *net.UDPAddr, delta int) []*net.UDPAddr {
	if addr == nil {
		return nil
	}
	out := make([]*net.UDPAddr, 0, 1+2*delta)
	out = append(out, cloneUDPAddr(addr))
	for d := 1; d <= delta; d++ {
		for _, offset := range []int{-d, d} {
			p := addr.Port + offset
			if p < 1 || p > 65535 {
				continue
			}
			a := cloneUDPAddr(addr)
			a.Port = p
			out = append(out, a)
		}
	}
	return out
}

func (pm *PathManager) writeCandidate(key uint64, addr *net.UDPAddr, typ uint8, sid [16]byte, dconn, dconn6 *net.UDPConn) {
	conn := dconn
	if addr != nil && addr.IP.To4() == nil {
		conn = dconn6
	}
	pm.writeFrameTo(conn, protocol.RelayFrame{
		Type:        typ,
		SessionID:   sid,
		SenderID:    pm.ownKey,
		RecipientID: key,
	}, addr)
}

func (pm *PathManager) ensureDirectConn(v6 bool) *net.UDPConn {
	pm.mu.RLock()
	conn := pm.directConn
	if v6 {
		conn = pm.directConn6
	}
	pm.mu.RUnlock()
	if conn != nil {
		return conn
	}
	network := "udp4"
	port := pm.directPort4
	bind := &net.UDPAddr{IP: net.IPv4zero, Port: port}
	if v6 {
		network = "udp6"
		port = pm.directPort6
		bind = &net.UDPAddr{IP: net.IPv6unspecified, Port: port}
	}
	if port < 1 || port > 65535 {
		bind.Port = 0
	}
	bound, err := net.ListenUDP(network, bind)
	if err != nil && bind.Port != 0 {
		// The preferred port is busy; fall back to an ephemeral one.
		bind.Port = 0
		bound, err = net.ListenUDP(network, bind)
	}
	if err != nil {
		log.Printf("path %s: open direct socket (%s): %v", pm.nid, network, err)
		return nil
	}
	pm.mu.Lock()
	select {
	case <-pm.closed:
		pm.mu.Unlock()
		_ = bound.Close()
		return nil
	default:
	}
	existing := pm.directConn
	if v6 {
		existing = pm.directConn6
	}
	if existing != nil {
		pm.mu.Unlock()
		_ = bound.Close()
		return existing
	}
	if v6 {
		pm.directConn6 = bound
	} else {
		pm.directConn = bound
	}
	pm.mu.Unlock()
	log.Printf("path %s: direct socket %s bound %s", pm.nid, network, bound.LocalAddr())
	go pm.directReadLoop(bound)
	return bound
}

func (pm *PathManager) directReadLoop(conn *net.UDPConn) {
	defer func() {
		pm.mu.Lock()
		if pm.directConn == conn {
			pm.directConn = nil
		}
		if pm.directConn6 == conn {
			pm.directConn6 = nil
		}
		pm.mu.Unlock()
	}()
	buf := make([]byte, 65535)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if !pm.isCurrentDirect(conn) {
			return
		}
		pm.directRecvFrames.Add(1)
		f, err := protocol.ParseRelayFrame(buf[:n])
		if err != nil {
			pm.recvDrops.Add(1)
			continue
		}
		pm.handleDirectFrame(conn, f, addr)
	}
}

func (pm *PathManager) isCurrentDirect(conn *net.UDPConn) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.directConn == conn || pm.directConn6 == conn
}

func (pm *PathManager) handleDirectFrame(conn *net.UDPConn, f protocol.RelayFrame, addr *net.UDPAddr) {
	pm.mu.RLock()
	ownKey := pm.ownKey
	sid := pm.sessionID
	pm.mu.RUnlock()
	if f.RecipientID != ownKey {
		pm.recvDrops.Add(1)
		return
	}

	now := time.Now()
	switch f.Type {
	case protocol.RelayTypeData:
		_, shim := pm.peerFor(f.SenderID)
		if shim == nil || !pm.noteDirectPeer(f.SenderID, addr, now, true) {
			pm.recvDrops.Add(1)
			return
		}
		shim.inject(f.Payload)
	case protocol.RelayTypeDirectProbe:
		if !pm.noteDirectPeer(f.SenderID, addr, now, true) {
			pm.recvDrops.Add(1)
			return
		}
		ack := protocol.RelayFrame{
			Type:        protocol.RelayTypeDirectProbeAck,
			SessionID:   sid,
			SenderID:    ownKey,
			RecipientID: f.SenderID,
		}
		for i := 0; i < directProbeAcks; i++ {
			pm.writeFrameTo(conn, ack, addr)
		}
	case protocol.RelayTypeDirectProbeAck:
		if !pm.noteDirectPeer(f.SenderID, addr, now, true) {
			pm.recvDrops.Add(1)
		}
	case protocol.RelayTypeDirectPing:
		if !pm.noteDirectPeer(f.SenderID, addr, now, true) {
			pm.recvDrops.Add(1)
			return
		}
		pm.writeFrameTo(conn, protocol.RelayFrame{
			Type:        protocol.RelayTypeDirectPong,
			SessionID:   sid,
			SenderID:    ownKey,
			RecipientID: f.SenderID,
		}, addr)
	case protocol.RelayTypeDirectPong:
		if !pm.noteDirectPeer(f.SenderID, addr, now, false) {
			pm.recvDrops.Add(1)
		}
	case protocol.RelayTypeDirectRegisterAck:
		pm.noteDirectMapping(string(f.Payload))
	default:
		pm.recvDrops.Add(1)
	}
}

// noteDirectPeer validates that a direct frame came from one of the peer's
// expected candidates. LAN/IPv6 candidates must match exactly; a public
// mapping may change source port after NAT rebinding, so only the IP is pinned.
// markReady is false for replies that must not establish a path on their own.
func (pm *PathManager) noteDirectPeer(key uint64, addr *net.UDPAddr, now time.Time, markReady bool) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	dp := pm.directPeers[key]
	if dp == nil {
		return false
	}
	kind := pm.matchCandidateLocked(dp, addr)
	if kind == "" {
		ipKey := addr.IP.String()
		if t, ok := pm.dropLogged[ipKey]; !ok || now.Sub(t) >= 30*time.Second {
			pm.dropLogged[ipKey] = now
			log.Printf("path %s: direct frame from unmatched peer=%s src=%s candidates=%d",
				pm.nid, pm.peerKeys[key], addr, len(pm.candidatesLocked(dp)))
		}
		return false
	}
	if dp.addr != nil && sameUDPAddr(dp.addr, addr) {
		dp.lastRecv = now
		if markReady && !dp.ready {
			dp.ready = true
			dp.lastPing = now
			dp.pathSince = now
			log.Printf("path %s: DIRECT_PROBE established peer=%s addr=%s kind=%s", pm.nid, pm.peerKeys[key], addr, kind)
		}
		if kind == "lan" && dp.lanAddr == nil {
			dp.lanAddr = cloneUDPAddr(addr)
		}
		return true
	}
	if !markReady {
		return true
	}
	if dp.ready && dp.addr != nil && pathRank(dp.kind) > pathRank(kind) {
		// A working higher-priority path must not be displaced by a probe ack
		// from a lower-priority candidate.
		return true
	}
	dp.addr = cloneUDPAddr(addr)
	dp.hintIP = append(net.IP(nil), addr.IP...)
	dp.kind = kind
	dp.ready = true
	dp.lastRecv = now
	dp.lastPing = now
	dp.pathSince = now
	if kind == "lan" && dp.lanAddr == nil {
		dp.lanAddr = cloneUDPAddr(addr)
	}
	log.Printf("path %s: DIRECT_PROBE established peer=%s addr=%s kind=%s", pm.nid, pm.peerKeys[key], addr, kind)
	return true
}

func pathRank(kind string) int {
	switch kind {
	case "lan":
		return 3
	case "ipv6":
		return 2
	case "public":
		return 1
	default:
		return 0
	}
}

func (pm *PathManager) isLocalNetLocked(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range pm.localNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (pm *PathManager) matchCandidateLocked(dp *directPeer, addr *net.UDPAddr) string {
	if addr == nil || addr.IP == nil || addr.IP.IsUnspecified() {
		return ""
	}
	for _, c := range pm.candidatesLocked(dp) {
		if !c.addr.IP.Equal(addr.IP) {
			// When a local gateway router performs inbound DNAT + LAN
			// masquerade (SNAT to the gateway's LAN IP) on hole-punched
			// packets from a remote peer while preserving the peer's public
			// source port, accept the gateway address as the public path so
			// replies traverse the router's conntrack entry back to the peer.
			if c.kind == "public" && dp.lanAddr == nil && c.addr.Port == addr.Port && pm.isLocalNetLocked(addr.IP) {
				return "public"
			}
			continue
		}
		if c.kind == "lan" && c.addr.Port != addr.Port {
			continue
		}
		return c.kind
	}
	if dp.lanAddr != nil && addr.IP.Equal(dp.lanAddr.IP) {
		return ""
	}
	if dp.hintIP != nil && addr.IP.Equal(dp.hintIP) {
		return "public"
	}
	return ""
}

// MappedPublicIP returns the most recent relay-observed public IPv4 address of
// this node's direct socket, or empty string if none has been observed yet.
func (pm *PathManager) MappedPublicIP() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if pm.mappedAddr == nil || pm.mappedAddr.IP == nil {
		return ""
	}
	if v4 := pm.mappedAddr.IP.To4(); v4 != nil {
		return v4.String()
	}
	return pm.mappedAddr.IP.String()
}

func (pm *PathManager) noteDirectMapping(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		pm.mu.Lock()
		pm.natType = "unknown"
		pm.mu.Unlock()
		return
	}
	addr, err := net.ResolveUDPAddr("udp", raw)
	if err != nil || addr.Port == 0 || addr.IP == nil || addr.IP.IsUnspecified() {
		return
	}
	now := time.Now()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.mappedAddr != nil && now.Sub(pm.mappedAt) < 10*time.Minute {
		if pm.mappedAddr.Port == addr.Port && pm.mappedAddr.IP.Equal(addr.IP) {
			pm.natType = "cone"
			pm.selfPublicIP = append(net.IP(nil), addr.IP...)
		} else {
			pm.natType = "symmetric"
		}
	} else if pm.selfPublicIP != nil && !pm.selfPublicIP.Equal(addr.IP) {
		pm.natType = "symmetric"
	} else if pm.natType == "" {
		pm.natType = "unknown"
	}
	pm.mappedAddr = cloneUDPAddr(addr)
	pm.mappedAt = now
	if s := addr.String(); s != pm.lastMapLog {
		pm.lastMapLog = s
		log.Printf("path %s: relay-observed mapping=%s nat=%s", pm.nid, s, pm.natType)
	}
}

func (pm *PathManager) handleDirectPeer(f protocol.RelayFrame) {
	pm.mu.RLock()
	ownKey := pm.ownKey
	pm.mu.RUnlock()
	if f.SenderID != ownKey {
		pm.recvDrops.Add(1)
		return
	}
	pm.mu.Lock()
	dp := pm.directPeers[f.RecipientID]
	if dp == nil {
		pm.mu.Unlock()
		return
	}
	if len(f.Payload) == 0 {
		// Registration and lookup race across two sockets. Retry a few times
		// quickly, then fall back to the normal lookup cadence so an offline
		// peer cannot generate a steady query stream.
		if dp.lookupMisses < 5 {
			dp.lookupMisses++
			dp.lastLookup = time.Time{}
		}
		pm.mu.Unlock()
		return
	}
	pm.mu.Unlock()
	addr, err := net.ResolveUDPAddr("udp", string(f.Payload))
	if err != nil || addr.Port == 0 || addr.IP == nil || addr.IP.IsUnspecified() {
		pm.recvDrops.Add(1)
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	dp = pm.directPeers[f.RecipientID]
	if dp == nil {
		return
	}
	dp.lookupMisses = 0
	old := dp.relayAddr
	changed := !sameUDPAddr(old, addr)
	dp.relayAddr = cloneUDPAddr(addr)
	if changed {
		log.Printf("path %s: peer=%s relay-observed endpoint=%s", pm.nid, pm.peerKeys[f.RecipientID], addr)
	}
	if !changed {
		return
	}
	if dp.ready && dp.kind == "public" {
		dp.ready = false
		dp.addr = nil
		dp.hintIP = nil
		dp.lastProbe = time.Time{}
		dp.lastRecv = time.Time{}
		dp.pathSince = time.Now()
	}
}

// DirectEndpoints returns the advertised LAN IPv4 endpoint, global IPv6
// endpoint, and current NAT classification. Empty strings mean the family is
// unavailable. The endpoints use the dedicated direct sockets, never the
// WireGuard port, so publishing them does not require a 1:1 port mapping.
func (pm *PathManager) DirectEndpoints() (string, string, string) {
	pm.mu.RLock()
	conn4 := pm.directConn
	conn6 := pm.directConn6
	nat := pm.natType
	pm.mu.RUnlock()
	if conn4 == nil {
		conn4 = pm.ensureDirectConn(false)
	}
	var ep4, ep6 string
	if conn4 != nil {
		ip := strings.TrimSpace(os.Getenv("SNET_LOCAL_IP"))
		if ip == "" {
			ip, _ = LocalIPForServer(pm.serverAddr)
		}
		if ip != "" {
			ep4 = net.JoinHostPort(ip, strconv.Itoa(conn4.LocalAddr().(*net.UDPAddr).Port))
		}
	}
	if ip6 := firstGlobalIPv6(); ip6 != "" {
		if conn6 == nil {
			conn6 = pm.ensureDirectConn(true)
		}
		if conn6 != nil {
			ep6 = net.JoinHostPort(ip6, strconv.Itoa(conn6.LocalAddr().(*net.UDPAddr).Port))
		}
	}
	if nat == "" {
		nat = "unknown"
	}
	return ep4, ep6, nat
}

func isVirtualInterfaceName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if n == "lo" || n == "docker0" {
		return true
	}
	for _, prefix := range []string{"veth", "br-", "virbr", "cni", "flannel", "kube", "tun", "tap", "wg", "tailscale", "zt"} {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// isPublicIPv6 reports whether ip is a globally routable, non-ULA IPv6 address.
func isPublicIPv6(ip net.IP) bool {
	return ip != nil && ip.To4() == nil && ip.IsGlobalUnicast() &&
		!ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsMulticast() && !ip.IsUnspecified()
}

func firstGlobalIPv6() string {
	if v := advertisedIPv6Value(); v != "" {
		return v
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || isVirtualInterfaceName(ifc.Name) {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if isPublicIPv6(ipn.IP) {
				return ipn.IP.String()
			}
		}
	}
	dumpInterfaces()
	return ""
}

func (pm *PathManager) directReady(nodeID string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	dp := pm.directPeers[protocol.RelayNodeKey(nodeID)]
	return dp != nil && dp.ready
}

func sameUDPAddr(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Port == b.Port && a.IP.Equal(b.IP) && a.Zone == b.Zone
}

func cloneUDPAddr(a *net.UDPAddr) *net.UDPAddr {
	if a == nil {
		return nil
	}
	return &net.UDPAddr{IP: append(net.IP(nil), a.IP...), Port: a.Port, Zone: a.Zone}
}

func (pm *PathManager) relayReadLoop(l *relayLink, conn *net.UDPConn) {
	defer pm.relayLinkDown(l, conn)
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		if !pm.isLinkConn(l, conn) {
			return
		}
		l.lastRecv.Store(time.Now().Unix())
		pm.recvFrames.Add(1)
		f, err := protocol.ParseRelayFrame(buf[:n])
		if err != nil {
			pm.recvDrops.Add(1)
			continue
		}
		switch f.Type {
		case protocol.RelayTypeHelloAck:
			pm.mu.Lock()
			if l.conn == conn {
				l.ready = true
				l.attempt = time.Time{}
				log.Printf("path %s: relay session established via %s", pm.nid, l.endpoint)
			}
			pm.mu.Unlock()
		case protocol.RelayTypePong:
			// Any valid PONG proves the relay still has our mapping.
		case protocol.RelayTypeError:
			now := time.Now()
			if now.Sub(l.lastErrLog) >= 30*time.Second {
				l.lastErrLog = now
				log.Printf("path %s: relay error from %s: %s", pm.nid, l.endpoint, string(f.Payload))
			}
		case protocol.RelayTypeDirectPeer:
			pm.handleDirectPeer(f)
		case protocol.RelayTypeData:
			_, shim := pm.peerFor(f.SenderID)
			if shim == nil {
				pm.recvDrops.Add(1)
				continue
			}
			shim.inject(f.Payload)
		default:
			pm.recvDrops.Add(1)
		}
	}
}

func (pm *PathManager) relayLinkDown(l *relayLink, conn *net.UDPConn) {
	pm.mu.Lock()
	if l.conn == conn {
		l.conn = nil
		l.ready = false
		l.nextDial = time.Now().Add(5 * time.Second)
	}
	pm.mu.Unlock()
	_ = conn.Close()
}

func (pm *PathManager) isLinkConn(l *relayLink, conn *net.UDPConn) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return l.conn == conn
}

func (pm *PathManager) peerFor(key uint64) (string, *peerShim) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	id := pm.peerKeys[key]
	if id == "" {
		return "", nil
	}
	return id, pm.shims[id]
}

// peerShim is a loopback UDP socket with two jobs: forward WireGuard's
// outbound packets into the PathManager, and inject relayed inbound packets
// into WireGuard from the exact address WireGuard considers the peer endpoint.
type peerShim struct {
	nodeID    string
	conn      *net.UDPConn
	pm        *PathManager
	closeOnce sync.Once
}

func newPeerShim(pm *PathManager, nodeID string) (*peerShim, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return nil, err
	}
	s := &peerShim{nodeID: nodeID, conn: conn, pm: pm}
	go s.readLoop()
	return s, nil
}

func (s *peerShim) endpoint() string {
	if s.conn == nil {
		return ""
	}
	return s.conn.LocalAddr().String()
}

func (s *peerShim) readLoop() {
	buf := make([]byte, 65535)
	for {
		n, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n > 0 {
			s.pm.sendToPeer(s.nodeID, buf[:n])
		}
	}
}

func (s *peerShim) inject(payload []byte) {
	if len(payload) == 0 {
		return
	}
	if _, err := s.conn.WriteToUDP(payload, s.pm.wgAddr); err != nil {
		s.pm.recvDrops.Add(1)
	}
}

func (s *peerShim) close() {
	s.closeOnce.Do(func() {
		if s.conn != nil {
			_ = s.conn.Close()
		}
	})
}
