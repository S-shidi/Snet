package client

import (
	"errors"
	"testing"

	"snet/internal/protocol"
)

func TestAddPeerRoutesReportsRouteConflict(t *testing.T) {
	orig := addSubnetRouteFn
	addSubnetRouteFn = func(string, string) error { return errors.New("RTNETLINK answers: File exists") }
	t.Cleanup(func() { addSubnetRouteFn = orig })

	err := addPeerRoutes("tun0", protocol.Node{IP: "10.88.0.2", AllowedSubnets: []string{"192.168.1.0/24"}})
	if err == nil {
		t.Fatal("expected addPeerRoutes to return the route error")
	}
}

func TestNoteSkippedRoutesMarksNetworkError(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET1": {
			Name:   "home",
			NodeID: "node1",
			IP:     "10.88.0.2",
			Token:  "tok",
			Subnet: "10.88.0.0/24",
			Active: true,
		},
	}

	d.mu.Lock()
	d.noteSkippedRoutesLocked("NET1", []string{"192.168.1.0/24"})
	d.mu.Unlock()

	status, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	nets, _ := status["networks"].([]map[string]any)
	if len(nets) != 1 || nets[0]["state"] != "error" {
		t.Fatalf("network status = %+v, want error", nets)
	}
	if nets[0]["error"] != "与本地局域网冲突，已跳过: 192.168.1.0/24" {
		t.Fatalf("error = %v", nets[0]["error"])
	}
}

func TestNoteApplyErrMarksNetworkError(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET1": {
			Name:   "home",
			NodeID: "node1",
			IP:     "10.88.0.2",
			Token:  "tok",
			Subnet: "10.88.0.0/24",
			Active: true,
		},
	}

	d.mu.Lock()
	d.noteApplyErrLocked("NET1", ErrRouteConflict)
	d.mu.Unlock()

	status, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	nets, _ := status["networks"].([]map[string]any)
	if len(nets) != 1 {
		t.Fatalf("networks = %d, want 1", len(nets))
	}
	if nets[0]["state"] != "error" {
		t.Fatalf("state = %v, want error", nets[0]["state"])
	}
	if nets[0]["error"] != "路由安装失败，可能与本机其他网络冲突" {
		t.Fatalf("error = %v", nets[0]["error"])
	}

	// A successful apply clears the error.
	d.mu.Lock()
	d.noteApplyErrLocked("NET1", nil)
	d.mu.Unlock()
	status, _ = d.Status()
	nets, _ = status["networks"].([]map[string]any)
	if nets[0]["state"] == "error" {
		t.Fatalf("state should clear after success, got %v", nets[0]["state"])
	}
}
