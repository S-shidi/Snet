package server

import (
	"net/http"
	"testing"

	"snet/internal/protocol"
)

// TestRotateInviteReplacesOldLink verifies the invite-link rotation: the
// owner gets a fresh pairing code and link, the code shared before the
// rotation stops working and the new one joins.
func TestRotateInviteReplacesOldLink(t *testing.T) {
	ts, _ := newTestServer(t)

	var created protocol.CreateNetworkResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: testKey(31), DeviceID: "dev-owner", Name: "office"}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	oldCode := created.PairingCode

	var rotated protocol.RotateInviteResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/rotate-invite", created.Token, nil, &rotated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate = %d", resp.StatusCode)
	}
	if rotated.NetworkID != created.NetworkID {
		t.Fatalf("rotate network = %q, want %q", rotated.NetworkID, created.NetworkID)
	}
	if rotated.PairingCode == "" || rotated.PairingCode == oldCode {
		t.Fatalf("rotate code = %q, want a new non-empty code", rotated.PairingCode)
	}
	if rotated.Link == "" {
		t.Fatal("rotate link is empty")
	}
	nid, code, _, name, err := protocol.ParseLink(rotated.Link)
	if err != nil {
		t.Fatalf("parse rotated link: %v", err)
	}
	if nid != created.NetworkID || code != rotated.PairingCode {
		t.Fatalf("rotated link = %q/%q, want %q/%q", nid, code, created.NetworkID, rotated.PairingCode)
	}
	if name != "office" {
		t.Fatalf("rotated link name = %q, want %q", name, "office")
	}

	// The link shared before the rotation must not join any more.
	var joined protocol.JoinResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: oldCode, PublicKey: testKey(32), DeviceID: "dev-old"}, &joined)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("join with old code = %d, want 401", resp.StatusCode)
	}

	// The new code joins.
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: rotated.PairingCode, PublicKey: testKey(33), DeviceID: "dev-new"}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join with new code = %d, want 200", resp.StatusCode)
	}
}

// TestRotateInviteRequiresOwner makes sure a plain member cannot invalidate
// the invite link of a network it does not own.
func TestRotateInviteRequiresOwner(t *testing.T) {
	ts, _ := newTestServer(t)

	var created protocol.CreateNetworkResp
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: testKey(34), DeviceID: "dev-owner"}, &created)

	var joined protocol.JoinResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: testKey(35), DeviceID: "dev-member"}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join = %d", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/rotate-invite", joined.Token, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("member rotate = %d, want 401", resp.StatusCode)
	}

	// The owner's code still works after the rejected rotation.
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/join", "",
		protocol.JoinReq{Code: created.PairingCode, PublicKey: testKey(36), DeviceID: "dev-other"}, &joined)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("join after rejected rotate = %d, want 200", resp.StatusCode)
	}
}
