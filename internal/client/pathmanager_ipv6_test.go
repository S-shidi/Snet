package client

import "testing"

func TestPeerV6AddrRejectsULAAndLinkLocal(t *testing.T) {
	for _, raw := range []string{
		"[fdbc:b1aa:db4c::824]:51820",
		"[fc00::1]:51820",
		"[fe80::1]:51820",
		"[::1]:51820",
	} {
		if got := peerV6Addr(raw); got != nil {
			t.Fatalf("peerV6Addr(%q) = %v, want nil", raw, got)
		}
	}
	if got := peerV6Addr("[2001:db8::1]:51820"); got == nil {
		t.Fatal("global IPv6 peer address was rejected")
	}
}

func TestIsVirtualInterfaceName(t *testing.T) {
	for _, name := range []string{"docker0", "veth123", "br-abc123", "virbr0", "tun0", "wg0"} {
		if !isVirtualInterfaceName(name) {
			t.Fatalf("%q should be treated as virtual", name)
		}
	}
	for _, name := range []string{"eth0", "enp1s0", "wlan0"} {
		if isVirtualInterfaceName(name) {
			t.Fatalf("%q should not be treated as virtual", name)
		}
	}
}
