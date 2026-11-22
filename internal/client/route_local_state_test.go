package client

import (
	"testing"

	"github.com/S-shidi/snet/shared/protocol"
)

func TestRouteEnabledCopyAnnotatesLocalState(t *testing.T) {
	withPhysicalSubnets(t, []string{"10.7.86.0/24"})
	d, _ := newTestDaemon(t)
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET1": {
			Name:   "home",
			NodeID: "self",
			IP:     "10.88.0.44",
			Token:  "tok",
			Subnet: "10.88.0.0/24",
			Active: true,
		},
	}

	out := d.routeEnabledCopy("NET1", []protocol.Route{
		{TargetNodeID: "self", CIDR: "10.7.86.0/24", Status: protocol.RouteStatusActive},
		{TargetNodeID: "other", CIDR: "10.7.86.0/24", Status: protocol.RouteStatusActive},
		{TargetNodeID: "other", CIDR: "192.168.50.0/24", Status: protocol.RouteStatusActive},
		{TargetNodeID: "other", CIDR: "10.7.86.0/24", Status: protocol.RouteStatusPending},
	})
	if out[0].LocalState != protocol.RouteLocalSelf {
		t.Fatalf("self route local state = %q, want %q", out[0].LocalState, protocol.RouteLocalSelf)
	}
	if out[1].LocalState != protocol.RouteLocalLANConflict {
		t.Fatalf("LAN route local state = %q, want %q", out[1].LocalState, protocol.RouteLocalLANConflict)
	}
	if out[2].LocalState != "" {
		t.Fatalf("unrelated route local state = %q, want empty", out[2].LocalState)
	}
	if out[3].LocalState != "" {
		t.Fatalf("pending route local state = %q, want empty", out[3].LocalState)
	}
}
