package server

import (
	"net/http"
	"testing"

	"snet/internal/protocol"
)

func bindDeviceForAuthTest(t *testing.T, s *Store, deviceID string, key int) string {
	t.Helper()
	code, _ := genOneCode(t, s)
	tok, err := s.BindDevice(code, deviceID, testKey(key), "")
	if err != nil {
		t.Fatalf("BindDevice(%s): %v", deviceID, err)
	}
	if tok == "" {
		t.Fatalf("BindDevice(%s) returned empty token", deviceID)
	}
	return tok
}

func createNetworkForAuthTest(t *testing.T, tsURL, deviceID string, key int) protocol.CreateNetworkResp {
	t.Helper()
	var created protocol.CreateNetworkResp
	resp := doJSON(t, http.MethodPost, tsURL+"/api/v1/networks", "", protocol.CreateNetworkReq{
		PublicKey: testKey(key),
		DeviceID:  deviceID,
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create network = %d, want 201", resp.StatusCode)
	}
	return created
}

func TestDeviceTokenNotAcceptedAsNodeBearer(t *testing.T) {
	ts, s := newTestServer(t)
	deviceToken := bindDeviceForAuthTest(t, s, "dev-aaaa", 1)
	created := createNetworkForAuthTest(t, ts.URL, "dev-aaaa", 2)

	resp := doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/"+created.NetworkID+"/peers", deviceToken, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("device token as node bearer = %d, want 401", resp.StatusCode)
	}
}

func TestNodeTokenNotAcceptedAsDeviceToken(t *testing.T) {
	ts, s := newTestServer(t)
	bindDeviceForAuthTest(t, s, "dev-aaaa", 1)
	created := createNetworkForAuthTest(t, ts.URL, "dev-aaaa", 2)

	resp := doJSONH(t, http.MethodGet, ts.URL+"/api/v1/devices/dev-aaaa/networks", "", map[string]string{
		"X-Device-Token": created.Token,
	}, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("node token as device token (device networks) = %d, want 401", resp.StatusCode)
	}

	resp = doJSONH(t, http.MethodGet, ts.URL+"/api/v1/devices/auth-status?deviceId=dev-aaaa", "", map[string]string{
		"X-Device-Token": created.Token,
	}, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("node token as device token (auth-status) = %d, want 401", resp.StatusCode)
	}
}

func TestDeviceTokenNotAcceptedForOwnerRoute(t *testing.T) {
	ts, s := newTestServer(t)
	deviceToken := bindDeviceForAuthTest(t, s, "dev-aaaa", 1)
	created := createNetworkForAuthTest(t, ts.URL, "dev-aaaa", 2)

	resp := doJSON(t, http.MethodPatch, ts.URL+"/api/v1/networks/"+created.NetworkID, deviceToken,
		map[string]any{"name": "hijacked"}, nil)
	// ownerNID deliberately returns 404 for an unknown/non-owner token to
	// avoid leaking whether the network exists; 401/403 are also acceptable
	// denial codes for this isolation assertion.
	if resp.StatusCode != http.StatusUnauthorized &&
		resp.StatusCode != http.StatusForbidden &&
		resp.StatusCode != http.StatusNotFound {
		t.Fatalf("device token on owner route = %d, want 401/403/404", resp.StatusCode)
	}
}
