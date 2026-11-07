package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"snet/internal/protocol"
	"snet/internal/relay"
)

const pathTestSecret = "0123456789abcdef0123456789abcdef"

func TestPathManagerRelayLoopbackShims(t *testing.T) {
	signer, err := relay.NewTicketSigner(pathTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	relaySrv, err := relay.Listen("127.0.0.1:0", signer, relay.Options{SessionTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer relaySrv.Close()
	go relaySrv.Serve()

	tokens := map[string]string{"tok-a": "node-a", "tok-b": "node-b"}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/networks/net-1/relay-ticket" {
			http.NotFound(w, r)
			return
		}
		token := r.Header.Get("Authorization")
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
		nodeID := tokens[token]
		if nodeID == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		claims, err := relay.NewClaims("net-1", nodeID, time.Hour)
		if err != nil {
			t.Errorf("claims: %v", err)
			return
		}
		ticket, err := signer.Sign(claims)
		if err != nil {
			t.Errorf("sign: %v", err)
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.RelayTicketResp{
			Endpoint:  relaySrv.Addr(),
			Ticket:    ticket,
			SessionID: claims.SessionID,
			NodeID:    nodeID,
			NodeKey:   protocol.RelayNodeKeyHex(nodeID),
			ExpiresAt: claims.ExpiresAt,
		})
	}))
	defer api.Close()

	wgA := mustUDPListener(t)
	defer wgA.Close()
	wgB := mustUDPListener(t)
	defer wgB.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pmA := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: wgA.LocalAddr().(*net.UDPAddr).Port}, api.URL, "")
	defer pmA.Close()
	pmB := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-b", Token: "tok-b", Port: wgB.LocalAddr().(*net.UDPAddr).Port}, api.URL, "")
	defer pmB.Close()

	peerA := protocol.Node{ID: "node-a", PublicKey: "a"}
	peerB := protocol.Node{ID: "node-b", PublicKey: "b"}
	pmA.UpdatePeers([]protocol.Node{peerB})
	pmB.UpdatePeers([]protocol.Node{peerA})
	go pmA.Run()
	go pmB.Run()

	waitRelayReady(t, pmA)
	waitRelayReady(t, pmB)

	shimForB := pmA.EndpointFor("node-b")
	if shimForB == "" {
		t.Fatal("no shim for node-b")
	}
	dst, err := net.ResolveUDPAddr("udp", shimForB)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("opaque-wireguard-ciphertext")
	if _, err := wgA.WriteToUDP(payload, dst); err != nil {
		t.Fatal(err)
	}

	_ = wgB.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := wgB.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("relay path did not deliver: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("payload = %q, want %q", buf[:n], payload)
	}

	waitDirectReady(t, pmA, "node-b")
	waitDirectReady(t, pmB, "node-a")
	status := pmA.Status()
	peerDirect, ok := status["peerDirect"].(map[string]any)
	if !ok {
		t.Fatalf("status peerDirect missing: %+v", status)
	}
	entry, ok := peerDirect["node-b"].(map[string]any)
	if !ok || entry["path"] != "direct" {
		t.Fatalf("node-b path = %+v, want direct", entry)
	}

	before := relaySrv.Stats().BytesIn
	directPayload := make([]byte, 1200)
	for i := range directPayload {
		directPayload[i] = byte(i)
	}
	if _, err := wgA.WriteToUDP(directPayload, dst); err != nil {
		t.Fatal(err)
	}
	_ = wgB.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _, err = wgB.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("direct path did not deliver: %v", err)
	}
	if string(buf[:n]) != string(directPayload) {
		t.Fatalf("direct payload mismatch: got %d bytes", n)
	}
	relayBytes := relaySrv.Stats().BytesIn - before
	if relayBytes >= uint64(len(directPayload)) {
		t.Fatalf("direct payload traversed relay (%d bytes)", relayBytes)
	}

	pmA.mu.Lock()
	dp := pmA.directPeers[protocol.RelayNodeKey("node-b")]
	dp.lastRecv = time.Now().Add(-directDeadAfter - time.Second)
	pmA.mu.Unlock()

	before = relaySrv.Stats().BytesIn
	fallbackPayload := []byte("relay-fallback-after-direct-timeout")
	if _, err := wgA.WriteToUDP(fallbackPayload, dst); err != nil {
		t.Fatal(err)
	}
	_ = wgB.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _, err = wgB.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("relay fallback did not deliver: %v", err)
	}
	if string(buf[:n]) != string(fallbackPayload) {
		t.Fatalf("fallback payload = %q, want %q", buf[:n], fallbackPayload)
	}
	relayBytes = relaySrv.Stats().BytesIn - before
	if relayBytes < uint64(len(fallbackPayload)) {
		t.Fatalf("fallback payload did not traverse relay (%d bytes)", relayBytes)
	}
}

func TestPathManagerPrefersLANWithoutRelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wgA := mustUDPListener(t)
	defer wgA.Close()
	wgB := mustUDPListener(t)
	defer wgB.Close()
	pmA := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: wgA.LocalAddr().(*net.UDPAddr).Port}, "127.0.0.1:9", "")
	defer pmA.Close()
	pmB := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-b", Token: "tok-b", Port: wgB.LocalAddr().(*net.UDPAddr).Port}, "127.0.0.1:9", "")
	defer pmB.Close()

	connA := pmA.ensureDirectConn(false)
	connB := pmB.ensureDirectConn(false)
	if connA == nil || connB == nil {
		t.Fatal("direct sockets not created")
	}
	peerA := protocol.Node{ID: "node-a", PublicKey: "a", DirectEndpoint: net.JoinHostPort("127.0.0.1", fmt.Sprint(connA.LocalAddr().(*net.UDPAddr).Port))}
	peerB := protocol.Node{ID: "node-b", PublicKey: "b", DirectEndpoint: net.JoinHostPort("127.0.0.1", fmt.Sprint(connB.LocalAddr().(*net.UDPAddr).Port))}
	pmA.UpdatePeersWithContext([]protocol.Node{peerB}, []string{"127.0.0.0/8"}, "")
	pmB.UpdatePeersWithContext([]protocol.Node{peerA}, []string{"127.0.0.0/8"}, "")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (!pmA.directReady("node-b") || !pmB.directReady("node-a")) {
		pmA.maintainDirect(time.Now())
		pmB.maintainDirect(time.Now())
		time.Sleep(20 * time.Millisecond)
	}
	if !pmA.directReady("node-b") || !pmB.directReady("node-a") {
		t.Fatalf("LAN direct did not converge: A=%+v B=%+v", pmA.Status(), pmB.Status())
	}
	for _, pm := range []*PathManager{pmA, pmB} {
		peerDirect := pm.Status()["peerDirect"].(map[string]any)
		for _, state := range peerDirect {
			if got := state.(map[string]any)["kind"]; got != "lan" {
				t.Fatalf("path kind = %v, want lan", got)
			}
		}
	}

	dst, err := net.ResolveUDPAddr("udp", pmA.EndpointFor("node-b"))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("lan-direct-payload")
	if _, err := wgA.WriteToUDP(payload, dst); err != nil {
		t.Fatal(err)
	}
	_ = wgB.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := wgB.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("LAN direct delivery: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("payload = %q, want %q", buf[:n], payload)
	}
}

func TestPathManagerNATClassification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	pm.mu.Lock()
	pm.selfPublicIP = net.ParseIP("203.0.113.5")
	pm.mu.Unlock()
	pm.noteDirectMapping("203.0.113.5:40000")
	if got := pm.Status()["natType"]; got != "unknown" {
		t.Fatalf("first mapping natType = %v, want unknown", got)
	}
	pm.noteDirectMapping("203.0.113.5:40000")
	if got := pm.Status()["natType"]; got != "cone" {
		t.Fatalf("stable mapping natType = %v, want cone", got)
	}
	pm.noteDirectMapping("203.0.113.5:40001")
	if got := pm.Status()["natType"]; got != "symmetric" {
		t.Fatalf("port-changing mapping natType = %v, want symmetric", got)
	}
	pm.noteDirectMapping("198.51.100.9:40001")
	if got := pm.Status()["natType"]; got != "symmetric" {
		t.Fatalf("IP-changing mapping natType = %v, want symmetric", got)
	}
}

