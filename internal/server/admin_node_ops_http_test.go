package server

import (
	"net/http"
	"testing"
)

func TestAdminNodeRoleAndSubnetsHTTP(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminToken: "secret"})

	var created map[string]any
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "", map[string]any{"publicKey": testKey(1)}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	nid := created["networkId"].(string)
	code := created["pairingCode"].(string)
	var joined map[string]any
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+nid+"/join", "",
		map[string]any{"code": code, "publicKey": testKey(2), "deviceId": "dev-joiner"}, &joined)
	nodeID := joined["nodeId"].(string)

	resp = doJSON(t, http.MethodPatch, ts.URL+"/admin/networks/"+nid+"/nodes/"+nodeID+"/role", "secret",
		map[string]any{"role": "admin"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set role = %d", resp.StatusCode)
	}
	var nodes []map[string]any
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks/"+nid+"/nodes", "secret", nil, &nodes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("nodes = %d", resp.StatusCode)
	}
	found := false
	for _, n := range nodes {
		if n["id"] == nodeID {
			found = true
			if n["role"] != "admin" {
				t.Fatalf("role = %v", n["role"])
			}
		}
	}
	if !found {
		t.Fatalf("node %s not found", nodeID)
	}

	resp = doJSON(t, http.MethodPatch, ts.URL+"/admin/networks/"+nid+"/nodes/"+nodeID+"/subnets", "secret",
		map[string]any{"subnets": []string{"192.168.50.0/24"}}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set subnets = %d", resp.StatusCode)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks/"+nid+"/nodes", "secret", nil, &nodes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("nodes after subnets = %d", resp.StatusCode)
	}
	for _, n := range nodes {
		if n["id"] != nodeID {
			continue
		}
		subnets, _ := n["allowedSubnets"].([]any)
		if len(subnets) != 1 || subnets[0] != "192.168.50.0/24" {
			t.Fatalf("allowedSubnets = %v", n["allowedSubnets"])
		}
	}

	resp = doJSON(t, http.MethodPatch, ts.URL+"/admin/networks/"+nid+"/nodes/"+nodeID+"/role", "secret",
		map[string]any{"role": "owner"}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("transfer owner = %d", resp.StatusCode)
	}
	var info map[string]any
	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/networks/"+nid, "secret", nil, &info)
	if resp.StatusCode != http.StatusOK || info["ownerNodeId"] != nodeID {
		t.Fatalf("network info = %d owner=%v", resp.StatusCode, info["ownerNodeId"])
	}
}
