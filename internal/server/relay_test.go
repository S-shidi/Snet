package server

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"snet/internal/protocol"
)

// TestRelayPairing verifies two UDP clients talking to the same relay port
// receive each other's packets (the WireGuard relay scenario).
func TestRelayPairing(t *testing.T) {
	// grab a free port
	lc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := lc.LocalAddr().(*net.UDPAddr).Port
	lc.Close()

	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	a := dialUDP(t, port)
	b := dialUDP(t, port)
	defer a.Close()
	defer b.Close()

	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	// first round registers both endpoints (packets may be dropped while the
	// partner is unknown — WireGuard keepalives retry, so this is harmless)
	if _, err := a.WriteToUDP([]byte("from-a"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("from-b"), relay); err != nil {
		t.Fatal(err)
	}

	// second round must be cross-delivered
	if _, err := a.WriteToUDP([]byte("from-a2"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("from-b2"), relay); err != nil {
		t.Fatal(err)
	}

	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 256)
	n, _, err := b.ReadFromUDP(buf)
	if err != nil || string(buf[:n]) != "from-a2" {
		t.Fatalf("b got %q err=%v", string(buf[:n]), err)
	}
	// a receives round-1 "from-b" then round-2 "from-b2" (FIFO); assert the
	// final forwarded packet
	var gotA string
	for i := 0; i < 2; i++ {
		n, _, err = a.ReadFromUDP(buf)
		if err != nil {
			t.Fatal(err)
		}
		gotA = string(buf[:n])
	}
	if gotA != "from-b2" {
		t.Fatalf("a got %q", gotA)
	}
}

// TestRelayFanout verifies that a third member of a network receives packets
// relayed from either of the other two (a network with >2 members). The old
// pairwise relay could not deliver to a 3rd endpoint at all.
func TestRelayFanout(t *testing.T) {
	lc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := lc.LocalAddr().(*net.UDPAddr).Port
	lc.Close()

	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// b must be on a different host from a/c so per-host broadcast dedup
	// (decision 3C) still delivers to both b and c.
	a := dialUDPFrom(t, "127.0.0.1")
	b := dialUDPFrom(t, lanIPv4(t))
	c := dialUDPFrom(t, "127.0.0.1")
	defer a.Close()
	defer b.Close()
	defer c.Close()

	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	// Register all three endpoints (first packet identifies the sender).
	mustWrite := func(c *net.UDPConn, msg string) {
		if _, err := c.WriteToUDP([]byte(msg), relay); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(a, "reg-a")
	mustWrite(b, "reg-b")
	mustWrite(c, "reg-c")

	// Now a real data packet from a must reach both b and c.
	mustWrite(a, "fanout-abc")

	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 256)
	gotB := ""
	for {
		n, _, err := b.ReadFromUDP(buf)
		if err != nil {
			break
		}
		gotB = string(buf[:n])
		if gotB == "fanout-abc" {
			break
		}
	}
	if gotB != "fanout-abc" {
		t.Fatalf("b got %q, want fanout-abc", gotB)
	}
	gotC := ""
	for {
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			break
		}
		gotC = string(buf[:n])
		if gotC == "fanout-abc" {
			break
		}
	}
	if gotC != "fanout-abc" {
		t.Fatalf("c got %q, want fanout-abc", gotC)
	}
}

