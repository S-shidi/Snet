package server

import (
	"strings"
	"testing"
)

func TestCreateNetworkRejectsOverlappingSubnet(t *testing.T) {
	s := NewStore()

	if _, err := s.CreateNetwork(testKey(1), "dev-aaaa", "a", "10.88.0.0/16", false); err != nil {
		t.Fatalf("create /16: %v", err)
	}

	// A /24 inside the existing /16 must be rejected by default.
	if _, err := s.CreateNetwork(testKey(2), "dev-bbbb", "b", "10.88.1.0/24", false); err == nil {
		t.Fatal("expected overlapping /24 to be rejected")
	} else if !strings.Contains(err.Error(), "重叠") {
		t.Fatalf("error should mention overlap, got %v", err)
	}

	// allowOverlap opts in explicitly.
	if _, err := s.CreateNetwork(testKey(3), "dev-cccc", "c", "10.88.1.0/24", false,
		networkShareOpts{allowOverlap: true}); err != nil {
		t.Fatalf("allowOverlap should permit the overlap: %v", err)
	}
}

func TestAutoSubnetSkipsSupernetOverlap(t *testing.T) {
	s := NewStore()

	if _, err := s.CreateNetwork(testKey(1), "dev-aaaa", "a", "10.88.0.0/16", false); err != nil {
		t.Fatalf("create /16: %v", err)
	}
	created, err := s.CreateNetwork(testKey(2), "dev-bbbb", "b", "", false)
	if err != nil {
		t.Fatalf("auto subnet: %v", err)
	}
	if subnetsOverlap(created.Subnet, "10.88.0.0/16") {
		t.Fatalf("auto subnet %s overlaps the existing /16", created.Subnet)
	}
}

func TestUpdateNetworkRejectsOverlappingSubnet(t *testing.T) {
	s := NewStore()

	if _, err := s.CreateNetwork(testKey(1), "dev-aaaa", "a", "10.88.0.0/16", false); err != nil {
		t.Fatalf("create /16: %v", err)
	}
	b, err := s.CreateNetwork(testKey(2), "dev-bbbb", "b", "10.99.0.0/24", false)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if err := s.UpdateNetworkSettings(b.Token, "", "10.88.1.0/24", nil); err == nil {
		t.Fatal("expected overlapping subnet update to be rejected")
	} else if !strings.Contains(err.Error(), "重叠") {
		t.Fatalf("error should mention overlap, got %v", err)
	}
}

func TestAutoSubnetPoolExhausted(t *testing.T) {
	s := NewStore()
	// Claim the whole aligned /12 pool, then auto allocation must fail instead
	// of handing out a duplicate subnet.
	if _, err := s.CreateNetwork(testKey(1), "dev-aaaa", "a", "10.80.0.0/12", false); err != nil {
		t.Fatalf("create /12: %v", err)
	}
	if _, err := s.CreateNetwork(testKey(2), "dev-bbbb", "b", "", false); err == nil {
		t.Fatal("expected ErrSubnetPoolExhausted")
	}
}
