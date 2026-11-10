package server

import (
	"net/http"
	"testing"

	"snet/internal/protocol"
)

func TestAnonymousEnrollmentRateLimited(t *testing.T) {
	ts, _ := newTestServer(t)

	// /devices/bind has the smallest enrollment window (20/min/IP), so it is
	// the cheapest representative of the anonymous enrollment limiters.
	body := protocol.BindDeviceReq{
		Code:      "INVALIDCODE",
		DeviceID:  "dev-aaaa",
		PublicKey: testKey(9),
	}
	for i := 0; i < 20; i++ {
		resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "", body, nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate limited before the 20/min window was exhausted", i+1)
		}
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/bind", "", body, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21st anonymous bind = %d, want 429", resp.StatusCode)
	}
}

func TestRequireDeviceAuthBlocksAnonymousCreateJoinRegister(t *testing.T) {
	ts, s := newTestServerOpts(t, Options{RequireDeviceAuth: true})

	unbound := protocol.CreateNetworkReq{
		PublicKey: testKey(10),
		DeviceID:  "dev-unbound",
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "", unbound, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous create with device gate = %d, want 403", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/NET1234/join", "", protocol.JoinReq{
		Code:      "BADCODE123",
		PublicKey: testKey(11),
		DeviceID:  "dev-unbound",
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous join with device gate = %d, want 403", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "", protocol.RegisterDeviceReq{
		DeviceID:  "dev-unbound",
		PublicKey: testKey(12),
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous register with device gate = %d, want 403", resp.StatusCode)
	}

	// A bound device passes the same routes. The gate checks the DeviceID's
	// stored binding, not the X-Device-Token header; send the token anyway to
	// document that the token is not the enrollment credential.
	boundToken := bindDeviceForAuthTest(t, s, "dev-bound", 13)
	var created protocol.CreateNetworkResp
	resp = doJSONH(t, http.MethodPost, ts.URL+"/api/v1/networks", "", map[string]string{
		"X-Device-Token": boundToken,
	}, protocol.CreateNetworkReq{
		PublicKey: testKey(14),
		DeviceID:  "dev-bound",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bound create = %d, want 201", resp.StatusCode)
	}

	joinerToken := bindDeviceForAuthTest(t, s, "dev-bound2", 15)
	resp = doJSONH(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "", map[string]string{
		"X-Device-Token": joinerToken,
	}, protocol.JoinReq{
		Code:      created.PairingCode,
		PublicKey: testKey(16),
		DeviceID:  "dev-bound2",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bound join = %d, want 200", resp.StatusCode)
	}

	resp = doJSONH(t, http.MethodPost, ts.URL+"/api/v1/devices", "", map[string]string{
		"X-Device-Token": boundToken,
	}, protocol.RegisterDeviceReq{
		DeviceID:  "dev-bound",
		PublicKey: testKey(14),
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bound register = %d, want 200", resp.StatusCode)
	}
}