// TestRelayPairLegacyBroadcastDeduplicatesByHost verifies the legacy
// relayPair.handle fan-out (relay.go:350-367) also dedups by host. This path
// is still reachable for the network's shared broadcast port: RelayRouteNodePort
// returns false there (portToNode has no entry), so handle falls back to the
// legacy fan-out.
func TestRelayPairLegacyBroadcastDeduplicatesByHost(t *testing.T) {
	port := freePort(t)
	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	dial := func() *net.UDPConn {
		t.Helper()
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, b, c, sender := dial(), dial(), dial(), dial()
	defer a.Close()
	defer b.Close()
	defer c.Close()
	defer sender.Close()

	relayAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	for _, conn := range []*net.UDPConn{a, b, c, sender} {
		if _, err := conn.WriteToUDP([]byte("reg"), relayAddr); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(r.Flows(port)) != 4 {
		if time.Now().After(deadline) {
			t.Fatalf("registered flows = %v, want 4", r.Flows(port))
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Let registration fan-out settle, then drain it.
	time.Sleep(100 * time.Millisecond)
	buf := make([]byte, 64)
	drain := func(conn *net.UDPConn) {
		for {
			_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			if _, _, err := conn.ReadFromUDP(buf); err != nil {
				return
			}
		}
	}
	for _, conn := range []*net.UDPConn{a, b, c, sender} {
		drain(conn)
	}

	payload := []byte("legacy-fanout")
	if _, err := sender.WriteToUDP(payload, relayAddr); err != nil {
		t.Fatal(err)
	}
	received := 0
	for _, conn := range []*net.UDPConn{a, b, c} {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			if string(buf[:n]) == string(payload) {
				received++
				break
			}
		}
	}
	if received != 1 {
		t.Fatalf("legacy broadcast delivered %d copies to same-host endpoints, want 1; flows=%v", received, r.Flows(port))
	}
}

// TestRelaySendFannedDeduplicatesByHost verifies decision 3C: broadcast
// fan-out sends at most one datagram per host, even when that host has
// multiple live flows. Unicast SendFrom is untouched.
func TestRelaySendFannedDeduplicatesByHost(t *testing.T) {
	port := freePort(t)
	r := NewRelay(port, 1)
	// Suppress automatic fan-out of registration packets; SendFanned is
	// driven explicitly below.
	r.SetNodeRoute(func(port int, sender string, data []byte) bool { return true })
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	dial := func() *net.UDPConn {
		t.Helper()
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, b, c := dial(), dial(), dial()
	defer a.Close()
	defer b.Close()
	defer c.Close()

	relayAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	for _, conn := range []*net.UDPConn{a, b, c} {
		if _, err := conn.WriteToUDP([]byte("reg"), relayAddr); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(r.Flows(port)) != 3 {
		if time.Now().After(deadline) {
			t.Fatalf("registered flows = %v, want 3", r.Flows(port))
		}
		time.Sleep(10 * time.Millisecond)
	}

	sent := r.SendFanned(port, "192.0.2.1:1", []byte("fanout"))
	if sent != 1 {
		t.Fatalf("SendFanned wrote %d endpoints, want 1 after host dedup; flows=%v", sent, r.Flows(port))
	}
	received := 0
	buf := make([]byte, 64)
	for _, conn := range []*net.UDPConn{a, b, c} {
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if _, _, err := conn.ReadFromUDP(buf); err == nil {
			received++
		}
	}
	if received != 1 {
		t.Fatalf("same-host endpoints received %d copies, want 1; flows=%v", received, r.Flows(port))
	}
}

// TestRelayEnsureLazy verifies that a relay created without Start (the lazy
// production path) binds its advertised port only when Ensure is first called,
// and that Ensure is a cheap no-op on repeat calls.
func TestRelayEnsureLazy(t *testing.T) {
	// grab a free port
	lc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := lc.LocalAddr().(*net.UDPAddr).Port
	lc.Close()

	r := NewRelay(port, 1)
	// No Start(): the port must be free until Ensure is invoked.
	if err := assertPortFree(t, port); err != nil {
		t.Fatalf("relay bound eagerly: %v", err)
	}
	if err := r.Ensure(port); err != nil {
		t.Fatalf("Ensure(advertised) failed: %v", err)
	}
	if err := r.Ensure(port); err != nil {
		t.Fatalf("second Ensure failed (should be no-op): %v", err)
	}
	defer r.Close()

	a := dialUDP(t, port)
	b := dialUDP(t, port)
	defer a.Close()
	defer b.Close()

	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}
	// register both, then cross-deliver like TestRelayPairing
	if _, err := a.WriteToUDP([]byte("reg-a"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("reg-b"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteToUDP([]byte("from-a2"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("from-b2"), relay); err != nil {
		t.Fatal(err)
	}

	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 256)
	var gotB string
	for {
		n, _, err := b.ReadFromUDP(buf)
		if err != nil {
			break
		}
		gotB = string(buf[:n])
		if gotB == "from-a2" {
			break
		}
	}
	if gotB != "from-a2" {
		t.Fatalf("b got %q, want from-a2", gotB)
	}
}

// assertPortFree reports whether a UDP listener can bind the given port on
// 127.0.0.1 (i.e. the relay has not bound it eagerly).
func assertPortFree(t *testing.T, port int) error {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}

func dialUDP(t *testing.T, relayPort int) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func dialUDPFrom(t *testing.T, ip string) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestRelayWhoami verifies the relay answers a whoami control request with
// the observed source endpoint (the requester's NAT mapping).
func TestRelayWhoami(t *testing.T) {
	port := freePort(t)
	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	c := dialUDP(t, port)
	defer c.Close()
	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}

	req := append([]byte("\xfeSNET1"), []byte(`{"op":"whoami"}`)...)
	if _, err := c.WriteToUDP(req, relay); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1024)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read whoami reply: %v", err)
	}
	got := string(buf[:n])
	if !strings.HasPrefix(got, "\xfeSNET1") {
		t.Fatalf("reply missing control prefix: %q", got)
	}
	body := buf[len(protocol.RelayCtrlPrefix):n]
	var w struct {
		Op       string `json:"op"`
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		t.Fatalf("whoami reply not JSON: %s (%v)", got, err)
	}
	if w.Op != "whoami" {
		t.Fatalf("whoami op = %q", w.Op)
	}
	_, wantPort, _ := net.SplitHostPort(c.LocalAddr().(*net.UDPAddr).String())
	_, gotHost, _ := net.SplitHostPort(w.Endpoint) // unused; keep simple
	if _, gotPort, err := net.SplitHostPort(w.Endpoint); err != nil || gotPort != wantPort {
		t.Fatalf("whoami endpoint = %q, want port %s", w.Endpoint, wantPort)
	}
	_ = gotHost
}

// TestRelayGroup verifies a group request lists every other live endpoint but
// not the requester, and that control packets are never fanned out.
func TestRelayGroup(t *testing.T) {
	port := freePort(t)
	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	a := dialUDP(t, port)
	b := dialUDP(t, port)
	defer a.Close()
	defer b.Close()
	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}

	// register both endpoints
	if _, err := a.WriteToUDP([]byte("ping-a"), relay); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteToUDP([]byte("ping-b"), relay); err != nil {
		t.Fatal(err)
	}

	// b's registration causes the relay to fan "ping-b" back to a; drain it so
	// the group reply below is the first packet a reads after the request.
	_ = a.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	if _, _, err := a.ReadFromUDP(buf); err != nil {
		t.Fatalf("drain forwarded ping-b: %v", err)
	}

	req := append([]byte("\xfeSNET1"), []byte(`{"op":"group"}`)...)
	if _, err := a.WriteToUDP(req, relay); err != nil {
		t.Fatal(err)
	}
	n, _, err := a.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read group reply: %v", err)
	}
	got := string(buf[:n])
	if !strings.HasPrefix(got, "\xfeSNET1") {
		t.Fatalf("reply missing control prefix: %q", got)
	}
	var g struct {
		Op    string   `json:"op"`
		Peers []string `json:"peers"`
	}
	if err := json.Unmarshal(buf[len(protocol.RelayCtrlPrefix):n], &g); err != nil {
		t.Fatalf("group reply not JSON: %s (%v)", got, err)
	}
	if g.Op != "group" || len(g.Peers) != 1 {
		t.Fatalf("group reply = %+v", g)
	}
	_, wantPort, _ := net.SplitHostPort(b.LocalAddr().(*net.UDPAddr).String())
	if _, gotPort, err := net.SplitHostPort(g.Peers[0]); err != nil || gotPort != wantPort {
		t.Fatalf("group peers[0] = %q, want port %s", g.Peers[0], wantPort)
	}
	// b must not receive the control packet (it is not fanned out)
	_ = b.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := b.ReadFromUDP(buf); err == nil {
		t.Fatal("b received a control packet that should never be forwarded")
	}
}

// TestRelayGroupIncludesDataFlows verifies that a group request returns the
// registered WireGuard data flows (e.g. a phone's keepalive flow) even when
// the requester probed from a one-shot control socket, while excluding the
// requester's own socket and any ctrlOnly endpoints. This mirrors the client
// flow of a phone that registers its data flow then asks the relay which
// peers to connect to.
func TestRelayGroupIncludesDataFlows(t *testing.T) {
	port := freePort(t)
	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}

	// phoneWG is the phone's persistent WireGuard data flow (it sends WG
	// keepalives, so the relay sees it as a data endpoint, not ctrlOnly).
	phoneWG := dialUDP(t, port)
	defer phoneWG.Close()
	// peer is another node's data flow.
	peer := dialUDP(t, port)
	defer peer.Close()

	// Register both data flows with a WG keepalive (type-4, 32 bytes).
	keep := make([]byte, 32)
	keep[0] = 4
	if _, err := phoneWG.WriteToUDP(keep, relay); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.WriteToUDP(keep, relay); err != nil {
		t.Fatal(err)
	}

	// phoneCtrl is a one-shot control probe from the phone: it only ever
	// speaks the SNET1 control protocol, so the relay must mark it ctrlOnly
	// and never list it as a peer.
	phoneCtrl := dialUDP(t, port)
	defer phoneCtrl.Close()

	req := append([]byte("\xfeSNET1"), []byte(`{"op":"group"}`)...)
	if _, err := phoneCtrl.WriteToUDP(req, relay); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 4096)
	_ = phoneCtrl.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := phoneCtrl.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read group reply: %v", err)
	}
	var g struct {
		Op    string   `json:"op"`
		Peers []string `json:"peers"`
	}
	body := buf[len(protocol.RelayCtrlPrefix):n]
	if err := json.Unmarshal(body, &g); err != nil {
		t.Fatalf("group reply not JSON: %s (%v)", buf[:n], err)
	}
	if g.Op != "group" {
		t.Fatalf("group op = %q", g.Op)
	}

	want := map[string]bool{
		portOf(t, phoneWG): true,
		portOf(t, peer):    true,
	}
	if len(g.Peers) != len(want) {
		t.Fatalf("group peers = %v, want exactly %v", g.Peers, want)
	}
	for _, p := range g.Peers {
		if !want[portOfPeer(t, p)] {
			t.Fatalf("unexpected peer %q (want %v)", p, want)
		}
	}
	if _, ok := want[portOf(t, phoneCtrl)]; ok {
		t.Fatalf("ctrl socket leaked into group peers")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	lc, err := net.ListenUDP("udp", &net.UDPAddr{Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	p := lc.LocalAddr().(*net.UDPAddr).Port
	lc.Close()
	return p
}

// flowsContainHost reports whether any "ip:port" flow in flows has host as
// its host part.
func flowsContainHost(flows []string, host string) bool {
	for _, ep := range flows {
		h, _, err := net.SplitHostPort(ep)
		if err == nil && hostNorm(h) == hostNorm(host) {
			return true
		}
	}
	return false
}

// portOf returns the source port of a local UDP conn ("[::]:59247" -> 59247).
func portOf(t *testing.T, c *net.UDPConn) string {
	t.Helper()
	_, p, err := net.SplitHostPort(c.LocalAddr().String())
	if err != nil {
		t.Fatalf("local addr %v: %v", c.LocalAddr(), err)
	}
	return p
}

// portOfPeer returns the port part of a relay-listed peer endpoint string.
func portOfPeer(t *testing.T, ep string) string {
	t.Helper()
	_, p, err := net.SplitHostPort(ep)
	if err != nil {
		t.Fatalf("peer endpoint %q: %v", ep, err)
	}
	return p
}

// TestRelayDampensControlFrames verifies the fan-out amplification loop is
// broken: identical WireGuard control/keepalive frames re-entering the relay
// from any source are fanned out at most once per window, and real data
// frames are never dampened. This is the counterpart to the Phase 1 storm fix.
func TestRelayDampensControlFrames(t *testing.T) {
	port := freePort(t)
	r := NewRelay(port, 1)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	a := dialUDP(t, port)
	b := dialUDP(t, port)
	defer a.Close()
	defer b.Close()
	relay := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}

	// A 148-byte handshake-init-shaped frame, and a 32-byte keepalive-shaped
	// frame; both are exactly the "control" shapes the amplifier feeds on.
	init := append([]byte{1}, make([]byte, 147)...)
	keepalive := append([]byte{4}, make([]byte, 31)...)

	mustWrite := func(c *net.UDPConn, m []byte) {
		if _, err := c.WriteToUDP(m, relay); err != nil {
			t.Fatal(err)
		}
	}

	// Register both endpoints with data-length frames (never dampened, always
	// fanned), so b starts in a's seen set and vice versa.
	fake := []byte("x")
	mustWrite(a, fake)
	mustWrite(b, fake)

	readUntil := func(c *net.UDPConn, want []byte, deadline time.Duration) bool {
		_ = c.SetReadDeadline(time.Now().Add(deadline))
		buf := make([]byte, 512)
		for {
			n, _, err := c.ReadFromUDP(buf)
			if err != nil {
				return false
			}
			if string(buf[:n]) == string(want) {
				return true
			}
		}
	}
	// b got a's registration frame; drain it so b's next read is the init.
	_ = readUntil(b, fake, time.Second)

	// First output of a's init reaches b.
	mustWrite(a, init)
	if !readUntil(b, init, time.Second) {
		t.Fatal("b never saw the first init fan-out")
	}
	// Re-broadcasting the SAME frame from b's side (the amplifier case: b
	// roamed back to the relay and re-emits the identical keepalive) must NOT
	// produce another copy at a.
	mustWrite(b, keepalive)
	if readUntil(a, keepalive, time.Second) {
		// Note: a may legitimately receive b's own fresh keepalive via fan-out
		// exactly once; re-sending it immediately must not produce a second.
		mustWrite(b, keepalive)
		if readUntil(a, keepalive, time.Second) {
			t.Fatal("duplicate identical keepalive was fanned out again")
		}
	}

	// Real data frames still flow unimpeded.
	mustWrite(a, []byte("payload-1"))
	if !readUntil(b, []byte("payload-1"), time.Second) {
		t.Fatal("data frame was dampened")
	}
}

// TestRelayClassifyWG sanity-checks WireGuard control-frame recognition.
func TestRelayClassifyWG(t *testing.T) {
	if classifyWG(append([]byte{1}, make([]byte, 147)...)) != wgInit {
		t.Fatal("148B type-1 not init")
	}
	if classifyWG(append([]byte{2}, make([]byte, 91)...)) != wgResponse {
		t.Fatal("92B type-2 not response")
	}
	if classifyWG(append([]byte{3}, make([]byte, 63)...)) != wgCookie {
		t.Fatal("64B type-3 not cookie")
	}
	if classifyWG(append([]byte{4}, make([]byte, 31)...)) != wgKeepalive {
		t.Fatal("32B type-4 not keepalive")
	}
	if classifyWG(append([]byte{1}, make([]byte, 100)...)) != wgData {
		t.Fatal("odd-length type-1 should be data")
	}
}

// TestPerNodeUnicastRouting verifies the Phase 3 unicast relay path: each node
// is assigned its own relay port, and a data frame addressed to a peer's node
// port is forwarded from the SENDER's socket to the recipient's inbound mapping
// on it — with no broadcast fan-out to third parties. Legacy peers (no node
// port) never take this path (covered by the un-routed fan-out tests).
func TestPerNodeUnicastRouting(t *testing.T) {
	base := freePort(t)
	r := NewRelay(base, 8)
	defer r.Close()

	s := NewStore()
	s.SetRelay("127.0.0.1", base, 8)
	s.SetRelayEnsure(r.Ensure)
	s.SetNodeEnsure(r.EnsureNode)
	r.SetNodeRoute(s.RelayRouteNodePort)
	s.SetRelayAllFlows(r.Flows)
	s.SetRelaySend(r.SendFrom)

	created, err := s.CreateNetwork(testKey(900), "owner-device-0000", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	join := func(key int, device string) protocol.JoinResp {
		t.Helper()
		rj, err := s.Join(created.NetworkID, created.PairingCode, testKey(key), device)
		if err != nil {
			t.Fatal(err)
		}
		return rj
	}
	// Owner is caller A; B and C join with distinct devices.
	respA, err := s.ListPeersFrom(created.Token, "203.0.113.10", true)
	if err != nil {
		t.Fatal(err)
	}
	respB := join(901, "device-b-0001")
	respC := join(902, "device-c-0002")
	respBp, err := s.ListPeersFrom(respB.Token, "203.0.113.20", true)
	if err != nil {
		t.Fatal(err)
	}
	respCp, err := s.ListPeersFrom(respC.Token, "203.0.113.30", true)
	if err != nil {
		t.Fatal(err)
	}
	// The peers views advertise the same per-node ports back.
	peerPort := func(ps []protocol.Node, id string) int {
		t.Helper()
		for _, p := range ps {
			if p.ID == id {
				return p.RelayPort
			}
		}
		return 0
	}
	aPort := respA.Self.RelayPort
	bPort := respBp.Self.RelayPort
	cPort := respCp.Self.RelayPort
	if aPort == 0 || bPort == 0 || cPort == 0 {
		t.Fatalf("node ports not assigned: a=%d b=%d c=%d", aPort, bPort, cPort)
	}
	if aPort == bPort || aPort == cPort || bPort == cPort {
		t.Fatalf("node ports must be distinct: a=%d b=%d c=%d", aPort, bPort, cPort)
	}
	if got := peerPort(respCp.Peers, respBp.Self.ID); got != bPort {
		t.Fatalf("B advertised as C's peers' RelayPort=%d, want %d", got, bPort)
	}
	if got := peerPort(respCp.Peers, respA.Self.ID); got != aPort {
		t.Fatalf("A advertised as C's peers' RelayPort=%d, want %d", got, aPort)
	}
	// After everyone has polled, every peer's view carries each other's port.
	respAll, err := s.ListPeersFrom(respB.Token, "203.0.113.21", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := peerPort(respAll.Peers, respA.Self.ID); got != aPort {
		t.Fatalf("after polls, A advertised RelayPort=%d, want %d", got, aPort)
	}
	if got := peerPort(respAll.Peers, respCp.Self.ID); got != cPort {
		t.Fatalf("after polls, C advertised RelayPort=%d, want %d", got, cPort)
	}

	// Simulate the wire with three loopback endpoints bound to distinct source
	// IPs, and point the store's control-plane host attribution at them so the
	// unicast router can identify senders and recipients.
	s.NoteCtrlHost(created.NetworkID, respA.Self.ID, "127.0.0.1")
	s.NoteCtrlHost(created.NetworkID, respBp.Self.ID, "127.0.0.2")
	s.NoteCtrlHost(created.NetworkID, respCp.Self.ID, "127.0.0.3")

	dialFrom := func(ip string) *net.UDPConn {
		t.Helper()
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(ip)})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// Two distinct real source hosts: loopback for A, a LAN interface for B
	// (macOS only owns 127.0.0.1 on lo, so we can't bind 127.0.0.2/3).
	lanIP := lanIPv4(t)
	a := dialFrom("127.0.0.1")
	b := dialFrom(lanIP)
	defer a.Close()
	defer b.Close()
	s.NoteCtrlHost(created.NetworkID, respA.Self.ID, "127.0.0.1")
	s.NoteCtrlHost(created.NetworkID, respBp.Self.ID, lanIP)

	// B opens its inbound mapping on A's socket: B keepalives to A's node port
	// (A targets its own port, the address its socket is bound to on the relay).
	ka := []byte("x")
	if _, err := b.WriteToUDP(ka, &net.UDPAddr{IP: net.ParseIP(lanIP), Port: aPort}); err != nil {
		t.Fatal(err)
	}
	// The relay handles A's and B's sockets in separate goroutines, so wait
	// until B's mapping is actually registered before A's payload can be
	// routed through it.
	deadline := time.Now().Add(2 * time.Second)
	for !flowsContainHost(r.Flows(aPort), lanIP) {
		if time.Now().After(deadline) {
			t.Fatalf("B's flow never registered on A's socket :%d", aPort)
		}
		time.Sleep(20 * time.Millisecond)
	}
	buf := make([]byte, 512)

	// A sends a data frame addressed to B's node port (relayHost:RelayPortB).
	// It must be routed unicast out of A's socket into B's mapping — fan-out
	// on B's port would go nowhere because B's flow is on A's port, not B's.
	payload := []byte("data-from-A")
	if _, err := a.WriteToUDP(payload, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: bPort}); err != nil {
		t.Fatal(err)
	}

	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, src, err := b.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("B never received the unicast frame: %v", err)
	}
	if string(buf[:n]) != string(payload) {
		t.Fatalf("B got %q, want %q", string(buf[:n]), payload)
	}
	// The frame must have left the relay from the SENDER's socket (A's node
	// port), proving it crossed the NAT via the sender's own mapping.
	if _, sp, err := net.SplitHostPort(src.String()); err != nil || sp != strconv.Itoa(aPort) {
		t.Fatalf("B received from %v, want relay src port %d", src, aPort)
	}
}

