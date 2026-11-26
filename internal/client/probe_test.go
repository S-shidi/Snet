package client

import (
	"net"
	"testing"
)

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	_ = conn.Close()
	return port
}

func TestProbeUDPRoundTrip(t *testing.T) {
	port := freeUDPPort(t)
	r := startProbeResponderAt("127.0.0.1", port)
	if r == nil {
		t.Fatal("responder did not start")
	}
	defer r.Close()

	loss, avg, sent, received, err := probeUDP(net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.1"), port, 3)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if sent != 3 || received != 3 {
		t.Fatalf("sent=%d received=%d, want 3/3", sent, received)
	}
	if loss != 0 {
		t.Fatalf("loss=%v, want 0", loss)
	}
	if avg < 0 {
		t.Fatalf("avg=%v, want >= 0", avg)
	}
}

func TestProbeUDPNoResponder(t *testing.T) {
	port := freeUDPPort(t)
	loss, _, sent, received, err := probeUDP(net.ParseIP("127.0.0.1"), net.ParseIP("127.0.0.1"), port, 2)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if sent != 2 || received != 0 {
		t.Fatalf("sent=%d received=%d, want 2/0", sent, received)
	}
	if loss != 100 {
		t.Fatalf("loss=%v, want 100", loss)
	}
}
