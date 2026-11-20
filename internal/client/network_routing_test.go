package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"snet/internal/protocol"
	"snet/internal/server"
)

type countingHandler struct {
	next http.Handler
	mu   sync.Mutex
	hits map[string]int
}

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.hits[r.URL.Path]++
	h.mu.Unlock()
	h.next.ServeHTTP(w, r)
}

func (h *countingHandler) reset() {
	h.mu.Lock()
	h.hits = make(map[string]int)
	h.mu.Unlock()
}

func (h *countingHandler) count(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits[path]
}

func newCountingServer(t *testing.T) (*server.Store, *httptest.Server, *countingHandler) {
	t.Helper()
	store := server.NewStore()
	h := &countingHandler{next: server.NewHandler(store, server.Options{}), hits: make(map[string]int)}
	ts := httptest.NewServer(h)
	return store, ts, h
}

func TestNetworksRoutePeersAndPingsToBoundServer(t *testing.T) {
	_, tsA, countA := newCountingServer(t)
	defer tsA.Close()
	srvB, tsB, countB := newCountingServer(t)
	defer tsB.Close()

	d, cfgPath := newTestDaemon(t)
	createdA, err := d.Create(tsA.URL, 51820, "net-a", "", false)
	if err != nil {
		t.Fatal(err)
	}
	createdB, err := srvB.CreateNetwork(testWGKey(t, 61), "device-b", "net-b", "10.99.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	// Keep an unrelated pending request while joining/using B.
	d.mu.Lock()
	d.cfg.PendingJoins = map[string]*PendingJoin{
		"PENDING-A": {PendingID: "PENDING-A", NetworkID: createdA.NetworkID, SnetServURL: normalizeServer(tsA.URL)},
	}
	d.mu.Unlock()

	if _, err := d.Join(tsB.URL, 0, createdB.NetworkID, createdB.PairingCode); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Peers(createdA.NetworkID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Peers(createdB.NetworkID); err != nil {
		t.Fatal(err)
	}
	if got := countA.count("/api/v1/networks/" + createdA.NetworkID + "/peers"); got != 1 {
		t.Fatalf("A peers calls = %d, want 1", got)
	}
	if got := countB.count("/api/v1/networks/" + createdB.NetworkID + "/peers"); got != 1 {
		t.Fatalf("B peers calls = %d, want 1", got)
	}

	countA.reset()
	countB.reset()
	d.pingServerState(createdA.NetworkID)
	d.pingServerState(createdB.NetworkID)
	if got := countA.count("/api/v1/networks/" + createdA.NetworkID + "/exists"); got != 1 {
		t.Fatalf("A exists calls = %d, want 1", got)
	}
	if got := countB.count("/api/v1/networks/" + createdB.NetworkID + "/exists"); got != 1 {
		t.Fatalf("B exists calls = %d, want 1", got)
	}

	if err := d.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigAt(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewDaemonAt(cfg, cfgPath)
	restarted.SetDeviceIDFile("")
	if restarted.cfg.Networks[createdA.NetworkID].SnetServURL != normalizeServer(tsA.URL) {
		t.Fatalf("A server after restart = %q", restarted.cfg.Networks[createdA.NetworkID].SnetServURL)
	}
	if restarted.cfg.Networks[createdB.NetworkID].SnetServURL != normalizeServer(tsB.URL) {
		t.Fatalf("B server after restart = %q", restarted.cfg.Networks[createdB.NetworkID].SnetServURL)
	}
	countA.reset()
	countB.reset()
	if _, err := restarted.Peers(createdA.NetworkID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Peers(createdB.NetworkID); err != nil {
		t.Fatal(err)
	}
	if countA.count("/api/v1/networks/"+createdA.NetworkID+"/peers") != 1 ||
		countB.count("/api/v1/networks/"+createdB.NetworkID+"/peers") != 1 {
		t.Fatalf("restarted routing counts A=%d B=%d", countA.count("/api/v1/networks/"+createdA.NetworkID+"/peers"), countB.count("/api/v1/networks/"+createdB.NetworkID+"/peers"))
	}

	d.mu.Lock()
	_, aExists := d.cfg.Networks[createdA.NetworkID]
	_, pendingExists := d.cfg.PendingJoins["PENDING-A"]
	d.mu.Unlock()
	if !aExists || !pendingExists {
		t.Fatal("joining/using B cleared A or its pending request")
	}
}

func TestInviteUsesBoundNetworkServer(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.ServerAddr = "https://global.example:8090"
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET-A": {SnetServURL: "https://a.example:8090", NodeID: "a", Token: "ta", Active: true},
		"NET-B": {SnetServURL: "https://b.example:8090", NodeID: "b", Token: "tb", Active: true},
	}
	d.mu.Unlock()
	link := d.inviteLinkLocked("NET-B", "ABCD1234", "net-b")
	_, _, got, _, err := protocol.ParseInviteLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://b.example:8090" {
		t.Fatalf("invite server = %q, want network B", got)
	}
}

func TestJoinWithInviteServerPersistsBinding(t *testing.T) {
	store := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(store, server.Options{}))
	defer ts.Close()
	created, err := store.CreateNetwork(testWGKey(t, 62), "device-owner", "invite-bind", "10.98.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := newTestDaemon(t)
	link := protocol.BuildLink(created.NetworkID, created.PairingCode, ts.URL, "invite-bind")
	// Exercise the same link parsing used by /ctl/join, then the direct Join.
	nid, code, linkServer, _, err := protocol.ParseInviteLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(linkServer, ts.URL) {
		t.Fatalf("link server = %q, want %q", linkServer, ts.URL)
	}
	if _, err := d.Join(linkServer, 0, nid, code); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	nc := d.cfg.Networks[nid]
	d.mu.Unlock()
	if nc == nil || nc.SnetServURL != normalizeServer(ts.URL) {
		t.Fatalf("joined network binding = %+v", nc)
	}
}
