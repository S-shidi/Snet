package relay

import (
	"encoding/hex"
	"net"
	"testing"
	"time"

	"snet/internal/protocol"
)

type relayTestClient struct {
	t      *testing.T
	conn   *net.UDPConn
	key    uint64
	sid    [16]byte
	ticket string
}

func newRelayTestClient(t *testing.T, srv *Server, signer *TicketSigner, networkID, nodeID string) *relayTestClient {
	t.Helper()
	claims, err := NewClaims(networkID, nodeID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := signer.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	var sid [16]byte
	for i := 0; i < 16; i++ {
		sid[i] = byte(i + 1 + len(nodeID))
	}
	// Use the ticket's actual session ID: the relay rejects a mismatch.
	raw, err := hex.DecodeString(claims.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	copy(sid[:], raw)
	conn, err := net.DialUDP("udp", nil, mustResolve(t, srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &relayTestClient{t: t, conn: conn, key: protocol.RelayNodeKey(nodeID), sid: sid, ticket: ticket}
	c.send(protocol.RelayFrame{
		Type:      protocol.RelayTypeHello,
		SessionID: sid,
		SenderID:  c.key,
		Payload:   []byte(ticket),
	})
	ack := c.recv()
	if ack.Type != protocol.RelayTypeHelloAck {
		t.Fatalf("hello ack type = %d", ack.Type)
	}
	return c
}

func (c *relayTestClient) send(f protocol.RelayFrame) {
	c.t.Helper()
	b, err := f.MarshalBinary()
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *relayTestClient) sendFrom(conn *net.UDPConn, addr *net.UDPAddr, f protocol.RelayFrame) {
	c.t.Helper()
	b, err := f.MarshalBinary()
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := conn.WriteToUDP(b, addr); err != nil {
		c.t.Fatal(err)
	}
}

func (c *relayTestClient) recv() protocol.RelayFrame {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65535)
	n, err := c.conn.Read(buf)
	if err != nil {
		c.t.Fatalf("recv: %v", err)
	}
	f, err := protocol.ParseRelayFrame(buf[:n])
	if err != nil {
		c.t.Fatalf("parse: %v", err)
	}
	return f
}

func TestServerForwardsAuthenticatedData(t *testing.T) {
	signer, err := NewTicketSigner(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := Listen("127.0.0.1:0", signer, Options{SessionTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve()

	a := newRelayTestClient(t, srv, signer, "net-1", "node-a")
	b := newRelayTestClient(t, srv, signer, "net-1", "node-b")

	payload := []byte("ciphertext")
	a.send(protocol.RelayFrame{
		Type:        protocol.RelayTypeData,
		SessionID:   a.sid,
		SenderID:    a.key,
		RecipientID: b.key,
		Payload:     payload,
	})
	got := b.recv()
	if got.Type != protocol.RelayTypeData || got.SenderID != a.key || string(got.Payload) != string(payload) {
		t.Fatalf("forwarded frame = %+v", got)
	}

	a.send(protocol.RelayFrame{Type: protocol.RelayTypePing, SessionID: a.sid, SenderID: a.key})
	if pong := a.recv(); pong.Type != protocol.RelayTypePong {
		t.Fatalf("pong type = %d", pong.Type)
	}

	a.send(protocol.RelayFrame{
		Type:        protocol.RelayTypeData,
		SessionID:   a.sid,
		SenderID:    a.key,
		RecipientID: protocol.RelayNodeKey("missing"),
		Payload:     payload,
	})
	if e := a.recv(); e.Type != protocol.RelayTypeError {
		t.Fatalf("missing recipient error type = %d", e.Type)
	}
}

func TestServerRejectsSpoofedAndCrossNetworkFrames(t *testing.T) {
	signer, _ := NewTicketSigner(testSecret)
	srv, err := Listen("127.0.0.1:0", signer, Options{SessionTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve()

	a := newRelayTestClient(t, srv, signer, "net-1", "node-a")
	b := newRelayTestClient(t, srv, signer, "net-2", "node-b")

	a.send(protocol.RelayFrame{
		Type:        protocol.RelayTypeData,
		SessionID:   a.sid,
		SenderID:    a.key ^ 1,
		RecipientID: b.key,
		Payload:     []byte("spoof"),
	})
	_ = b.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	buf := make([]byte, 2048)
	if _, err := b.conn.Read(buf); err == nil {
		t.Fatal("spoofed sender frame was delivered")
	}

	a.send(protocol.RelayFrame{
		Type:        protocol.RelayTypeData,
		SessionID:   a.sid,
		SenderID:    a.key,
		RecipientID: b.key,
		Payload:     []byte("cross-network"),
	})
	if e := a.recv(); e.Type != protocol.RelayTypeError {
		t.Fatalf("cross-network error type = %d", e.Type)
	}
}

func TestServerRejectsIdleSession(t *testing.T) {
	signer, _ := NewTicketSigner(testSecret)
	srv, err := Listen("127.0.0.1:0", signer, Options{SessionTTL: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve()

	a := newRelayTestClient(t, srv, signer, "net-1", "node-a")
	time.Sleep(80 * time.Millisecond)
	a.send(protocol.RelayFrame{Type: protocol.RelayTypePing, SessionID: a.sid, SenderID: a.key})
	_ = a.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err := a.conn.Read(make([]byte, 256)); err == nil {
		t.Fatal("idle session was kept alive")
	}
	if n := srv.Stats().Sessions; n != 0 {
		t.Fatalf("sessions = %d, want 0", n)
	}
}

func TestServerIgnoresInvalidHello(t *testing.T) {
	signer, _ := NewTicketSigner(testSecret)
	srv, err := Listen("127.0.0.1:0", signer, Options{SessionTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve()

	conn, err := net.DialUDP("udp", nil, mustResolve(t, srv.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var sid [16]byte
	sid[0] = 9
	f := protocol.RelayFrame{Type: protocol.RelayTypeHello, SessionID: sid, SenderID: 42, Payload: []byte("bogus")}
	b, _ := f.MarshalBinary()
	if _, err := conn.Write(b); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, err := conn.Read(make([]byte, 256)); err == nil {
		t.Fatal("invalid HELLO received a response")
	}
}

func mustResolve(t *testing.T, addr string) *net.UDPAddr {
	t.Helper()
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	return ua
}

func (c *relayTestClient) registerDirect(conn *net.UDPConn, srv *Server) {
	c.t.Helper()
	addr := mustResolve(c.t, srv.Addr())
	c.sendFrom(conn, addr, protocol.RelayFrame{
		Type:      protocol.RelayTypeDirectRegister,
		SessionID: c.sid,
		SenderID:  c.key,
		Payload:   []byte(c.ticket),
	})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65535)
	n, err := conn.Read(buf)
	if err != nil {
		c.t.Fatalf("direct register ack: %v", err)
	}
	f, err := protocol.ParseRelayFrame(buf[:n])
	if err != nil {
		c.t.Fatalf("parse direct register ack: %v", err)
	}
	if f.Type != protocol.RelayTypeDirectRegisterAck || f.RecipientID != c.key {
		c.t.Fatalf("direct register ack = %+v", f)
	}
}

func TestServerDirectRegisterAndLookup(t *testing.T) {
	signer, _ := NewTicketSigner(testSecret)
	srv, err := Listen("127.0.0.1:0", signer, Options{SessionTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve()

	a := newRelayTestClient(t, srv, signer, "net-1", "node-a")
	b := newRelayTestClient(t, srv, signer, "net-1", "node-b")
	c := newRelayTestClient(t, srv, signer, "net-2", "node-c")

	aDirect := listenDirect(t)
	defer aDirect.Close()
	bDirect := listenDirect(t)
	defer bDirect.Close()
	cDirect := listenDirect(t)
	defer cDirect.Close()

	a.registerDirect(aDirect, srv)
	b.registerDirect(bDirect, srv)
	c.registerDirect(cDirect, srv)

	a.send(protocol.RelayFrame{
		Type:        protocol.RelayTypeDirectLookup,
		SessionID:   a.sid,
		SenderID:    a.key,
		RecipientID: b.key,
	})
	got := a.recv()
	if got.Type != protocol.RelayTypeDirectPeer || got.RecipientID != b.key {
		t.Fatalf("direct peer response = %+v", got)
	}
	if string(got.Payload) != bDirect.LocalAddr().String() {
		t.Fatalf("direct peer addr = %q, want %q", got.Payload, bDirect.LocalAddr().String())
	}

	a.send(protocol.RelayFrame{
		Type:        protocol.RelayTypeDirectLookup,
		SessionID:   a.sid,
		SenderID:    a.key,
		RecipientID: c.key,
	})
	got = a.recv()
	if got.Type != protocol.RelayTypeDirectPeer || len(got.Payload) != 0 {
		t.Fatalf("cross-network direct lookup = %+v", got)
	}
}

func TestControlRateLimitPerIP(t *testing.T) {
	s := &Server{control: make(map[string]*controlBucket), ctrlRate: 2, ctrlBurst: 2}
	addr := &net.UDPAddr{IP: net.ParseIP("198.51.100.10"), Port: 5000}
	now := time.Now()
	if !s.allowControl(addr, now) || !s.allowControl(addr, now) {
		t.Fatal("initial burst should be allowed")
	}
	if s.allowControl(addr, now) {
		t.Fatal("third control frame within the burst window should be dropped")
	}
	if !s.allowControl(&net.UDPAddr{IP: net.ParseIP("198.51.100.11"), Port: 5000}, now) {
		t.Fatal("rate limit must be per source IP")
	}
	if !s.allowControl(addr, now.Add(500*time.Millisecond)) {
		t.Fatal("token bucket should refill over time")
	}
}

func listenDirect(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}
