package client

import (
	"strings"
	"testing"
)

// withPhysicalSubnets overrides the physical LAN lister for the duration of a
// test so attach can be exercised without touching the host routing table.
func withPhysicalSubnets(t *testing.T, subnets []string) {
	t.Helper()
	orig := physicalSubnetsFn
	physicalSubnetsFn = func() []string { return subnets }
	t.Cleanup(func() { physicalSubnetsFn = orig })
}

func TestAttachRejectsPhysicalLANConflict(t *testing.T) {
	withPhysicalSubnets(t, []string{"192.168.1.0/24"})
	d, _ := newTestDaemon(t)

	err := d.attach("NET1", "home", "node1", "192.168.1.2", "tok", "", "192.168.1.0/24", false)
	if err == nil || !strings.Contains(err.Error(), "物理局域网冲突") {
		t.Fatalf("expected physical LAN conflict, got %v", err)
	}
}

func TestAttachRejectsPhysicalLANSupernet(t *testing.T) {
	// A /16 physical LAN must also block a /24 virtual network inside it.
	withPhysicalSubnets(t, []string{"192.168.0.0/16"})
	d, _ := newTestDaemon(t)

	err := d.attach("NET1", "home", "node1", "192.168.1.2", "tok", "", "192.168.1.0/24", false)
	if err == nil || !strings.Contains(err.Error(), "物理局域网冲突") {
		t.Fatalf("expected supernet conflict, got %v", err)
	}
}

func TestAttachAllowsNonConflictingLAN(t *testing.T) {
	withPhysicalSubnets(t, []string{"192.168.1.0/24"})
	d, _ := newTestDaemon(t)

	if err := d.attach("NET1", "home", "node1", "10.88.0.2", "tok", "", "10.88.0.0/24", false); err != nil {
		t.Fatalf("non-conflicting subnet should attach: %v", err)
	}
}

func TestAttachLANConflictBypass(t *testing.T) {
	withPhysicalSubnets(t, []string{"192.168.1.0/24"})
	t.Setenv("SNET_IGNORE_LAN_CONFLICT", "1")
	d, _ := newTestDaemon(t)

	if err := d.attach("NET1", "home", "node1", "192.168.1.2", "tok", "", "192.168.1.0/24", false); err != nil {
		t.Fatalf("SNET_IGNORE_LAN_CONFLICT=1 should allow attach: %v", err)
	}
}

func TestCheckLANConflictParseFailureIsSafe(t *testing.T) {
	withPhysicalSubnets(t, []string{"not-a-cidr"})
	if err := checkLANConflict("10.88.0.0/24"); err == nil {
		t.Fatal("unparseable local subnet should be treated as a conflict")
	}
}
