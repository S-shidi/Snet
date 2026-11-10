package server

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestProbeServerEchoesSourceEndpoint(t *testing.T) {
	ps, err := StartProbeServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()

	addr := ps.conn.LocalAddr().(*net.UDPAddr)
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("probe read: %v", err)
	}
	var resp struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("probe response %q: %v", buf[:n], err)
	}
	want := conn.LocalAddr().String()
	if resp.Endpoint != want {
		t.Fatalf("endpoint = %q, want %q", resp.Endpoint, want)
	}
}

func TestProbeServerClose(t *testing.T) {
	ps, err := StartProbeServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ps.conn.LocalAddr().(*net.UDPAddr)
	if err := ps.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Close waits for the loop goroutine, so the UDP port must be free again.
	reopened, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("port was not released after Close: %v", err)
	}
	_ = reopened.Close()
}
