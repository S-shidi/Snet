package client

import (
	"testing"

	"github.com/S-shidi/snet/shared/protocol"
)

func TestRouteAllowedDefaultsToEnabledAndPersistsToggle(t *testing.T) {
	cfg := &Config{}
	if !cfg.RouteAllowed("NET1", "10.20.30.0/24") {
		t.Fatal("new route should default to enabled")
	}
	if !cfg.SetRouteAllowed("NET1", "10.20.30.0/24", false) {
		t.Fatal("toggle to disabled should change state")
	}
	if cfg.RouteAllowed("NET1", "10.20.30.0/24") {
		t.Fatal("disabled route should be off")
	}
	if cfg.SetRouteAllowed("NET1", "10.20.30.0/24", false) {
		t.Fatal("setting the same state should not report a change")
	}
	if !cfg.SetRouteAllowed("NET1", "10.20.30.0/24", true) {
		t.Fatal("toggle back to enabled should change state")
	}
}

func TestPlanRelayRoutesHonorsPerRouteSwitch(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET1": {NodeID: "self", IP: "10.88.1.2", Subnet: "10.88.1.0/24", Active: true},
	}
	nc := d.cfg.Networks["NET1"]
	peers := []protocol.Node{{ID: "gateway", IP: "10.88.1.3"}}
	routes := []protocol.Route{{
		TargetNodeID: "gateway",
		CIDR:         "10.20.30.0/24",
		Status:       protocol.RouteStatusActive,
	}}

	d.cfg.SetRouteAllowed("NET1", "10.20.30.0/24", false)
	out, _ := d.planRelayRoutesLocked("NET1", nc, routes, peers)
	if len(out) != 1 || len(out[0].AllowedIPs) != 0 {
		t.Fatalf("disabled route still injected: %+v", out)
	}

	d.cfg.SetRouteAllowed("NET1", "10.20.30.0/24", true)
	out, _ = d.planRelayRoutesLocked("NET1", nc, routes, peers)
	if len(out) != 1 || len(out[0].AllowedIPs) != 1 || out[0].AllowedIPs[0] != "10.20.30.0/24" {
		t.Fatalf("enabled route not injected: %+v", out)
	}
}