// testNetwork3 returns a store with three nodes A/B/C each owning a distinct
// node port, with ctrlHost attribution set like the live deployment:
// A = mobile (control 223.160.209.21), B = Mac (112.10.250.51),
// C = docker on the relay host (127.0.0.1). The recorder tap and per-port flow
// listings are wired to the store's fake relay sink.
type testNetwork3 struct {
	store   *Store
	rec     *relayRecorder
	flows   map[int][]string
	a, b, c protocol.Node
	aPort   int
	bPort   int
	cPort   int
	aTok    string
	bTok    string
	cTok    string
}

func buildNetwork3(t *testing.T) *testNetwork3 {
	t.Helper()
	base := freePort(t)
	r := NewRelay(base, 8)
	t.Cleanup(func() { r.Close() })

	s := NewStore()
	s.SetRelay("127.0.0.1", base, 8)
	s.SetRelayEnsure(r.Ensure)
	s.SetNodeEnsure(r.EnsureNode)
	r.SetNodeRoute(s.RelayRouteNodePort)

	net3 := &testNetwork3{store: s, flows: map[int][]string{}}
	net3.rec = &relayRecorder{}
	s.SetRelaySend(func(port int, to string, data []byte) bool {
		net3.rec.sends = append(net3.rec.sends, sendRec{port, to, string(data)})
		return true
	})
	s.SetRelayAllFlows(func(port int) []string {
		return net3.flows[port]
	})

	created, err := s.CreateNetwork(testKey(950), "owner-device-9500", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	join := func(key int, device string) protocol.JoinResp {
		t.Helper()
		rj, err := s.Join(created.NetworkID, created.PairingCode, testKey(key), device)
		if err != nil {
			t.Fatal(err)
		}
		return rj
	}
	la, err := s.ListPeersFrom(created.Token, "223.160.209.21", true)
	if err != nil {
		t.Fatal(err)
	}
	net3.a = *la.Self
	net3.aTok = created.Token
	rb := join(951, "device-b-9501")
	rc := join(952, "device-c-9502")
	lb, err := s.ListPeersFrom(rb.Token, "112.10.250.51", true)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := s.ListPeersFrom(rc.Token, "66.187.6.46", true)
	if err != nil {
		t.Fatal(err)
	}
	net3.b = *lb.Self
	net3.c = *lc.Self
	net3.bTok = rb.Token
	net3.cTok = rc.Token
	net3.aPort, net3.bPort, net3.cPort = net3.a.RelayPort, net3.b.RelayPort, net3.c.RelayPort
	if net3.aPort == 0 || net3.bPort == 0 || net3.cPort == 0 ||
		net3.aPort == net3.bPort || net3.aPort == net3.cPort || net3.bPort == net3.cPort {
		t.Fatalf("distinct node ports required: a=%d b=%d c=%d", net3.aPort, net3.bPort, net3.cPort)
	}
	return net3
}

type sendRec struct {
	port int
	to   string
	data string
}
type relayRecorder struct {
	sends []sendRec
}

func (rr *relayRecorder) contains(to, data string) bool {
	for _, s := range rr.sends {
		if s.to == to && s.data == data {
			return true
		}
	}
	return false
}

// TestNeighborDataPlaneAttribution verifies that a data-plane flow whose host
// differs from the node's control-plane host is still attributed by /23-/16
// operator-pool adjacency: the phone (control 223.160.209.21) reaches the Mac
// through the same /23 as its control egress (223.160.208.21).
func TestNeighborDataPlaneAttribution(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// The phone keepalives toward Mac's node port from its data-plane NAT host,
	// which matches no stored control/IP address exactly.
	s.OnRelayFlow(net3.bPort, "223.160.208.21:39802")

	ns := s.networks[net3.a.NetworkID]
	if ns == nil {
		t.Fatal("no network state")
	}
	if got := s.exclusiveHostOwnerLocked(ns, "223.160.208.21"); got != net3.a.ID {
		t.Fatalf("data-plane host 223.160.208.21 owner=%q, want A=%q", got, net3.a.ID)
	}
}

// TestOnRelayFlowRefreshesEachFlow verifies the per-flow throttle: three
// nodes sharing one network must all refresh their authoritative relay flow
// even when their packets arrive within the same second. The old
// network-wide gate let only the first caller update relayFlowByNode, leaving
// the other peers' direct-punch hints stale and mis-attributing relay traffic.
func TestOnRelayFlowRefreshesEachFlow(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// OnRelayFlow's port is the recipient's node port, not the sender's.
	// Use B as the recipient for all three senders so A can be resolved by
	// operator-pool adjacency (/23) against B's peers. b and c match their
	// control-plane hosts exactly.
	s.OnRelayFlow(net3.bPort, "112.10.250.51:12831")
	s.OnRelayFlow(net3.bPort, "66.187.6.46:51900")
	s.OnRelayFlow(net3.bPort, "223.160.208.21:39802")

	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[net3.a.NetworkID]
	if ns == nil {
		t.Fatal("no network state")
	}
	for _, tc := range []struct {
		name string
		id   string
		want string
	}{
		{"a", net3.a.ID, "223.160.208.21:39802"},
		{"b", net3.b.ID, "112.10.250.51:12831"},
		{"c", net3.c.ID, "66.187.6.46:51900"},
	} {
		if got := ns.relayFlowByNode[tc.id]; got != tc.want {
			t.Fatalf("relayFlowByNode[%s]=%q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestRelayRouteSharedHostIgnoresPinnedSiblingFlow verifies the live
// NAS/phone failure: both siblings' NAT mappings were pinned to NAS, so phone
// frames were routed from NAS's node port and never reached sdserver. A pinned
// flow under a shared egress must be ignored and the frame fanned out from
// every sibling's node port instead.
func TestRelayRouteSharedHostIgnoresPinnedSiblingFlow(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
		s.learnNodeHostLocked(ns, id, "39.180.139.85")
	}
	const sender = "39.180.139.85:7777"
	ns.flowByNode[sender] = net3.b.ID
	ns.relayFlowByNode[net3.b.ID] = sender
	s.mu.Unlock()

	net3.flows[net3.bPort] = []string{"223.160.209.21:30001"}
	net3.flows[net3.cPort] = []string{"223.160.209.21:30002"}
	payload := []byte("shared-sender")
	s.RelayRouteNodePort(net3.aPort, sender, payload)

	for _, want := range []string{"223.160.209.21:30001", "223.160.209.21:30002"} {
		if !net3.rec.contains(want, string(payload)) {
			t.Fatalf("shared sender not fanned out to %s; sends=%v", want, net3.rec.sends)
		}
	}
}

// TestOnRelayFlowSharedHostDoesNotPinSiblingFlow verifies that a shared
// egress can no longer poison the exact-flow indexes. A mapping previously
// pinned to one sibling is cleared, and no new pin is created for the host.
func TestOnRelayFlowSharedHostDoesNotPinSiblingFlow(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
		s.learnNodeHostLocked(ns, id, "39.180.139.85")
	}
	const addr = "39.180.139.85:7777"
	ns.flowByNode[addr] = net3.b.ID
	ns.relayFlowByNode[net3.b.ID] = addr
	s.mu.Unlock()

	s.OnRelayFlow(net3.aPort, addr)

	s.mu.Lock()
	defer s.mu.Unlock()
	ns = s.networks[net3.a.NetworkID]
	if _, ok := ns.flowByNode[addr]; ok {
		t.Fatal("shared-host flow was pinned to a sibling")
	}
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		if got := ns.relayFlowByNode[id]; got != "" {
			t.Fatalf("shared-host relayFlowByNode[%s]=%q, want empty", id, got)
		}
	}
}

// TestOnRelayFlowRejectsAmbiguousSharedCtrlHost verifies that two siblings
// sharing one control-plane egress are not guessed by map iteration order. A
// wrong pick used to overwrite a sibling's authoritative relay flow, sending
// that peer's frames to the wrong NAT mapping.
func TestOnRelayFlowRejectsAmbiguousSharedCtrlHost(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()

	const addr = "39.180.139.85:7777"
	// A is the recipient; both of its peers claim the sender's host, so the
	// flow is genuinely ambiguous and must not be attributed to either.
	s.OnRelayFlow(net3.aPort, addr)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		if got := ns.relayFlowByNode[id]; got != "" {
			t.Fatalf("ambiguous flow attributed to %s: %q", id, got)
		}
	}
	if _, ok := ns.flowByNode[addr]; ok {
		t.Fatal("ambiguous flow recorded in flowByNode")
	}
}

// TestRelayRouteFansOutAmbiguousSharedSender verifies that a frame from a
// shared egress with no exact flow is delivered from every sibling's node
// port. The recipient accepts the copy matching its NAT mapping and drops the
// rest; guessing one sibling used to send only the wrong port and lose the
// frame (or spray it at an unrelated node).
func TestRelayRouteFansOutAmbiguousSharedSender(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()
	net3.flows[net3.bPort] = []string{"223.160.209.21:30001"}
	net3.flows[net3.cPort] = []string{"223.160.209.21:30002"}

	payload := []byte("ambiguous-sender")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload)
	for _, want := range []struct {
		port int
		to   string
	}{
		{net3.bPort, "223.160.209.21:30001"},
		{net3.cPort, "223.160.209.21:30002"},
	} {
		if !net3.rec.contains(want.to, string(payload)) {
			t.Fatalf("ambiguous sender not fanned out from port %d to %s; sends=%v", want.port, want.to, net3.rec.sends)
		}
	}
}

