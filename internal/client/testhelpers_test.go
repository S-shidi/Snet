package client

import (
	"net"
	"path/filepath"
	"testing"
)

func newTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfigAt(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDaemonAt(cfg, cfgPath)
	d.SetDeviceIDFile("")
	return d, cfgPath
}

func testWGKey(t *testing.T, i int) string {
	t.Helper()
	_, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	_ = i
	return pub
}

func mustUDPListener(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustPathAddr(t *testing.T, raw string) *net.UDPAddr {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", raw)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}