func waitDirectReady(t *testing.T, pm *PathManager, nodeID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pm.directReady(nodeID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("direct path %s -> %s did not become ready: %+v", pm.nid, nodeID, pm.Status())
}

func waitRelayReady(t *testing.T, pm *PathManager) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ready, _ := pm.Status()["ready"].(bool); ready {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("relay path %s did not become ready: %+v", pm.nid, pm.Status())
}

func mustDialUDP(t *testing.T, addr *net.UDPAddr) *net.UDPConn {
	t.Helper()
	c, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustUDPListener(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPathManagerSelectsFirstReadyRelayLink(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	defer pm.Close()
	down := mustUDPListener(t)
	ready := mustUDPListener(t)
	defer down.Close()
	defer ready.Close()
	downConn := mustDialUDP(t, down.LocalAddr().(*net.UDPAddr))
	readyConn := mustDialUDP(t, ready.LocalAddr().(*net.UDPAddr))
	defer downConn.Close()
	defer readyConn.Close()

	pm.mu.Lock()
	pm.ticket = protocol.RelayTicketResp{Ticket: "ticket"}
	pm.sessionID = [16]byte{1}
	pm.relayLinks = []*relayLink{
		{endpoint: "relay-a:8092", conn: downConn},
		{endpoint: "relay-b:8092", conn: readyConn, ready: true},
	}
	pm.relayLinks[1].lastRecv.Store(time.Now().Unix())
	pm.mu.Unlock()

	status := pm.Status()
	if got := status["relayCandidates"]; got != 2 {
		t.Fatalf("relayCandidates = %v, want 2", got)
	}
	if got := status["relayReady"]; got != 1 {
		t.Fatalf("relayReady = %v, want 1", got)
	}
	if got := status["relayIndex"]; got != 1 {
		t.Fatalf("relayIndex = %v, want 1", got)
	}

	pm.sendToPeer("node-b", []byte("opaque"))
	_ = ready.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, err := ready.Read(buf)
	if err != nil {
		t.Fatalf("ready relay received nothing: %v", err)
	}
	f, err := protocol.ParseRelayFrame(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != protocol.RelayTypeData || string(f.Payload) != "opaque" {
		t.Fatalf("unexpected frame: %+v", f)
	}
	_ = down.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := down.Read(buf); err == nil {
		t.Fatal("unready relay link received data")
	}
}

func TestPathManagerRegistersDirectOnEveryReadyRelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	defer pm.Close()
	direct := mustUDPListener(t)
	relayA := mustUDPListener(t)
	relayB := mustUDPListener(t)
	defer direct.Close()
	defer relayA.Close()
	defer relayB.Close()

	pm.mu.Lock()
	pm.directConn = direct
	pm.ticket = protocol.RelayTicketResp{Ticket: "ticket"}
	pm.sessionID = [16]byte{2}
	pm.relayLinks = []*relayLink{
		{endpoint: "relay-a:8092", conn: relayA, addr: relayA.LocalAddr().(*net.UDPAddr), ready: true},
		{endpoint: "relay-b:8092", conn: relayB, addr: relayB.LocalAddr().(*net.UDPAddr), ready: true},
	}
	pm.relayLinks[0].lastRecv.Store(time.Now().Unix())
	pm.relayLinks[1].lastRecv.Store(time.Now().Unix())
	pm.mu.Unlock()

	pm.maintainDirect(time.Now())

	for i, conn := range []*net.UDPConn{relayA, relayB} {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 2048)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("relay %d received no direct register: %v", i, err)
		}
		f, err := protocol.ParseRelayFrame(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		if f.Type != protocol.RelayTypeDirectRegister || string(f.Payload) != "ticket" {
			t.Fatalf("relay %d frame = %+v", i, f)
		}
	}
}

func TestPathManagerReconnectsSilentRelayLink(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	defer pm.Close()
	conn := mustUDPListener(t)
	defer conn.Close()
	link := &relayLink{endpoint: "127.0.0.1:9", conn: conn, ready: true}
	link.lastRecv.Store(time.Now().Add(-time.Minute).Unix())
	pm.mu.Lock()
	pm.relayLinks = []*relayLink{link}
	pm.mu.Unlock()

	pm.maintainRelays(time.Now())

	pm.mu.RLock()
	gotConn := link.conn
	ready := link.ready
	nextDial := link.nextDial
	pm.mu.RUnlock()
	if gotConn != nil || ready {
		t.Fatalf("silent link still attached: conn=%v ready=%v", gotConn, ready)
	}
	if time.Until(nextDial) <= 0 || time.Until(nextDial) > 6*time.Second {
		t.Fatalf("nextDial = %v, want reconnect within 6s", nextDial)
	}
}

func mustPathAddr(t *testing.T, raw string) *net.UDPAddr {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", raw)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}

func TestPathManagerRejectsWrongLANPort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	key := protocol.RelayNodeKey("node-b")
	dp := &directPeer{lanAddr: mustPathAddr(t, "192.168.1.10:51820")}
	pm.mu.Lock()
	pm.directPeers[key] = dp
	pm.mu.Unlock()

	if pm.noteDirectPeer(key, mustPathAddr(t, "192.168.1.10:51821"), time.Now(), true) {
		t.Fatal("wrong LAN port should be rejected")
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if dp.ready || dp.addr != nil {
		t.Fatalf("rejected LAN frame changed peer state: %+v", dp)
	}
}

func TestPathManagerRejectsUnknownPublicSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	key := protocol.RelayNodeKey("node-b")
	dp := &directPeer{relayAddr: mustPathAddr(t, "203.0.113.5:51820")}
	pm.mu.Lock()
	pm.directPeers[key] = dp
	pm.mu.Unlock()

	if pm.noteDirectPeer(key, mustPathAddr(t, "198.51.100.7:51820"), time.Now(), true) {
		t.Fatal("unknown public source should be rejected")
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if dp.ready || dp.addr != nil {
		t.Fatalf("rejected public frame changed peer state: %+v", dp)
	}
}

func TestPathManagerAllowsPublicPortRebinding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	key := protocol.RelayNodeKey("node-b")
	dp := &directPeer{relayAddr: mustPathAddr(t, "203.0.113.5:51820")}
	pm.mu.Lock()
	pm.directPeers[key] = dp
	pm.mu.Unlock()

	rebound := mustPathAddr(t, "203.0.113.5:40000")
	if !pm.noteDirectPeer(key, rebound, time.Now(), true) {
		t.Fatal("public NAT rebinding should be accepted")
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if !dp.ready || dp.kind != "public" || dp.addr == nil || dp.addr.Port != 40000 {
		t.Fatalf("rebound public mapping not accepted: %+v", dp)
	}
}

func TestPathManagerRejectsDirectFrameWhenNoCandidate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	key := protocol.RelayNodeKey("node-b")
	dp := &directPeer{}
	pm.mu.Lock()
	pm.directPeers[key] = dp
	pm.mu.Unlock()

	if pm.noteDirectPeer(key, mustPathAddr(t, "203.0.113.5:51820"), time.Now(), true) {
		t.Fatal("direct frame without candidates should be rejected")
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if dp.ready || dp.addr != nil {
		t.Fatalf("rejected direct frame changed peer state: %+v", dp)
	}
}

func TestPathManagerCandidatePriority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	dp := &directPeer{
		lanAddr:   mustPathAddr(t, "192.168.1.10:51820"),
		v6Addr:    mustPathAddr(t, "[2001:db8::1]:51820"),
		relayAddr: mustPathAddr(t, "203.0.113.5:51820"),
	}
	pm.mu.RLock()
	cands := pm.candidatesLocked(dp)
	pm.mu.RUnlock()
	if len(cands) != 3 {
		t.Fatalf("candidates = %+v, want 3", cands)
	}
	want := []string{"lan", "ipv6", "public"}
	for i, w := range want {
		if cands[i].kind != w {
			t.Fatalf("candidate[%d] kind = %q, want %q (all=%+v)", i, cands[i].kind, w, cands)
		}
	}
}

func TestPathManagerLowPriorityAckDoesNotOverrideHighPriority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	key := protocol.RelayNodeKey("node-b")
	lan := mustPathAddr(t, "192.168.1.10:51820")
	dp := &directPeer{
		lanAddr:   lan,
		relayAddr: mustPathAddr(t, "203.0.113.5:51820"),
		addr:      lan,
		kind:      "lan",
		ready:     true,
	}
	pm.mu.Lock()
	pm.directPeers[key] = dp
	pm.mu.Unlock()

	if !pm.noteDirectPeer(key, mustPathAddr(t, "203.0.113.5:40000"), time.Now(), true) {
		t.Fatal("valid public ACK should be accepted (but not applied)")
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if dp.kind != "lan" || dp.addr == nil || !sameUDPAddr(dp.addr, lan) {
		t.Fatalf("low-priority ACK displaced LAN path: %+v", dp)
	}
}

// TestPathManagerAcceptsDataFromLowerPrioritySourceWhenHigherReady documents
// the current DATA behavior: a frame from a lower-priority candidate is still
// injected while a higher-priority path is ready, and it does not displace the
// ready path. 设计意图：不因路径抖动丢包；若评审要求严格单向，需改为 drop 并报告。
func TestPathManagerAcceptsDataFromLowerPrioritySourceWhenHigherReady(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	defer pm.Close()
	key := protocol.RelayNodeKey("node-b")
	lan := mustPathAddr(t, "192.168.1.10:51820")
	public := mustPathAddr(t, "203.0.113.5:40000")
	dp := &directPeer{
		lanAddr:   lan,
		relayAddr: mustPathAddr(t, "203.0.113.5:51820"),
		addr:      lan,
		kind:      "lan",
		ready:     true,
	}
	shimConn := mustUDPListener(t)
	defer shimConn.Close()
	wg := mustUDPListener(t)
	defer wg.Close()
	pm.mu.Lock()
	pm.directPeers[key] = dp
	pm.peerKeys[key] = "node-b"
	pm.shims["node-b"] = &peerShim{nodeID: "node-b", conn: shimConn, pm: pm}
	pm.wgAddr = wg.LocalAddr().(*net.UDPAddr)
	pm.mu.Unlock()

	payload := []byte("data-from-lower-priority-public")
	pm.handleDirectFrame(nil, protocol.RelayFrame{
		Type:        protocol.RelayTypeData,
		SenderID:    key,
		RecipientID: pm.ownKey,
		Payload:     payload,
	}, public)

	_ = wg.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, _, err := wg.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("lower-priority DATA was not injected: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("injected payload = %q, want %q", buf[:n], payload)
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if dp.kind != "lan" || dp.addr == nil || !sameUDPAddr(dp.addr, lan) || !dp.ready {
		t.Fatalf("lower-priority DATA displaced ready LAN path: %+v", dp)
	}
}

func TestPathManagerSymmetricNATStaysRelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	defer pm.Close()
	key := protocol.RelayNodeKey("node-b")
	directConn := mustUDPListener(t)
	defer directConn.Close()
	candidate := mustUDPListener(t)
	defer candidate.Close()
	relay := mustUDPListener(t)
	defer relay.Close()
	relayConn := mustDialUDP(t, relay.LocalAddr().(*net.UDPAddr))
	defer relayConn.Close()

	pm.mu.Lock()
	pm.directConn = directConn
	pm.directPeers[key] = &directPeer{relayAddr: candidate.LocalAddr().(*net.UDPAddr)}
	pm.peerKeys[key] = "node-b"
	pm.mu.Unlock()

	base := time.Now()
	for i := 0; i < 6; i++ {
		pm.maintainDirect(base.Add(time.Duration(i) * 4 * time.Second))
	}
	pm.mu.RLock()
	dp := pm.directPeers[key]
	ready := dp.ready
	addr := dp.addr
	pm.mu.RUnlock()
	if ready || addr != nil {
		t.Fatalf("direct became ready without ACK: ready=%v addr=%v", ready, addr)
	}
	if pm.directSendFrames.Load() == 0 {
		t.Fatal("maintainDirect sent no direct probes")
	}
	status := pm.Status()
	peerDirect := status["peerDirect"].(map[string]any)
	entry := peerDirect["node-b"].(map[string]any)
	if path, _ := entry["path"].(string); path != "probing" && path != "discovering" {
		t.Fatalf("peerDirect path = %q, want probing/discovering", path)
	}

	pm.mu.Lock()
	pm.relayLinks = []*relayLink{{endpoint: "relay", conn: relayConn, ready: true}}
	pm.relayLinks[0].lastRecv.Store(time.Now().Unix())
	pm.sessionID = [16]byte{3}
	pm.mu.Unlock()
	beforeDirect := pm.directSendFrames.Load()
	payload := []byte("must-use-relay")
	pm.sendToPeer("node-b", payload)

	_ = relay.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, err := relay.Read(buf)
	if err != nil {
		t.Fatalf("relay received nothing: %v", err)
	}
	f, err := protocol.ParseRelayFrame(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != protocol.RelayTypeData || string(f.Payload) != string(payload) {
		t.Fatalf("relay frame = %+v, want DATA payload", f)
	}
	if got := pm.directSendFrames.Load(); got != beforeDirect {
		t.Fatalf("sendToPeer wrote to direct socket: before=%d after=%d", beforeDirect, got)
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if dp.ready || dp.addr != nil {
		t.Fatalf("direct state changed after relay send: ready=%v addr=%v", dp.ready, dp.addr)
	}
}

func TestPathManagerDirectSendFailureFallsBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pm := NewPathManager(ctx, "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9", "")
	defer pm.Close()
	key := protocol.RelayNodeKey("node-b")
	lan := mustPathAddr(t, "192.168.1.10:51820")
	closedDirect := mustUDPListener(t)
	if err := closedDirect.Close(); err != nil {
		t.Fatal(err)
	}
	relay := mustUDPListener(t)
	defer relay.Close()
	relayConn := mustDialUDP(t, relay.LocalAddr().(*net.UDPAddr))
	defer relayConn.Close()

	dp := &directPeer{
		addr:     lan,
		lanAddr:  lan,
		kind:     "lan",
		ready:    true,
		lastRecv: time.Now(),
	}
	pm.mu.Lock()
	pm.directConn = closedDirect
	pm.directPeers[key] = dp
	pm.peerKeys[key] = "node-b"
	pm.relayLinks = []*relayLink{{endpoint: "relay", conn: relayConn, ready: true}}
	pm.relayLinks[0].lastRecv.Store(time.Now().Unix())
	pm.sessionID = [16]byte{4}
	pm.mu.Unlock()

	payload := []byte("direct-write-fails")
	pm.sendToPeer("node-b", payload)

	_ = relay.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 2048)
	n, err := relay.Read(buf)
	if err != nil {
		t.Fatalf("relay received nothing after direct failure: %v", err)
	}
	f, err := protocol.ParseRelayFrame(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != protocol.RelayTypeData || string(f.Payload) != string(payload) {
		t.Fatalf("relay frame = %+v, want DATA payload", f)
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if dp.ready {
		t.Fatal("markDirectFailed did not clear dp.ready")
	}
	if !dp.lastProbe.IsZero() {
		t.Fatalf("markDirectFailed did not reset lastProbe: %v", dp.lastProbe)
	}
	if pm.sendDrops.Load() == 0 {
		t.Fatal("direct write failure did not increment sendDrops")
	}
}