// TestRelayRouteFansOutFromEverySiblingNodePort verifies that a shared-host
// sender with no exact flow is delivered from each sibling's own node port,
// not merely to each sibling's flow. The sendRec port field is asserted here;
// TestRelayRouteFansOutAmbiguousSharedSender only checks the target flow.
func TestRelayRouteFansOutFromEverySiblingNodePort(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()
	net3.flows[net3.bPort] = []string{"223.160.209.21:30001"}
	net3.flows[net3.cPort] = []string{"223.160.209.21:30002"}

	payload := []byte("ambiguous-sender-port-check")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload)

	want := []sendRec{
		{port: net3.bPort, to: "223.160.209.21:30001", data: string(payload)},
		{port: net3.cPort, to: "223.160.209.21:30002", data: string(payload)},
	}
	for _, w := range want {
		found := false
		for _, got := range net3.rec.sends {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fanout did not send from node port %d to %s; sends=%v", w.port, w.to, net3.rec.sends)
		}
	}
}

// TestRelayRouteSharedHostDoesNotFanOutToUnrelatedSibling checks the negative
// side of T12 ("no wrong sibling continuously receives") and the review's
// §4.3 warning that fan-out "copies traffic and pollutes siblings".
//
// Scenario 1: B/C share the sender's egress and have live recipient flows;
// an unrelated sibling E has its own live flow but must receive zero copies,
// and the broadcast fallback must not run.
//
// Scenario 2: the recipient has no live flow, so the router takes the
// broadcast fallback. Relay.SendFanned sends to every live endpoint on the
// relay socket except the sender, so E would receive a copy. That is current
// behaviour, recorded here; narrowing the fallback is a separate decision.
func TestRelayRouteSharedHostDoesNotFanOutToUnrelatedSibling(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	info, err := s.NetworkInfo(net3.aTok)
	if err != nil {
		t.Fatal(err)
	}
	rjE, err := s.Join(info.ID, info.PairingCode, testKey(953), "device-e-9503")
	if err != nil {
		t.Fatal(err)
	}
	le, err := s.ListPeersFrom(rjE.Token, "203.0.113.99", true)
	if err != nil {
		t.Fatal(err)
	}
	e := *le.Self
	ePort := e.RelayPort
	if ePort == 0 || ePort == net3.aPort || ePort == net3.bPort || ePort == net3.cPort {
		t.Fatalf("unrelated sibling got a bad node port: %d (a=%d b=%d c=%d)", ePort, net3.aPort, net3.bPort, net3.cPort)
	}

	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()

	const (
		bFlow  = "223.160.209.21:31001"
		cFlow  = "223.160.209.21:31002"
		eFlow1 = "203.0.113.99:31003"
		eFlow2 = "203.0.113.99:31004"
		eFlow3 = "203.0.113.99:31005"
	)
	eFlows := []string{eFlow1, eFlow2, eFlow3}
	net3.flows[net3.bPort] = []string{bFlow}
	net3.flows[net3.cPort] = []string{cFlow}
	net3.flows[ePort] = append([]string(nil), eFlows...)

	fanned := 0
	eCopies := 0
	s.SetRelaySendFanned(func(port int, except string, data []byte) int {
		fanned++
		// Relay.SendFanned dedups by host (decision 3C): all E flows share
		// one host, so only one endpoint receives a copy.
		seenHosts := map[string]bool{}
		for _, to := range eFlows {
			if to == except {
				continue
			}
			host, _, err := net.SplitHostPort(to)
			if err != nil || seenHosts[host] {
				continue
			}
			seenHosts[host] = true
			net3.rec.sends = append(net3.rec.sends, sendRec{port: port, to: to, data: string(data)})
			eCopies++
		}
		return len(seenHosts)
	})

	// Scenario 1: B/C flows hit, so no broadcast fallback and E gets nothing.
	payload1 := []byte("scenario-1")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload1)
	if !net3.rec.contains(bFlow, string(payload1)) || !net3.rec.contains(cFlow, string(payload1)) {
		t.Fatalf("scenario 1 did not fan out to both siblings; sends=%v", net3.rec.sends)
	}
	for _, eFlow := range eFlows {
		if net3.rec.contains(eFlow, string(payload1)) {
			t.Fatalf("scenario 1 sent a copy to unrelated sibling %s; sends=%v", eFlow, net3.rec.sends)
		}
	}
	if fanned != 0 {
		t.Fatalf("scenario 1 triggered broadcast fallback %d times; sends=%v", fanned, net3.rec.sends)
	}

	// Scenario 2: no recipient flow -> broadcast fallback. Relay.SendFanned
	// now dedups by host (decision 3C), so E's three same-host flows receive
	// one copy.
	net3.rec.sends = nil
	net3.flows[net3.bPort] = nil
	net3.flows[net3.cPort] = nil
	fanned = 0
	eCopies = 0
	payload2 := []byte("scenario-2")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload2)
	if fanned == 0 {
		t.Fatalf("scenario 2 did not trigger broadcast fallback; sends=%v", net3.rec.sends)
	}
	if eCopies != 1 {
		t.Fatalf("broadcast fallback sent %d copies to unrelated sibling, want 1 after host dedup; sends=%v", eCopies, net3.rec.sends)
	}
	t.Logf("scenario 2: broadcast fallback sent %d copy to unrelated sibling %v (host-deduped, decision 3C)", eCopies, eFlows)
}

// TestRelayRouteDeduplicatesSameRecipientFlowAcrossSiblings checks T12's
// "at most one valid copy" across sibling node ports. sentTo is created
// inside each deliver(sp) call (store.go:1519), so it dedups duplicates within
// one port's flow list but not the same flow observed on two sibling ports.
func TestRelayRouteDeduplicatesSameRecipientFlowAcrossSiblings(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()

	const rf = "223.160.209.21:32001"
	countRF := func() int {
		n := 0
		for _, sr := range net3.rec.sends {
			if sr.to == rf {
				n++
			}
		}
		return n
	}

	// Same recipient flow observed on both sibling ports: the shared sentTo
	// map dedups across sibling candidates, so one copy is expected.
	net3.flows[net3.bPort] = []string{rf}
	net3.flows[net3.cPort] = []string{rf}
	payload := []byte("cross-sibling-duplicate")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload)
	if got := countRF(); got != 1 {
		t.Fatalf("same recipient flow across sibling ports sent %d times, want 1 after shared sentTo; sends=%v", got, net3.rec.sends)
	}
	t.Logf("same recipient flow %s appeared on both sibling ports and was sent 1 time (shared sentTo dedup, decision 2B)", rf)

	// Within one deliver(sp), duplicate entries in the same port's flow list
	// are deduped by sentTo.
	net3.rec.sends = nil
	net3.flows[net3.bPort] = []string{rf, rf}
	net3.flows[net3.cPort] = nil
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload)
	if got := countRF(); got != 1 {
		t.Fatalf("duplicate entries in one sibling port's flow list sent %d times, want 1; sends=%v", got, net3.rec.sends)
	}
}

// TestRelayRouteSharesSentToAcrossSiblings verifies decision 2B: when the same
// recipient flow appears on two sibling node ports, the shared sentTo map
// sends it only once.
func TestRelayRouteSharesSentToAcrossSiblings(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()

	const rf = "223.160.209.21:32001"
	net3.flows[net3.bPort] = []string{rf}
	net3.flows[net3.cPort] = []string{rf}
	payload := []byte("shared-sentto")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload)

	copies := 0
	for _, sr := range net3.rec.sends {
		if sr.to == rf {
			copies++
		}
	}
	if copies != 1 {
		t.Fatalf("same recipient flow across sibling ports sent %d times, want 1 after shared sentTo; sends=%v", copies, net3.rec.sends)
	}
}

// TestRelayRouteSharedSentToRetriesNextSiblingOnFailure verifies the 2B
// follow-up: a failed send must not mark the flow as sent in the shared map,
// so the next sibling can retry it.
func TestRelayRouteSharedSentToRetriesNextSiblingOnFailure(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()

	const rf = "223.160.209.21:32001"
	net3.flows[net3.bPort] = []string{rf}
	net3.flows[net3.cPort] = []string{rf}

	attempts := 0
	successes := 0
	s.SetRelaySend(func(port int, to string, data []byte) bool {
		if to != rf {
			return true
		}
		attempts++
		// Candidate map iteration order is not stable; fail the first
		// attempt and succeed the second, which is the retry semantics under
		// test.
		if attempts == 1 {
			return false
		}
		successes++
		return true
	})

	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", []byte("retry-next-sibling"))

	if attempts != 2 {
		t.Fatalf("same flow attempted %d times, want 2 (B fail, C retry)", attempts)
	}
	if successes != 1 {
		t.Fatalf("same flow succeeded %d times, want 1", successes)
	}
}

// TestRelayRouteDropsWhenAllSiblingSendsFail verifies decision B: when a
// recipient had candidate flows but every sibling send fails, the router must
// drop and count instead of broadcasting to unrelated endpoints.
func TestRelayRouteDropsWhenAllSiblingSendsFail(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()

	const rf = "223.160.209.21:34001"
	net3.flows[net3.bPort] = []string{rf}
	net3.flows[net3.cPort] = []string{rf}

	attempts := 0
	s.SetRelaySend(func(port int, to string, data []byte) bool {
		if to == rf {
			attempts++
			return false
		}
		return true
	})
	fanned := 0
	s.SetRelaySendFanned(func(port int, except string, data []byte) int {
		fanned++
		return 1
	})
	before := s.RelayFanoutFallbacks()

	ok := s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", []byte("all-fail"))

	if ok {
		t.Fatalf("RelayRouteNodePort returned true after all sibling sends failed, want false")
	}
	if fanned != 0 {
		t.Fatalf("broadcast fallback ran %d times after all sibling sends failed, want 0", fanned)
	}
	if got := s.RelayFanoutFallbacks(); got != before+1 {
		t.Fatalf("fanout fallback counter = %d, want %d (drop+count)", got, before+1)
	}
	if attempts != 2 {
		t.Fatalf("sibling sends attempted %d times, want 2", attempts)
	}
}

