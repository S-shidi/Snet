package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"snet/internal/protocol"
)

func newTestServerFromStore(t *testing.T, store *Store, opts Options) (*httptest.Server, *Store) {
	t.Helper()
	return httptest.NewServer(NewHandler(store, opts)), store
}

func TestNetworkInviteUsesStoredSnetServURL(t *testing.T) {
	store := NewStore()
	store.SetSnetServURL("https://coordinator.example:8090")
	created, err := store.CreateNetwork(testKey(70), "device-invite", "invite", "10.97.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.NetworkInviteServer(created.NetworkID); got != "https://coordinator.example:8090" {
		t.Fatalf("NetworkInviteServer = %q", got)
	}
	info, err := store.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.SnetServURL != "https://coordinator.example:8090" {
		t.Fatalf("NetworkInfo.SnetServURL = %q", info.SnetServURL)
	}

	ts, _ := newTestServerFromStore(t, store, Options{AdminToken: "admin-token"})
	var invite struct {
		Link string `json:"link"`
	}
	resp := doJSON(t, http.MethodGet, ts.URL+"/admin/networks/"+created.NetworkID+"/invite-link", "admin-token", nil, &invite)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin invite = %d", resp.StatusCode)
	}
	_, _, serverURL, _, err := protocol.ParseInviteLink(invite.Link)
	if err != nil {
		t.Fatal(err)
	}
	if serverURL != "https://coordinator.example:8090" {
		t.Fatalf("admin invite server = %q", serverURL)
	}

	var rotated protocol.RotateInviteResp
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks/"+created.NetworkID+"/rotate-invite", created.Token, nil, &rotated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate invite = %d", resp.StatusCode)
	}
	_, _, serverURL, _, err = protocol.ParseInviteLink(rotated.Link)
	if err != nil {
		t.Fatal(err)
	}
	if serverURL != "https://coordinator.example:8090" {
		t.Fatalf("rotated invite server = %q", serverURL)
	}
}
