package client

import (
	"testing"

	"snet/internal/protocol"
)

func TestRoutePlannerInjectsDelegatedCIDRIntoTargetPeer(t *testing.T) {
	peers := []protocol.Node{
		{ID: "node-a", IP: "10.88.1.2", AllowedIPs: []string{"192.168.5.0/24"}},
		{ID: "node-b", IP: "10.88.1.3"},
	}
	routes := []protocol.Route{
		{TargetNodeID: "node-b", CIDR: "192.168.9.0/24", Status: protocol.RouteStatusActive},
	}
	out := NewRoutePlanner("node-self", true).Apply(peers, routes)

	if len(out[0].AllowedIPs) != 1 || out[0].AllowedIPs[0] != "192.168.5.0/24" {
		t.Fatalf("unrelated peer changed: %+v", out[0].AllowedIPs)
	}
	if len(out[1].AllowedIPs) != 1 || out[1].AllowedIPs[0] != "192.168.9.0/24" {
		t.Fatalf("target peer = %+v, want the delegated CIDR", out[1].AllowedIPs)
	}
	// The input must not be mutated.
	if len(peers[1].AllowedIPs) != 0 {
		t.Fatalf("input was mutated: %+v", peers[1].AllowedIPs)
	}
}

func TestRoutePlannerSkipsRoutesOverlappingLocalLAN(t *testing.T) {
	orig := physicalSubnetsFn
	physicalSubnetsFn = func() []string { return []string{"192.168.1.0/24"} }
	t.Cleanup(func() { physicalSubnetsFn = orig })

	peers := []protocol.Node{{ID: "node-b", IP: "10.88.1.3"}}
	routes := []protocol.Route{
		{TargetNodeID: "node-b", CIDR: "192.168.0.0/16", Status: protocol.RouteStatusActive},
		{TargetNodeID: "node-b", CIDR: "192.168.1.0/24", Status: protocol.RouteStatusActive},
		{TargetNodeID: "node-b", CIDR: "192.168.50.0/24", Status: protocol.RouteStatusActive},
	}
	p := NewRoutePlanner("node-self", true)
	out := p.Apply(peers, routes)
	if len(out[0].AllowedIPs) != 1 || out[0].AllowedIPs[0] != "192.168.50.0/24" {
		t.Fatalf("allowed subnets = %+v, want only the non-conflicting route", out[0].AllowedIPs)
	}
	skipped := p.Skipped()
	if len(skipped) != 2 || skipped[0] != "192.168.0.0/16" || skipped[1] != "192.168.1.0/24" {
		t.Fatalf("skipped = %+v, want both overlapping routes", skipped)
	}
}

func TestRoutePlannerIgnoresRoutesWhenRelayDisabled(t *testing.T) {
	peers := []protocol.Node{{ID: "node-b", IP: "10.88.1.3"}}
	routes := []protocol.Route{
		{TargetNodeID: "node-b", CIDR: "192.168.9.0/24", Status: protocol.RouteStatusActive},
	}
	out := NewRoutePlanner("node-self", false).Apply(peers, routes)
	if len(out[0].AllowedIPs) != 0 {
		t.Fatalf("use_relay=false must ignore the delegated routes: %+v", out[0].AllowedIPs)
	}
}

func TestRoutePlannerIgnoresPendingRoutes(t *testing.T) {
	peers := []protocol.Node{{ID: "node-b", IP: "10.88.1.3"}}
	routes := []protocol.Route{
		{TargetNodeID: "node-b", CIDR: "192.168.9.0/24", Status: protocol.RouteStatusPending},
	}
	out := NewRoutePlanner("node-self", true).Apply(peers, routes)
	if len(out[0].AllowedIPs) != 0 {
		t.Fatalf("pending routes must not be injected: %+v", out[0].AllowedIPs)
	}
}