// TestRelayRouteSuppressesBroadcastAfterHintDeletion verifies the TTL patch:
// after a failed exact-flow send deletes the hint, the next trigger within
// relayFlowBadTTL must drop+count instead of broadcasting. Expiry restores
// the bootstrap broadcast.
func TestRelayRouteSuppressesBroadcastAfterHintDeletion(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	const dead = "223.160.208.21:39802"
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.relayFlowByNode[net3.a.ID] = dead
	if ns.flowByNode == nil {
		ns.flowByNode = make(map[string]string)
	}
	ns.flowByNode[dead] = net3.a.ID
	s.mu.Unlock()
	net3.flows[net3.cPort] = []string{dead}

	s.SetRelaySend(func(port int, to string, data []byte) bool {
		return to != dead
	})
	fanned := 0
	s.SetRelaySendFanned(func(port int, except string, data []byte) int {
		fanned++
		return 1
	})
	before := s.RelayFanoutFallbacks()
	payload := []byte("ttl-after-hint-delete")

	ok := s.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)
	if ok {
		t.Fatalf("first trigger returned true, want false after failed exact flow")
	}
	if fanned != 0 {
		t.Fatalf("first trigger broadcast %d times, want 0", fanned)
	}
	if got := s.RelayFanoutFallbacks(); got != before+1 {
		t.Fatalf("counter after first trigger = %d, want %d", got, before+1)
	}

	// Second trigger: hint is gone, TTL suppresses broadcast.
	ok = s.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)
	if ok {
		t.Fatalf("second trigger returned true, want false during TTL")
	}
	if fanned != 0 {
		t.Fatalf("second trigger broadcast %d times within TTL, want 0", fanned)
	}
	if got := s.RelayFanoutFallbacks(); got != before+2 {
		t.Fatalf("counter after second trigger = %d, want %d", got, before+2)
	}

	until, hasTTL := relayFlowBadUntilForTest(t, ns, net3.a.ID)
	if !hasTTL || !time.Now().Before(until) {
		t.Fatalf("relayFlowBadUntil[%s] = %v (has=%v), want future TTL", net3.a.ID, until, hasTTL)
	}

	// Fast-forward TTL by expiring the entry directly; tests are in-package.
	expireRelayFlowBadForTest(t, ns, net3.a.ID)
	ok = s.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)
	if !ok {
		t.Fatalf("third trigger returned false after TTL expiry, want true (broadcast)")
	}
	if fanned != 1 {
		t.Fatalf("third trigger broadcast %d times after TTL expiry, want 1", fanned)
	}
	if got := s.RelayFanoutFallbacks(); got != before+3 {
		t.Fatalf("counter after TTL expiry = %d, want %d", got, before+3)
	}
}

func relayFlowBadUntilForTest(t *testing.T, ns *networkState, id string) (time.Time, bool) {
	t.Helper()
	v := reflect.ValueOf(ns).Elem().FieldByName("relayFlowBadUntil")
	if !v.IsValid() {
		t.Fatalf("relayFlowBadUntil field missing")
	}
	m, ok := v.Interface().(map[string]time.Time)
	if !ok {
		t.Fatalf("relayFlowBadUntil type = %s, want map[string]time.Time", v.Type())
	}
	until, ok := m[id]
	return until, ok
}

func expireRelayFlowBadForTest(t *testing.T, ns *networkState, id string) {
	t.Helper()
	v := reflect.ValueOf(ns).Elem().FieldByName("relayFlowBadUntil")
	if !v.IsValid() {
		t.Fatalf("relayFlowBadUntil field missing")
	}
	m, ok := v.Interface().(map[string]time.Time)
	if !ok {
		t.Fatalf("relayFlowBadUntil type = %s, want map[string]time.Time", v.Type())
	}
	m[id] = time.Now().Add(-time.Second)
}

// TestRelayRouteNarrowsSharedFanoutAndCountsFallback verifies decision 1B+1C:
// the shared-sender candidate loop only targets siblings sharing the sender's
// host (never an unrelated E), while a recipient with no live flow still gets
// the broadcast fallback, now counted.
func TestRelayRouteNarrowsSharedFanoutAndCountsFallback(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	info, err := s.NetworkInfo(net3.aTok)
	if err != nil {
		t.Fatal(err)
	}
	rjE, err := s.Join(info.ID, info.PairingCode, testKey(954), "device-e-9504")
	if err != nil {
		t.Fatal(err)
	}
	le, err := s.ListPeersFrom(rjE.Token, "203.0.113.99", true)
	if err != nil {
		t.Fatal(err)
	}
	e := *le.Self
	ePort := e.RelayPort
	if ePort == 0 || ePort == net3.aPort || ePort == net3.bPort || ePort == net3.cPort {
		t.Fatalf("unrelated sibling got a bad node port: %d", ePort)
	}

	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()

	const (
		bFlow = "223.160.209.21:33001"
		cFlow = "223.160.209.21:33002"
		eFlow = "203.0.113.99:33003"
	)
	net3.flows[net3.bPort] = []string{bFlow}
	net3.flows[net3.cPort] = []string{cFlow}
	net3.flows[ePort] = []string{eFlow}

	fanned := 0
	s.SetRelaySendFanned(func(port int, except string, data []byte) int {
		fanned++
		return 1
	})
	before := s.RelayFanoutFallbacks()

	// Scenario 1: B/C have live flows. E is on a different host and must not
	// receive a copy; the candidate loop never includes it.
	payload1 := []byte("narrow-1")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload1)
	if net3.rec.contains(eFlow, string(payload1)) {
		t.Fatalf("unrelated sibling %s received a copy; sends=%v", eFlow, net3.rec.sends)
	}
	if fanned != 0 {
		t.Fatalf("broadcast fallback ran %d times with B/C flows present", fanned)
	}
	if got := s.RelayFanoutFallbacks(); got != before {
		t.Fatalf("fanout fallback counter = %d, want unchanged %d", got, before)
	}

	// Scenario 2: recipient has no flow. The broadcast fallback must still run
	// and the counter must increment.
	net3.rec.sends = nil
	net3.flows[net3.bPort] = nil
	net3.flows[net3.cPort] = nil
	fanned = 0
	payload2 := []byte("narrow-2")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload2)
	if fanned == 0 {
		t.Fatalf("broadcast fallback did not run when recipient had no flow")
	}
	if got := s.RelayFanoutFallbacks(); got != before+1 {
		t.Fatalf("fanout fallback counter = %d, want %d", got, before+1)
	}
}

// TestRelayRouteDropsFailedHostCandidateContinuesToNextSibling verifies the host-level
// failure path: an exclusive-host send that fails removes that host from
// recipientHosts, so a later flow under the same host is not attempted. The
// outer candidate loop still falls through to the other sibling. "Fallback"
// here means the next sibling candidate, not broadcast fan-out: current code
// marks sentTo before the send and therefore does not call the broadcast
// fallback for a failed send.
func TestRelayRouteDropsFailedHostCandidateContinuesToNextSibling(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.ctrlHost[net3.b.ID] = "39.180.139.85"
	ns.ctrlHost[net3.c.ID] = "39.180.139.85"
	for _, id := range []string{net3.b.ID, net3.c.ID} {
		ns.nodes[id].Endpoint = ""
		ns.nodes[id].EndpointV6 = ""
	}
	s.mu.Unlock()

	const (
		bf  = "223.160.209.21:30001"
		bf2 = "223.160.209.21:30002" // same host as bf; must be skipped after bf fails
		cf  = "223.160.209.21:30003"
	)
	net3.flows[net3.bPort] = []string{bf, bf2}
	net3.flows[net3.cPort] = []string{cf}

	failed := map[string]bool{}
	fanned := 0
	s.SetRelaySendFanned(func(port int, except string, data []byte) int {
		fanned++
		return 0
	})
	s.SetRelaySend(func(port int, to string, data []byte) bool {
		ok := !(port == net3.bPort && to == bf)
		net3.rec.sends = append(net3.rec.sends, sendRec{port: port, to: to, data: string(data)})
		failed[to] = !ok
		return ok
	})

	payload := []byte("failed-candidate")
	s.RelayRouteNodePort(net3.aPort, "39.180.139.85:7777", payload)

	count := func(to string) int {
		n := 0
		for _, sr := range net3.rec.sends {
			if sr.to == to {
				n++
			}
		}
		return n
	}
	if got := count(bf); got != 1 {
		t.Fatalf("failed candidate %s attempted %d times, want 1; sends=%v", bf, got, net3.rec.sends)
	}
	if !failed[bf] {
		t.Fatalf("candidate %s was not marked failed; sends=%v", bf, net3.rec.sends)
	}
	if got := count(bf2); got != 0 {
		t.Fatalf("same-host candidate %s attempted %d times after %s failed, want 0; sends=%v", bf2, got, bf, net3.rec.sends)
	}
	if got := count(cf); got != 1 {
		t.Fatalf("second sibling flow %s attempted %d times, want 1; sends=%v", cf, got, net3.rec.sends)
	}
	if failed[cf] {
		t.Fatalf("second sibling flow %s unexpectedly failed; sends=%v", cf, net3.rec.sends)
	}
	if fanned != 0 {
		t.Fatalf("broadcast fallback called %d times after a failed send, want 0 (current behaviour); sends=%v", fanned, net3.rec.sends)
	}
}

// TestRelayRouteExactFlowSuccessKeepsCandidate verifies the complement of
// decision A: a successful exact-flow send must keep relayFlowByNode and
// flowByNode intact.
func TestRelayRouteExactFlowSuccessKeepsCandidate(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	const exact = "223.160.208.21:39802"
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.relayFlowByNode[net3.a.ID] = exact
	if ns.flowByNode == nil {
		ns.flowByNode = make(map[string]string)
	}
	ns.flowByNode[exact] = net3.a.ID
	s.mu.Unlock()
	net3.flows[net3.cPort] = []string{exact}

	attempted := false
	s.SetRelaySend(func(port int, to string, data []byte) bool {
		net3.rec.sends = append(net3.rec.sends, sendRec{port: port, to: to, data: string(data)})
		if to == exact {
			attempted = true
		}
		return true
	})

	payload := []byte("exact-flow-success")
	s.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)

	if !attempted {
		t.Fatalf("exact flow %s was never attempted; sends=%v", exact, net3.rec.sends)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := ns.relayFlowByNode[net3.a.ID]; got != exact {
		t.Fatalf("successful exact flow was removed from relayFlowByNode: got %q, want %q", got, exact)
	}
	if got := ns.flowByNode[exact]; got != net3.a.ID {
		t.Fatalf("successful exact flow was removed from flowByNode: got %q, want %q", got, net3.a.ID)
	}
}

// TestRelayRouteExactFlowFailureDeletesHintAndReattributes verifies decision A:
// a failed exact-flow send removes relayFlowByNode (and the recipient-owned
// flowByNode entry) so the next packet does not retry the dead mapping and can
// fall back to broadcast/shared-host routing.
func TestRelayRouteExactFlowFailureDeletesHintAndReattributes(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	const dead = "223.160.208.21:39802"
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.relayFlowByNode[net3.a.ID] = dead
	if ns.flowByNode == nil {
		ns.flowByNode = make(map[string]string)
	}
	ns.flowByNode[dead] = net3.a.ID
	s.mu.Unlock()
	net3.flows[net3.cPort] = []string{dead}

	attempts := 0
	fanned := 0
	s.SetRelaySend(func(port int, to string, data []byte) bool {
		net3.rec.sends = append(net3.rec.sends, sendRec{port: port, to: to, data: string(data)})
		if to == dead {
			attempts++
			return false
		}
		return true
	})
	s.SetRelaySendFanned(func(port int, except string, data []byte) int {
		fanned++
		return 0
	})

	payload := []byte("dead-exact-flow")
	s.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)

	s.mu.Lock()
	if _, ok := ns.relayFlowByNode[net3.a.ID]; ok {
		s.mu.Unlock()
		t.Fatalf("relayFlowByNode[%s] still present after failed exact-flow send", net3.a.ID)
	}
	if _, ok := ns.flowByNode[dead]; ok {
		s.mu.Unlock()
		t.Fatalf("flowByNode[%s] still present after failed exact-flow send", dead)
	}
	s.mu.Unlock()

	if attempts != 1 {
		t.Fatalf("dead exact flow attempted %d times on first trigger, want 1; sends=%v", attempts, net3.rec.sends)
	}
	if fanned != 0 {
		t.Fatalf("broadcast fallback called %d times on first trigger, want 0 (drop+count on total failure); sends=%v", fanned, net3.rec.sends)
	}

	// Second trigger: the dead flow must not be retried; with no remaining
	// exact flow/host match, the router falls back to broadcast.
	s.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)
	if attempts != 1 {
		t.Fatalf("dead exact flow attempted again on second trigger: attempts=%d; sends=%v", attempts, net3.rec.sends)
	}
	if fanned != 1 {
		t.Fatalf("broadcast fallback calls after second trigger = %d, want 1; sends=%v", fanned, net3.rec.sends)
	}
}

// TestRelayRouteExactFlowFailureConcurrentSafe exercises the exact-flow hint
// deletion from many goroutines. RelayRouteNodePort holds s.mu for the whole
// call, so the delete and the OnRelayFlow/prune writers are serialized; run
// with -race -count=100 to catch map races.
func TestRelayRouteExactFlowFailureConcurrentSafe(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	const dead = "223.160.208.21:39802"
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.relayFlowByNode[net3.a.ID] = dead
	if ns.flowByNode == nil {
		ns.flowByNode = make(map[string]string)
	}
	ns.flowByNode[dead] = net3.a.ID
	s.mu.Unlock()
	net3.flows[net3.cPort] = []string{dead}

	var attempts int64
	var fanned int64
	s.SetRelaySend(func(port int, to string, data []byte) bool {
		if to == dead {
			atomic.AddInt64(&attempts, 1)
			return false
		}
		return true
	})
	s.SetRelaySendFanned(func(port int, except string, data []byte) int {
		atomic.AddInt64(&fanned, 1)
		return 0
	})

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			s.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", []byte("concurrent-dead-flow"))
		}()
	}
	wg.Wait()

	// At most one call may attempt the dead flow: RelayRouteNodePort
	// serializes on s.mu, and the first failed attempt deletes the hint.
	// The cleared-map assertions below prove the failure path actually ran,
	// so this does not need to rely on an exact count.
	if got := atomic.LoadInt64(&attempts); got > 1 {
		t.Fatalf("dead exact flow attempted %d times across concurrent calls, want at most 1", got)
	}
	if got := atomic.LoadInt64(&fanned); got == 0 {
		t.Fatalf("expected broadcast fallback after the dead flow was dropped; fanned=%d", got)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := ns.relayFlowByNode[net3.a.ID]; ok {
		t.Fatal("relayFlowByNode still present after concurrent failed exact-flow sends")
	}
	if _, ok := ns.flowByNode[dead]; ok {
		t.Fatal("flowByNode still present after concurrent failed exact-flow sends")
	}
}

// TestPruneStaleRelayFlowsClearsHint verifies that an idle NAT mapping is
// removed from every relay index, including the authoritative per-node hint
// peers use for direct punching.
func TestPruneStaleRelayFlowsClearsHint(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store
	const addr = "112.10.250.51:12831"

	s.OnRelayFlow(net3.bPort, addr)

	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.networks[net3.a.NetworkID]
	ns.relaySeen[addr] = time.Now().Add(-2 * time.Minute)
	s.pruneStaleRelayFlowsLocked(ns, time.Now())

	if _, ok := ns.relaySeen[addr]; ok {
		t.Fatal("stale relaySeen entry not pruned")
	}
	if _, ok := ns.flowByNode[addr]; ok {
		t.Fatal("stale flowByNode entry not pruned")
	}
	if _, ok := ns.flowWrite[addr]; ok {
		t.Fatal("stale flowWrite entry not pruned")
	}
	if got := ns.relayFlowByNode[net3.b.ID]; got != "" {
		t.Fatalf("stale authoritative flow hint kept: %q", got)
	}
}

// TestSharedHostNeverDropsRecipient verifies that a reverse-index collision on
// one host (112.10.250.51 -> {Mac, phone}) can no longer swallow a phone-bound
// frame. The phone's authoritative exact flow is always delivered; the shared
// host is a fan-out candidate so the recipient's real mapping is reached even
// when the flow hook pinned a sibling's socket (extra copies are dropped by
// the sibling's WG session).
func TestSharedHostNeverDropsRecipient(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// Poison the reverse index to model the live bug: 112.10.250.51 is both
	// Mac (truth) and phone (stale).
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	s.learnNodeHostLocked(ns, net3.a.ID, "112.10.250.51")
	s.mu.Unlock()

	// Docker (C, the sender) has two inbound mappings on its node port: the
	// phone's data-plane flow 223.160.208.21:39802 (same /23 as the phone's
	// control host, attributed on the wire) and Mac's flow under the shared
	// host 112.10.250.51:12831.
	s.OnRelayFlow(net3.cPort, "223.160.208.21:39802")
	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	if got := s.exclusiveHostOwnerLocked(ns, "223.160.208.21"); got != net3.a.ID {
		s.mu.Unlock()
		t.Fatalf("222.160.208.21 data-plane attribution owner=%q, want phone=%q", got, net3.a.ID)
	}
	s.mu.Unlock()
	net3.flows[net3.cPort] = []string{"112.10.250.51:12831", "223.160.208.21:39802"}

	payload := []byte("for-phone")

	// Docker sends to the phone's node port: the phone's own exact flow must
	// always receive the payload — the old code returned early with no sends.
	net3.store.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)

	if !net3.rec.contains("223.160.208.21:39802", string(payload)) {
		t.Fatalf("phone-bound frame never reached phone flow; sends=%v", net3.rec.sends)
	}
}

// TestSharedEgressCoLearnOnPeerAttribution models the live NAS bug: Mac and NAS
// sit behind one home egress (39.180.139.85). Mac claims the host first and
// becomes its exclusive owner; the refuse-to-steal guard in listPeersFrom then
// refused NAS's control-plane claim, leaving NAS with no ctrlHost, no
// nodeByHost membership and no relay flow — every frame to NAS's node port was
// silently dropped at the empty-recipient gate before the decision log. When
// the claiming node's own self-advertised endpoint host matches the control
// plane source (NAS egresses from 39.180.139.85 just like Mac), the host must
// be co-learned as a shared egress instead of refused, and a frame to NAS must
// fan out to NAS's socket under the shared host.
func TestSharedEgressCoLearnOnPeerAttribution(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// NAS (C) self-advertises the home egress as its direct endpoint; Mac (B)
	// already exclusively owns that host via its control-plane calls.
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.nodes[net3.c.ID].Endpoint = "39.180.139.85:51820"
	s.mu.Unlock()

	// B (Mac) claims the home host first; from that point on it is exclusive.
	lb, err := s.ListPeersFrom(net3.bTok, "39.180.139.85", true)
	if err != nil {
		t.Fatal(err)
	}
	if lb.Self == nil {
		t.Fatal("no self for B")
	}

	// NAS calls the control plane from the same home egress: previously the
	// exclusive-owner guard refused and NAS stayed invisible. Now the
	// self-advertised endpoint must verify the address and co-learn NAS as a
	// shared owner.
	if _, err := s.ListPeersFrom(net3.cTok, "39.180.139.85", true); err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	if !ns.nodeByHost["39.180.139.85"][net3.b.ID] {
		s.mu.Unlock()
		t.Fatalf("home host no longer owned by Mac after NAS co-learn; nodeByHost=%v", ns.nodeByHost)
	}
	if !ns.nodeByHost["39.180.139.85"][net3.c.ID] {
		s.mu.Unlock()
		t.Fatalf("home host not co-learned to NAS; nodeByHost=%v", ns.nodeByHost)
	}
	if got := ns.ctrlHost[net3.c.ID]; got != "39.180.139.85" {
		s.mu.Unlock()
		t.Fatalf("NAS ctrlHost = %q, want 39.180.139.85", got)
	}
	if got := s.exclusiveHostOwnerLocked(ns, "39.180.139.85"); got != "" {
		s.mu.Unlock()
		t.Fatalf("home host still has exclusive owner %q after co-learn, want shared", got)
	}
	s.mu.Unlock()

	// Phone (A) sends to NAS's node port before NAS's exact flow is pinned.
	// Both siblings' sockets are live under the shared egress on the phone's
	// port — the shared-host fan-out must reach NAS's socket.
	net3.flows[net3.aPort] = []string{"39.180.139.85:4302", "39.180.139.85:1213"}

	payload := []byte("phone-to-nas")
	net3.store.RelayRouteNodePort(net3.cPort, "223.160.208.21:39802", payload)

	if !net3.rec.contains("39.180.139.85:1213", string(payload)) {
		t.Fatalf("shared-host fan-out never reached NAS flow; sends=%v", net3.rec.sends)
	}
}

// TestSharedHostRecipientBootstrapFanOut models the live bug: Mac and NAS sit
// behind one home egress (39.180.139.85:4302 and :1213) and a phone-bound
// frame is sent before Mac's exact flow is pinned. The shared host must not
// drop the frame (old behaviour) — every live flow under the shared egress is
// fanned out so the recipient's own WG socket accepts the copy and a wrong
// sibling drops its copy harmlessly.
func TestSharedHostRecipientBootstrapFanOut(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// The shared host 39.180.139.85 is owned by more than one node (Mac, phone)
	// — a genuine shared egress, not an exclusive host.
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	s.learnNodeHostLocked(ns, net3.b.ID, "39.180.139.85")
	s.learnNodeHostLocked(ns, net3.a.ID, "39.180.139.85")
	s.mu.Unlock()

	// Docker (C, sender) socket shows both siblings' flows under the shared
	// egress; recipient Mac's exact flow has no pin yet.
	net3.flows[net3.cPort] = []string{"39.180.139.85:4302", "39.180.139.85:1213"}

	payload := []byte("phone-to-mac")
	net3.store.RelayRouteNodePort(net3.bPort, "66.187.6.46:51900", payload)

	if !net3.rec.contains("39.180.139.85:4302", string(payload)) {
		t.Fatalf("shared-host fan-out never reached Mac's flow; sends=%v", net3.rec.sends)
	}
}