func TestRoutePlannerSkipsSelfTargetedRoutes(t *testing.T) {
	// The gateway must not route its own delegated traffic back into the
	// tunnel: it has to forward it towards the LAN.
	peers := []protocol.Node{{ID: "node-self", IP: "10.88.1.2"}}
	routes := []protocol.Route{
		{TargetNodeID: "node-self", CIDR: "192.168.9.0/24", Status: protocol.RouteStatusActive},
	}
	out := NewRoutePlanner("node-self", true).Apply(peers, routes)
	if len(out[0].AllowedIPs) != 0 {
		t.Fatalf("self-targeted route must be skipped: %+v", out[0].AllowedIPs)
	}
}

func TestRoutePlannerMergesWithOwnSubnetsWithoutDuplicates(t *testing.T) {
	peers := []protocol.Node{
		{ID: "node-b", IP: "10.88.1.3", AllowedIPs: []string{"192.168.9.0/24", "192.168.7.0/24"}},
	}
	routes := []protocol.Route{
		{TargetNodeID: "node-b", CIDR: "192.168.9.0/24", Status: protocol.RouteStatusActive},
		{TargetNodeID: "node-b", CIDR: "192.168.8.0/24", Status: protocol.RouteStatusActive},
	}
	out := NewRoutePlanner("node-self", true).Apply(peers, routes)
	got := out[0].AllowedIPs
	if len(got) != 3 {
		t.Fatalf("allowed subnets = %+v, want 3 entries", got)
	}
	seen := map[string]bool{}
	for _, s := range got {
		if seen[s] {
			t.Fatalf("duplicate subnet %q in %+v", s, got)
		}
		seen[s] = true
	}
	for _, want := range []string{"192.168.9.0/24", "192.168.7.0/24", "192.168.8.0/24"} {
		if !seen[want] {
			t.Fatalf("missing %q in %+v", want, got)
		}
	}
}

func TestRoutePlannerReturnsInputWhenNothingApplies(t *testing.T) {
	peers := []protocol.Node{{ID: "node-b", IP: "10.88.1.3"}}
	out := NewRoutePlanner("node-self", true).Apply(peers, nil)
	if len(out) != 1 || out[0].ID != "node-b" {
		t.Fatalf("unexpected result: %+v", out)
	}
}

func TestConfigRelaySwitchDefaults(t *testing.T) {
	// A fresh config has the gateway role off and the relay routes on.
	c := &Config{}
	if c.RelayAllowed() {
		t.Fatal("allow_relay must default to false")
	}
	if !c.RelayUsed() {
		t.Fatal("use_relay must default to true")
	}
	off := false
	c.UseRelay = &off
	if c.RelayUsed() {
		t.Fatal("use_relay=false must disable the relay routes")
	}
	c.AllowRelay = true
	if !c.RelayAllowed() {
		t.Fatal("allow_relay=true must enable the gateway role")
	}
}

func TestRelayGatewaySubnetsOnlyFromActiveNetworks(t *testing.T) {
	nets := map[string]*NetworkCfg{
		"a": {Subnet: "10.88.1.0/24", Active: true},
		"b": {Subnet: "10.88.2.0/24", Active: false},
		"c": {Subnet: "10.88.1.0/24", Active: true},
		"d": {Subnet: "", Active: true},
	}
	got := relayGatewaySubnets(nets)
	if len(got) != 1 || got[0] != "10.88.1.0/24" {
		t.Fatalf("subnets = %+v, want [10.88.1.0/24]", got)
	}
}

func TestRoutePlannerServedBySelf(t *testing.T) {
	routes := []protocol.Route{
		{TargetNodeID: "node-self", CIDR: "192.168.50.0/24", Status: protocol.RouteStatusActive},
		{TargetNodeID: "node-other", CIDR: "192.168.60.0/24", Status: protocol.RouteStatusActive},
		{TargetNodeID: "node-self", CIDR: "192.168.70.0/24", Status: protocol.RouteStatusPending},
	}
	got := NewRoutePlanner("node-self", true).ServedBySelf(routes)
	if len(got) != 1 || got[0] != "192.168.50.0/24" {
		t.Fatalf("served = %+v, want [192.168.50.0/24]", got)
	}
}