// TestSharedHostRecipientPinnedFlow verifies the deterministic path once the
// recipient's exact mapping is pinned: a frame bound for Mac is delivered to
// 39.180.139.85:4302, and because the flow hook can coin-flip which sibling a
// shared-egress address belongs to, a copy may also land on the sibling's
// socket (39.180.139.85:1213) where WG drops it harmlessly — the recipient's
// own mapping must never be skipped.
func TestSharedHostRecipientPinnedFlow(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// The shared host is owned by Mac and the phone, and Mac's exact flow is
	// pinned by the flow hook (full-address reverse index).
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	s.learnNodeHostLocked(ns, net3.b.ID, "39.180.139.85")
	s.learnNodeHostLocked(ns, net3.a.ID, "39.180.139.85")
	ns.relayFlowByNode[net3.b.ID] = "39.180.139.85:4302"
	if ns.flowByNode == nil {
		ns.flowByNode = make(map[string]string)
	}
	ns.flowByNode["39.180.139.85:4302"] = net3.b.ID
	s.mu.Unlock()

	// Docker (C, sender) socket shows both siblings under the shared egress.
	net3.flows[net3.cPort] = []string{"39.180.139.85:4302", "39.180.139.85:1213"}

	payload := []byte("phone-to-mac-pinned")
	net3.store.RelayRouteNodePort(net3.bPort, "66.187.6.46:51900", payload)

	if !net3.rec.contains("39.180.139.85:4302", string(payload)) {
		t.Fatalf("pinned flow 4302 never got the frame; sends=%v", net3.rec.sends)
	}
}

// TestSharedHostSenderExactFlowAttribution verifies the sender side of the
// same collision: a frame arriving from a shared-egress exact flow is resolved
// by the full-address reverse index even though the host alone maps to both
// siblings.
func TestSharedHostSenderExactFlowAttribution(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// Shared host 39.180.139.85 belongs to both Mac (B) and the phone (A).
	// Mac's exact mapping is pinned via the flow hook.
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	s.learnNodeHostLocked(ns, net3.b.ID, "39.180.139.85")
	s.learnNodeHostLocked(ns, net3.a.ID, "39.180.139.85")
	ns.relayFlowByNode[net3.b.ID] = "39.180.139.85:4302"
	if ns.flowByNode == nil {
		ns.flowByNode = make(map[string]string)
	}
	ns.flowByNode["39.180.139.85:4302"] = net3.b.ID
	s.mu.Unlock()

	// Docker (C, recipient) socket holds an inbound flow toward Mac.
	net3.flows[net3.bPort] = []string{"66.187.6.46:51900"}

	// Mac sends toward docker's node port from its shared-egress exact flow:
	// attribution must land on Mac by exact flow, not on the host-only
	// ambiguous sibling (which would be the wrong sender socket).
	payload := []byte("mac-to-docker-exact")
	net3.store.RelayRouteNodePort(net3.cPort, "39.180.139.85:4302", payload)

	if !net3.rec.contains("66.187.6.46:51900", string(payload)) {
		t.Fatalf("docker never received mac frame via exact-flow sender attribution; sends=%v", net3.rec.sends)
	}
}

// TestFlowFallbackActiveNode verifies the period before the recipient opens a
// flow on the sender's socket: a frame is still delivered via the recipient's
// authoritative flow (docker on the relay host accepting any server source).
func TestFlowFallbackActiveNode(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// Mac (B) sends a data frame to docker (C) before docker has opened any
	// flow on Mac's socket. Docker's live flow (66.187.6.46:51900) must still
	// receive it.
	s.OnRelayFlow(net3.cPort, "66.187.6.46:51900")
	payload := []byte("mac-to-docker")
	net3.store.RelayRouteNodePort(net3.cPort, "112.10.250.51:12831", payload)

	if !net3.rec.contains("66.187.6.46:51900", string(payload)) {
		t.Fatalf("docker never got the frame via authoritative flow; sends=%v", net3.rec.sends)
	}
}

// TestMultiEgressFanOut verifies the CGNAT multi-egress case: a subscriber
// holds two live public mappings (the operator rotates egress per flow), so
// the docker node's socket shows two phone flows under distinct hosts
// (223.160.208.29, 223.160.209.29). A docker->phone frame must be delivered to
// BOTH flows, not just the first match — sending one mapping only loses every
// alternate egress (observed as ~50% loss).
func TestMultiEgressFanOut(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	// The phone is seen using both egress hosts (each exclusively its own).
	s.learnNodeHostLocked(ns, net3.a.ID, "223.160.208.29")
	s.learnNodeHostLocked(ns, net3.a.ID, "223.160.209.29")
	s.mu.Unlock()

	// Docker (C, sender) has two inbound mappings on its node port: one per
	// phone egress. Both must receive the data frame.
	net3.flows[net3.cPort] = []string{"223.160.208.29:39801", "223.160.209.29:39802"}

	payload := []byte("docker-to-phone")
	net3.store.RelayRouteNodePort(net3.aPort, "66.187.6.46:51900", payload)

	if !net3.rec.contains("223.160.208.29:39801", string(payload)) {
		t.Fatalf("payload not delivered to first phone egress; sends=%v", net3.rec.sends)
	}
	if !net3.rec.contains("223.160.209.29:39802", string(payload)) {
		t.Fatalf("payload not delivered to second phone egress; sends=%v", net3.rec.sends)
	}
}

// TestStaleDataPlaneHostPruned verifies that a data-plane host which goes
// idle past the TTL is removed from the reverse index: a CGNAT pool the
// operator recycled for another subscriber must not keep steering frames
// toward the old owner's flows. A still-live hold (existing relayFlowByNode
// mapping or ctrlHost) keeps the host alive.
func TestStaleDataPlaneHostPruned(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork(testKey(960), "device-960", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := s.Join(created.NetworkID, created.PairingCode, testKey(961), "device-c-961")
	if err != nil {
		t.Fatal(err)
	}
	ns := s.networks[created.NetworkID]
	if ns == nil {
		t.Fatal("no network state")
	}

	now := time.Now()
	s.mu.Lock()
	// Phone learns a data-plane host (e.g. CGNAT egress 223.160.208.29) that
	// later goes idle.
	s.learnNodeHostLocked(ns, rc.NodeID, "223.160.208.29")
	ns.hostSeen["223.160.208.29"] = now.Add(-10 * time.Minute)
	if _, ok := ns.nodeByHost["223.160.208.29"]; !ok {
		s.mu.Unlock()
		t.Fatal("host not in reverse index after learn")
	}
	old := len(ns.hostSeen)
	s.pruneStaleHostsLocked(ns, now)
	if _, ok := ns.nodeByHost["223.160.208.29"]; ok {
		s.mu.Unlock()
		t.Fatalf("stale host still in reverse index after prune")
	}
	if len(ns.hostSeen) >= old {
		s.mu.Unlock()
		t.Fatalf("hostSeen not pruned (was %d, now %d)", old, len(ns.hostSeen))
	}

	// A fresh ctrlHost reference must keep the host alive even when old.
	s.learnNodeHostLocked(ns, rc.NodeID, "223.160.209.21")
	ns.hostSeen["223.160.209.21"] = now.Add(-30 * time.Second)
	ns.ctrlHost[rc.NodeID] = "223.160.209.21"
	ns.hostSeen["223.160.209.21"] = now.Add(-10 * time.Minute)
	before := len(ns.nodeByHost)
	s.pruneStaleHostsLocked(ns, now)
	if _, ok := ns.nodeByHost["223.160.209.21"]; !ok {
		s.mu.Unlock()
		t.Fatalf("live ctrlHost pruned from reverse index; nodeByHost=%v", ns.nodeByHost)
	}
	if len(ns.nodeByHost) != before {
		s.mu.Unlock()
		t.Fatalf("prune removed a live entry")
	}
	s.mu.Unlock()
}

// TestAttribBySelfAdvertisedEndpoint verifies the NAS regression: a node whose
// control plane rides a v2ray proxy on the relay host (ctrlHost=66.187.6.46)
// but whose data plane egresses from its real public address, which it
// self-advertises as its endpoint (39.180.139.10:51820). A data frame from
// that data-plane host matches neither the control-plane host nor any learned
// reverse-index host, so it must be attributed from node.Endpoint or it is
// silently dropped (observed NAS<->everyone loss).
func TestAttribBySelfAdvertisedEndpoint(t *testing.T) {
	net3 := buildNetwork3(t)
	s := net3.store

	// NAS (C): control plane is the relay-host proxy, data plane is its real
	// public address.
	s.mu.Lock()
	ns := s.networks[net3.a.NetworkID]
	ns.nodes[net3.c.ID].Endpoint = "39.180.139.10:51820"
	s.mu.Unlock()

	// Mac (B) keeps its inbound mapping on NAS's socket.
	s.OnRelayFlow(net3.cPort, "112.10.250.51:12831")
	net3.flows[net3.cPort] = []string{"112.10.250.51:12831"}

	payload := []byte("nas-to-mac")

	// NAS sends a data frame to Mac's node port from its direct public
	// address; ctrlHost and reverse-index matches both miss.
	s.RelayRouteNodePort(net3.bPort, "39.180.139.10:4390", payload)

	if !net3.rec.contains("112.10.250.51:12831", string(payload)) {
		t.Fatalf("NAS data-plane frame not delivered to Mac; sends=%v", net3.rec.sends)
	}

	// The self-advertised endpoint must seed the reverse index.
	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	if !ns.nodeByHost["39.180.139.10"][net3.c.ID] {
		s.mu.Unlock()
		t.Fatalf("self endpoint host 39.180.139.10 not learned to NAS; nodeByHost=%v", ns.nodeByHost)
	}
	s.mu.Unlock()

	s.OnRelayFlow(net3.bPort, "39.180.139.10:4390")
	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	if got := ns.relayFlowByNode[net3.c.ID]; got != "39.180.139.10:4390" {
		s.mu.Unlock()
		t.Fatalf("relay flow attribution for NAS = %q, want 39.180.139.10:4390", got)
	}
	s.mu.Unlock()

	// Two nodes advertising the same *fresh* self endpoint is ambiguous and
	// must not attribute (prevents a reused/stolen address hijacking a flow).
	// The host must be one the reverse index has not already learned.
	s.mu.Lock()
	ns = s.networks[net3.a.NetworkID]
	ns.nodes[net3.c.ID].Endpoint = "139.180.139.10:51820"
	ns.nodes[net3.b.ID].Endpoint = "139.180.139.10:51820"
	s.mu.Unlock()
	s.RelayRouteNodePort(net3.bPort, "139.180.139.10:4391", []byte("ambiguous"))
	if net3.rec.contains("112.10.250.51:12831", "ambiguous") {
		t.Fatalf("ambiguous self endpoint attributed and delivered; sends=%v", net3.rec.sends)
	}
}

// lanIPv4 returns a routable non-loopback IPv4 of this host, or skips the test
// when none exists (e.g. an offline CI runner).
func lanIPv4(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("net.Interfaces: %v", err)
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				return v4.String()
			}
		}
	}
	t.Skip("no non-loopback IPv4 interface")
	return ""
}

// TestMultiRelayEndpointsAdvertised verifies that when alternate relay hosts
// are configured, ListPeersFrom advertises every candidate on the network's
// relay port (primary first, deduplicated by host).
func TestMultiRelayEndpointsAdvertised(t *testing.T) {
	s := NewStore()
	base := freePort(t)
	s.SetRelay("relay-a.example", base, 8)
	s.SetRelayAlternates([]string{"relay-b.example", "relay-c.example", "relay-a.example"})
	created, err := s.CreateNetwork(testKey(960), "dev-relay-multi", "", "", false)
	if err != nil {
		t.Fatal(err)
	}

	la, err := s.ListPeersFrom(created.Token, "223.160.209.21", true)
	if err != nil {
		t.Fatal(err)
	}
	if la.RelayEndpoint == "" {
		t.Fatal("primary RelayEndpoint empty")
	}
	if len(la.RelayEndpoints) != 3 {
		t.Fatalf("want 3 candidates, got %v", la.RelayEndpoints)
	}
	if la.RelayEndpoints[0] != la.RelayEndpoint {
		t.Fatalf("primary not first: %v vs %q", la.RelayEndpoints, la.RelayEndpoint)
	}
	if la.RelayEndpoints[1] != "relay-b.example:"+fmt.Sprint(created.RelayPort) {
		t.Fatalf("alternate B wrong: %v (port %d)", la.RelayEndpoints, created.RelayPort)
	}
	if la.RelayEndpoints[2] != "relay-c.example:"+fmt.Sprint(created.RelayPort) {
		t.Fatalf("alternate C wrong: %v", la.RelayEndpoints)
	}
	// All candidates share the network's relay port.
	for _, ep := range la.RelayEndpoints {
		_, port, err := net.SplitHostPort(ep)
		if err != nil {
			t.Fatalf("bad candidate %q: %v", ep, err)
		}
		if port != fmt.Sprint(created.RelayPort) {
			t.Fatalf("candidate %q uses wrong port (want %d)", ep, created.RelayPort)
		}
	}

	// Single-relay mode must keep the field unset for backward compatibility.
	s2 := NewStore()
	base2 := freePort(t)
	s2.SetRelay("relay-a.example", base2, 8)
	created2, err := s2.CreateNetwork(testKey(961), "dev-relay-single", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	la2, err := s2.ListPeersFrom(created2.Token, "223.160.209.21", true)
	if err != nil {
		t.Fatal(err)
	}
	if la2.RelayEndpoint == "" {
		t.Fatal("single relay endpoint empty")
	}
	if len(la2.RelayEndpoints) != 0 {
		t.Fatalf("single-relay mode should not set RelayEndpoints, got %v", la2.RelayEndpoints)
	}
}

// TestRelayTopoPersistedAcrossRestart verifies that the per-node relay
// topology (assigned unicast port and serving instance host) survives a store
// restart: the same port is re-advertised and the RelayHostOverride is
// preserved, so peers' cached endpoints stay valid and cross-instance routing
// keeps working.
func TestRelayTopoPersistedAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topo.db")

	newStore := func() *Store {
		t.Helper()
		s, err := NewStoreAt(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		base := freePort(t)
		// Single-instance store: no alternates, so every node collapses onto
		// the primary host and RelayHostOverride stays empty on the wire.
		s.SetRelay("127.0.0.1", base, 16)
		return s
	}

	s := newStore()
	created, err := s.CreateNetwork(testKey(970), "dev-topo-persist", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	join := func(key int, device string) string {
		t.Helper()
		rj, err := s.Join(created.NetworkID, created.PairingCode, testKey(key), device)
		if err != nil {
			t.Fatal(err)
		}
		return rj.Token
	}
	tokA := created.Token
	tokB := join(971, "dev-topo-b")

	// Both callers poll with per-node ports enabled so ports get allocated.
	la, err := s.ListPeersFrom(tokA, "203.0.113.41", true)
	if err != nil {
		t.Fatal(err)
	}
	lb, err := s.ListPeersFrom(tokB, "203.0.113.42", true)
	if err != nil {
		t.Fatal(err)
	}
	aPort := la.Self.RelayPort
	bPort := lb.Self.RelayPort
	if aPort == 0 || bPort == 0 {
		t.Fatalf("node ports not assigned: a=%d b=%d", aPort, bPort)
	}
	if aPort == bPort {
		t.Fatalf("node ports must differ: a=%d b=%d", aPort, bPort)
	}
	hostOf := func(ps []protocol.Node, id string) string {
		t.Helper()
		for _, p := range ps {
			if p.ID == id {
				return p.RelayHostOverride
			}
		}
		return ""
	}
	// Single instance: no override anywhere.
	if got := hostOf(la.Peers, lb.Self.ID); got != "" {
		t.Fatalf("single-instance B RelayHostOverride=%q, want empty", got)
	}
	if la.Self.RelayHostOverride != "" {
		t.Fatalf("single-instance self RelayHostOverride=%q, want empty", la.Self.RelayHostOverride)
	}

	// Restart the store against the same DB file.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := newStore()

	// The persisted topology must restore the exact same ports and no drift.
	la2, err := s2.ListPeersFrom(tokA, "203.0.113.41", true)
	if err != nil {
		t.Fatal(err)
	}
	lb2, err := s2.ListPeersFrom(tokB, "203.0.113.42", true)
	if err != nil {
		t.Fatal(err)
	}
	if la2.Self.RelayPort != aPort {
		t.Fatalf("A port after restart=%d, want persisted %d", la2.Self.RelayPort, aPort)
	}
	if lb2.Self.RelayPort != bPort {
		t.Fatalf("B port after restart=%d, want persisted %d", lb2.Self.RelayPort, bPort)
	}
	if got := hostOf(la2.Peers, lb2.Self.ID); got != "" {
		t.Fatalf("after restart B RelayHostOverride=%q, want empty", got)
	}
}

// TestRelayTopoMultiInstanceAssignment verifies that when alternates are
// configured, each node is assigned a fixed serving instance by stable hash
// and peers receive that instance's host as RelayHostOverride: repeated calls
// pick the same instance, and it is never the primary for primary-assigned
// nodes.
func TestRelayTopoMultiInstanceAssignment(t *testing.T) {
	s := NewStore()
	base := freePort(t)
	s.SetRelay("10.0.0.1", base, 32)
	s.SetRelayAlternates([]string{"10.0.0.2", "10.0.0.3"})
	created, err := s.CreateNetwork(testKey(972), "dev-topo-multi", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	join := func(key int, device string) string {
		t.Helper()
		rj, err := s.Join(created.NetworkID, created.PairingCode, testKey(key), device)
		if err != nil {
			t.Fatal(err)
		}
		return rj.Token
	}
	tokA := created.Token
	tokB := join(973, "dev-topo-multi-b")
	tokC := join(974, "dev-topo-multi-c")

	assign := func(tok, host string) *protocol.Node {
		t.Helper()
		resp, err := s.ListPeersFrom(tok, host, true)
		if err != nil {
			t.Fatal(err)
		}
		return resp.Self
	}
	a1 := assign(tokA, "203.0.113.51")
	b1 := assign(tokB, "203.0.113.52")
	c1 := assign(tokC, "203.0.113.53")

	// Hosts are normalized downwards; primary host collapses to "".
	norm := func(h string) string {
		if h == "" {
			return "10.0.0.1"
		}
		return h
	}
	seen := map[string]bool{norm(a1.RelayHostOverride): true, norm(b1.RelayHostOverride): true, norm(c1.RelayHostOverride): true}
	// Hash assignment is deterministic but a few nodes can legitimately
	// collide; sample more identities so this assertion only fails when
	// assignment is actually broken. Pairing codes are use-limited, so reset
	// the code between small batches.
	for round := 0; round < 2; round++ {
		code, err := s.ResetCode(tokA)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			key := 975 + round*3 + i
			rj, err := s.Join(created.NetworkID, code, testKey(key), fmt.Sprintf("dev-topo-multi-extra-%d", key))
			if err != nil {
				t.Fatal(err)
			}
			n := assign(rj.Token, fmt.Sprintf("203.0.113.%d", 100+key-975))
			seen[norm(n.RelayHostOverride)] = true
		}
	}
	if len(seen) == 1 {
		t.Fatalf("expected nodes spread across instances, all on %v", seen)
	}

	// Stable: repeated polls return the same instance and the same port.
	a2 := assign(tokA, "203.0.113.51")
	if a2.RelayHostOverride != a1.RelayHostOverride {
		t.Fatalf("A instance changed across polls: %q -> %q", a1.RelayHostOverride, a2.RelayHostOverride)
	}
	if a2.RelayPort != a1.RelayPort {
		t.Fatalf("A port changed across polls: %d -> %d", a1.RelayPort, a2.RelayPort)
	}

	// Peers advertise the serving instance for each other.
	la, err := s.ListPeersFrom(tokA, "203.0.113.51", true)
	if err != nil {
		t.Fatal(err)
	}
	hostOf := func(ps []protocol.Node, id string) string {
		t.Helper()
		for _, p := range ps {
			if p.ID == id {
				return p.RelayHostOverride
			}
		}
		return ""
	}
	if got := hostOf(la.Peers, b1.ID); got != b1.RelayHostOverride {
		t.Fatalf("peer B override=%q, want %q", got, b1.RelayHostOverride)
	}
	if got := hostOf(la.Peers, c1.ID); got != c1.RelayHostOverride {
		t.Fatalf("peer C override=%q, want %q", got, c1.RelayHostOverride)
	}
}

// TestRelayTopoHostStaleAfterAlternatesRemoved verifies that persisted
// RelayHostOverride hosts written while alternates were configured do not
// survive a restart where the deployment collapsed back to a single
// instance: the port is kept (cached peer endpoints stay valid) but the
// stale serving host is cleared so peers fall back to the primary relay.
func TestRelayTopoHostStaleAfterAlternatesRemoved(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topo-stale.db")

	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	base := freePort(t)
	s.SetRelay("10.0.0.1", base, 32)
	created, err := s.CreateNetwork(testKey(973), "dev-topo-stale", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	tokB, err := s.Join(created.NetworkID, created.PairingCode, testKey(974), "dev-topo-stale-b")
	if err != nil {
		t.Fatal(err)
	}
	// Assign B a port through the normal single-instance path, then simulate
	// stale persisted topology from a previous multi-instance deployment by
	// hand-writing an alternate host override for the same node.
	lb, err := s.ListPeersFrom(tokB.Token, "203.0.113.62", true)
	if err != nil {
		t.Fatal(err)
	}
	bPort := lb.Self.RelayPort
	if bPort == 0 {
		t.Fatal("B port not assigned")
	}
	if err := s.persistRelayTopo(created.NetworkID, lb.Self.ID, bPort, "10.0.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart as single instance: no alternates.
	s2, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s2.SetRelay("10.0.0.1", base, 32)
	lb2, err := s2.ListPeersFrom(tokB.Token, "203.0.113.62", true)
	if err != nil {
		t.Fatal(err)
	}
	if lb2.Self.RelayPort != bPort {
		t.Fatalf("B port after collapse=%d, want kept %d", lb2.Self.RelayPort, bPort)
	}
	if lb2.Self.RelayHostOverride != "" {
		t.Fatalf("B stale host after collapse=%q, want empty", lb2.Self.RelayHostOverride)
	}
}
