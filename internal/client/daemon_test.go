package client

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"snet/internal/constants"
	"snet/internal/protocol"
	"snet/internal/server"
)

func newTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfigAt(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDaemonAt(cfg, cfgPath)
	d.SetDeviceIDFile("")
	return d, cfgPath
}

func TestConfigMigrationFromV1(t *testing.T) {
	v1 := `{
  "serverAddr": "https://snet.test",
  "wireguardPort": 51820,
  "privateKey": "aa",
  "networkId": "net123",
  "nodeId": "node1",
  "ip": "10.88.0.5",
  "token": "tok",
  "pairingCode": "pair"
}`
	cfg, err := parseConfig([]byte(v1))
	if err != nil {
		t.Fatal(err)
	}
	nc := cfg.Networks["net123"]
	if nc == nil {
		t.Fatal("legacy network not migrated")
	}
	if nc.NodeID != "node1" || nc.IP != "10.88.0.5" || nc.Token != "tok" || nc.PairingCode != "pair" {
		t.Fatalf("migrated cfg wrong: %+v", nc)
	}
	if nc.Subnet != defaultSubnet || !nc.Active || nc.Port != 51820 {
		t.Fatalf("migrated defaults wrong: %+v", nc)
	}
	if cfg.PrivateKey != "aa" || cfg.ServerAddr != "https://snet.test" {
		t.Fatalf("top-level fields lost: %+v", cfg)
	}
}

// TestNetworkErrorSurfacedInStatus verifies a tunnel bring-up failure is
// recorded and surfaced by the status API, and cleared when the network is
// left, so the UI never shows a network as healthy-but-dead.
func TestNetworkErrorSurfacedInStatus(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.Networks = map[string]*NetworkCfg{
		"n1": {NodeID: "n1", IP: "10.0.0.1", Token: "t", Subnet: "10.0.0.0/24", Active: true},
	}
	d.netErrs["n1"] = "create tun: operation not permitted"
	d.mu.Unlock()

	st, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	nets, _ := st["networks"].([]map[string]any)
	if len(nets) != 1 {
		t.Fatalf("networks = %v", nets)
	}
	if nets[0]["active"] != true {
		t.Fatalf("active should stay true (intent kept): %v", nets[0])
	}
	if nets[0]["interface"] != "" {
		t.Fatalf("interface should be empty: %v", nets[0])
	}
	if !strings.Contains(nets[0]["error"].(string), "operation not permitted") {
		t.Fatalf("error not surfaced: %v", nets[0])
	}

	// leaving the network clears the recorded error
	d.mu.Lock()
	d.leaveLocked("n1")
	d.mu.Unlock()
	if _, ok := d.netErrs["n1"]; ok {
		t.Fatal("netErrs not cleared on leave")
	}
	st, _ = d.Status()
	nets, _ = st["networks"].([]map[string]any)
	if nets[0]["error"] != "" {
		t.Fatalf("error should be cleared after leave: %v", nets[0])
	}
}

// TestRetryScheduling verifies a failed bring-up schedules a background retry
// that is cancelled when the daemon closes.
func TestRetryScheduling(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.Networks = map[string]*NetworkCfg{
		"n1": {NodeID: "n1", IP: "10.0.0.1", Token: "t", Subnet: "10.0.0.0/24", Active: true},
	}
	d.scheduleRetryLocked("n1")
	d.mu.Unlock()

	d.mu.Lock()
	if _, ok := d.retryPending["n1"]; !ok {
		t.Fatal("n1 not scheduled")
	}
	if d.retryStop == nil {
		t.Fatal("retry loop not started")
	}
	d.mu.Unlock()

	d.Close()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.retryPending) != 0 {
		t.Fatalf("retry pending not cleared on close: %v", d.retryPending)
	}
	if d.retryStop != nil {
		t.Fatal("retry loop not stopped on close")
	}
}

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := &Config{
		ServerAddr: "https://snet.test",
		Networks: map[string]*NetworkCfg{
			"a": {NodeID: "n1", IP: "10.0.0.2", Token: "t", Subnet: "10.0.0.0/24", Active: true},
		},
	}
	if err := c.SaveAt(path); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Networks["a"].Token != "t" || c2.Networks["a"].Subnet != "10.0.0.0/24" {
		t.Fatalf("round trip wrong: %+v", c2.Networks["a"])
	}
}

// TestConfigStaleServerCleared verifies the load-time cleanup: a legacy device
// with no binding, no networks and no pending joins has no meaningful server
// address and gets it dropped.

func TestSetServerSupportsCustomAndEmptyDefault(t *testing.T) {
	d, _ := newTestDaemon(t)
	if got := d.cfg.EffectiveServerAddr(); got != constants.DefaultServer {
		t.Fatalf("empty ServerAddr effective = %q, want %q", got, constants.DefaultServer)
	}
	if err := d.SetServer("https://custom.example:8090/"); err != nil {
		t.Fatal(err)
	}
	if d.cfg.ServerAddr != "https://custom.example:8090" {
		t.Fatalf("custom server = %q", d.cfg.ServerAddr)
	}
	if got := d.serverAddrLocked(); got != "https://custom.example:8090" {
		t.Fatalf("effective custom server = %q", got)
	}
	if err := d.SetServer(""); err != nil {
		t.Fatal(err)
	}
	if d.cfg.ServerAddr != "" {
		t.Fatalf("empty server should stay empty, got %q", d.cfg.ServerAddr)
	}
	if got := d.serverAddrLocked(); got != constants.DefaultServer {
		t.Fatalf("fallback server = %q, want %q", got, constants.DefaultServer)
	}
}

func TestNormalizeServer(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"https://snet.example", "https://snet.example"},
		{"https://snet.example/", "https://snet.example"},
		{"http://127.0.0.1:8090", "http://127.0.0.1:8090"},
		{"http://127.0.0.1:8090//", "http://127.0.0.1:8090"},
		{"snet.example", "https://snet.example"},
	}
	for _, c := range cases {
		if got := normalizeServer(c.in); got != c.want {
			t.Fatalf("normalizeServer(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBindRoundTrip verifies the daemon bind flow against a server with the
// enrollment gate on: binding with an admin code unlocks create/join, and
// unbound devices are refused with the enrollment hint.
func TestSubnetsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"10.88.0.0/24", "10.88.0.0/24", true},
		{"10.88.0.0/24", "10.88.0.0/23", true},
		{"10.88.0.0/24", "10.88.1.0/24", false},
		{"10.88.0.0/24", "192.168.1.0/24", false},
	}
	for _, c := range cases {
		if got := subnetsOverlap(c.a, c.b); got != c.want {
			t.Errorf("subnetsOverlap(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCreateJoinRoundTrip(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	d, _ := newTestDaemon(t)

	// owner create
	resp, err := d.Create(ts.URL, 51820, "home", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.NetworkID == "" || resp.Token == "" || resp.Subnet == "" {
		t.Fatalf("create resp incomplete: %+v", resp)
	}
	if d.cfg.DeviceID == "" {
		t.Fatal("device id not assigned")
	}
	if !d.cfg.Networks[resp.NetworkID].Owner {
		t.Fatal("owner network not marked owner")
	}

	// second network join on the same server
	join, err := d.Join(ts.URL, 0, resp.NetworkID, resp.PairingCode)
	if err == nil {
		t.Fatalf("join own network should fail, got %+v", join)
	}
	// owner cannot create a second network (one network per device)
	if _, err := d.Create(ts.URL, 0, "office", "192.168.9.0/24", false); err == nil {
		t.Fatal("owner daemon should not be able to create a second network")
	}
	// a fresh daemon (different device) creates a second network, daemon d2 joins it
	d3, d3path := newTestDaemon(t)
	ownerResp, err := d3.Create(ts.URL, 0, "office", "192.168.9.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	if ownerResp.Subnet != "192.168.9.0/24" {
		t.Fatalf("explicit subnet ignored: %s", ownerResp.Subnet)
	}

	d2, _ := newTestDaemon(t)
	join, err = d2.Join(ts.URL, 0, ownerResp.NetworkID, ownerResp.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if join.IP == "" {
		t.Fatal("no ip assigned on join")
	}
	if d2.cfg.Networks[join.NetworkID].Owner {
		t.Fatal("non-owner marked owner")
	}

	// leave keeps the node; rejoin brings it back
	if err := d2.Leave(join.NetworkID); err != nil {
		t.Fatal(err)
	}
	if d2.cfg.Networks[join.NetworkID].Active {
		t.Fatal("leave did not deactivate")
	}
	if err := d2.Rejoin(join.NetworkID); err != nil {
		t.Fatal(err)
	}
	if !d2.cfg.Networks[join.NetworkID].Active {
		t.Fatal("rejoin did not reactivate")
	}

	// netinfo + rename + reset code via owner daemon
	info, err := d3.Info(ownerResp.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Nodes) < 1 {
		t.Fatalf("netinfo nodes: %+v", info)
	}
	if err := d3.UpdateSettings(ownerResp.NetworkID, "office-renamed", "", nil); err != nil {
		t.Fatal(err)
	}
	code, err := d3.ResetCode(ownerResp.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if code == "" {
		t.Fatal("empty reset code")
	}

	// kick the second daemon's node
	if err := d3.Kick(ownerResp.NetworkID, join.NodeID); err != nil {
		t.Fatal(err)
	}

	// remove from second daemon
	if err := d2.Remove(join.NetworkID); err != nil {
		t.Fatal(err)
	}
	if _, ok := d2.cfg.Networks[join.NetworkID]; ok {
		t.Fatal("remove left network in config")
	}

	// owner deletes its first network
	if err := d.DeleteNetwork(resp.NetworkID); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.cfg.Networks[resp.NetworkID]; ok {
		t.Fatal("delete left network in config")
	}

	// status snapshot shape
	st, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	if st["deviceId"] != d.cfg.DeviceID {
		t.Fatalf("status deviceId wrong: %v", st["deviceId"])
	}
	if _, ok := st["networks"].([]map[string]any); !ok {
		t.Fatalf("status networks type wrong: %T", st["networks"])
	}

	// persisted config reflects state
	data, err := os.ReadFile(d3path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "office-renamed") {
		t.Fatalf("config not persisted with rename: %s", data)
	}
}

// TestEmptyServerDefaultsToBound verifies create/join with an empty server
// address use the currently bound server instead of switching or failing.
func TestSwitchServerClearsNetworks(t *testing.T) {
	tsA := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer tsA.Close()
	tsB := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer tsB.Close()
	d, _ := newTestDaemon(t)
	createdA, err := d.Create(tsA.URL, 51820, "net-a", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.cfg.Networks) != 1 {
		t.Fatalf("expected one network on A: %+v", d.cfg.Networks)
	}
	// switching to B is allowed and clears A's networks
	resp, err := d.Create(tsB.URL, 0, "net-b", "", false)
	if err != nil {
		t.Fatalf("create on B: %v", err)
	}
	if len(d.cfg.Networks) != 1 || d.cfg.Networks[resp.NetworkID] == nil {
		t.Fatalf("expected only B's network after switch: %+v", d.cfg.Networks)
	}
	if _, ok := d.cfg.Networks[createdA.NetworkID]; ok {
		t.Fatalf("A's network still present after switch: %+v", d.cfg.Networks)
	}
	if d.cfg.ServerAddr != normalizeServer(tsB.URL) {
		t.Fatalf("server not switched: %+v", d.cfg)
	}
	// the owner limit applies per server: after switching, A's owned network
	// is gone, so creating again on B is rejected for a different reason
	if _, err := d.Create(tsB.URL, 0, "net-b2", "", false); err == nil || !strings.Contains(err.Error(), "已创建网络") {
		t.Fatalf("second create on B err = %v, want owner limit", err)
	}
}

// TestFailedSwitchRollsBack verifies a failed switch to a new server (e.g. the
// enrollment gate refusing an unbound device) leaves the current server's
// address and networks fully intact.
func TestCreateOneNetworkPerDevice(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	d, _ := newTestDaemon(t)
	if _, err := d.Create(ts.URL, 51820, "home", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Create(ts.URL, 0, "second", "", false); err == nil {
		t.Fatal("daemon guard: second create should be rejected")
	}
}

func TestPendingJoinFlow(t *testing.T) {
	old := pendingPollInterval
	pendingPollInterval = 50 * time.Millisecond
	defer func() { pendingPollInterval = old }()

	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()

	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "office", "10.99.0.0/24", true)
	if err != nil {
		t.Fatal(err)
	}

	joiner, _ := newTestDaemon(t)
	joined, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if joined.Status != "pending" || joined.PendingID == "" {
		t.Fatalf("join = %+v, want pending", joined)
	}
	// not attached to any network yet
	if len(joiner.cfg.Networks) != 0 {
		t.Fatalf("pending join must not attach a network: %+v", joiner.cfg.Networks)
	}
	if len(joiner.cfg.PendingJoins) != 1 {
		t.Fatalf("pending join not persisted: %+v", joiner.cfg.PendingJoins)
	}

	// owner sees the pending request
	info, err := owner.Info(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Pending) != 1 {
		t.Fatalf("owner pending count = %d", len(info.Pending))
	}
	pendingID := info.Pending[0].ID

	// owner approves; the joiner's poll loop attaches the network
	if err := owner.ApprovePending(created.NetworkID, pendingID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		joiner.mu.Lock()
		nc := joiner.cfg.Networks[created.NetworkID]
		joiner.mu.Unlock()
		if nc != nil && nc.Active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joiner never attached after approval: %+v", joiner.cfg.Networks)
		}
		time.Sleep(20 * time.Millisecond)
	}
	joiner.mu.Lock()
	nc := joiner.cfg.Networks[created.NetworkID]
	joiner.mu.Unlock()
	if !strings.HasPrefix(nc.IP, "10.99.0.") {
		t.Fatalf("approved IP out of range: %s", nc.IP)
	}
	if len(joiner.cfg.PendingJoins) != 0 {
		t.Fatalf("pending join not cleaned up: %+v", joiner.cfg.PendingJoins)
	}

	// owner can now see two members
	info, err = owner.Info(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Nodes) != 2 {
		t.Fatalf("member count after approval = %d", len(info.Nodes))
	}
}

func TestPendingJoinDenied(t *testing.T) {
	old := pendingPollInterval
	pendingPollInterval = 50 * time.Millisecond
	defer func() { pendingPollInterval = old }()

	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()

	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "office", "10.99.1.0/24", true)
	if err != nil {
		t.Fatal(err)
	}
	joiner, _ := newTestDaemon(t)
	if _, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode); err != nil {
		t.Fatal(err)
	}

	info, err := owner.Info(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Pending) != 1 {
		t.Fatalf("owner pending count = %d", len(info.Pending))
	}
	if err := owner.DenyPending(created.NetworkID, info.Pending[0].ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		joiner.mu.Lock()
		pj := joiner.cfg.PendingJoins[info.Pending[0].ID]
		status := ""
		if pj != nil {
			status = pj.Status
		}
		joiner.mu.Unlock()
		if status == "denied" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joiner never saw denial: %+v", joiner.cfg.PendingJoins)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(joiner.cfg.Networks) != 0 {
		t.Fatalf("denied join must not attach a network: %+v", joiner.cfg.Networks)
	}
}

func TestCancelPending(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "office", "10.99.2.0/24", true)
	if err != nil {
		t.Fatal(err)
	}
	joiner, _ := newTestDaemon(t)
	joined, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := joiner.CancelPending(joined.PendingID); err != nil {
		t.Fatal(err)
	}
	if len(joiner.cfg.PendingJoins) != 0 {
		t.Fatalf("cancel did not clear pending: %+v", joiner.cfg.PendingJoins)
	}
	if err := joiner.CancelPending(joined.PendingID); err == nil {
		t.Fatal("cancel of missing pending should fail")
	}
}

// TestSubnetChangeDetected verifies the poll loop rebuilds the tunnel when
// the owner re-allocates the network subnet (self IP change).
func TestSubnetChangeDetected(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "home", "10.88.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joiner, _ := newTestDaemon(t)
	joined, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if joined.IP != "10.88.0.2" {
		t.Fatalf("joiner IP = %s", joined.IP)
	}
	// The poll loop only runs when the tunnel could be created; without a TUN
	// device there is nothing to rebuild, so skip.
	joiner.mu.Lock()
	probe, tErr := NewTunnel(joiner.cfg.PrivateKey, "10.0.0.2", 51830, 1420)
	joiner.mu.Unlock()
	if tErr == nil && probe != nil {
		probe.Close()
	} else {
		t.Skip("no TUN device available in this environment")
	}

	// owner changes subnet; the joiner's poll loop should detect the new self
	// IP and re-attach (bringUp). We verify the stored IP + subnet converge.
	if err := owner.UpdateSettings(created.NetworkID, "", "192.168.60.0/24", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		joiner.mu.Lock()
		nc := joiner.cfg.Networks[created.NetworkID]
		joiner.mu.Unlock()
		if nc != nil && strings.HasPrefix(nc.IP, "192.168.60.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joiner never picked up new subnet: ip=%s subnet=%s", nc.IP, nc.Subnet)
		}
		time.Sleep(30 * time.Millisecond)
	}
	joiner.mu.Lock()
	nc := joiner.cfg.Networks[created.NetworkID]
	joiner.mu.Unlock()
	if nc.Subnet != "192.168.60.0/24" {
		t.Fatalf("subnet not updated: %s", nc.Subnet)
	}
}

// TestNetworkNameSyncedFromServer verifies the poll loop picks up a name set
// on the server (e.g. renamed by the owner) even when the joiner joined with
// an empty name, so client and server display stay consistent.
func TestNetworkNameSyncedFromServer(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()
	owner, _ := newTestDaemon(t)
	created, err := owner.Create(ts.URL, 0, "", "10.88.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joiner, _ := newTestDaemon(t)
	joined, err := joiner.Join(ts.URL, 0, created.NetworkID, created.PairingCode)
	if err != nil {
		t.Fatal(err)
	}
	if joined.IP == "" {
		t.Fatalf("joiner joined without IP")
	}
	if got := joiner.cfg.Networks[created.NetworkID].Name; got != "" {
		t.Fatalf("joiner initial name = %q, want empty", got)
	}
	// The poll loop only runs when the tunnel could be created.
	joiner.mu.Lock()
	probe, tErr := NewTunnel(joiner.cfg.PrivateKey, "10.0.0.2", 51830, 1420)
	joiner.mu.Unlock()
	if tErr == nil && probe != nil {
		probe.Close()
	} else {
		t.Skip("no TUN device available in this environment")
	}

	// owner renames the network on the server; the joiner's poll loop should
	// adopt the new name.
	if err := owner.UpdateSettings(created.NetworkID, "office-renamed", "", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		joiner.mu.Lock()
		nc := joiner.cfg.Networks[created.NetworkID]
		joiner.mu.Unlock()
		if nc != nil && nc.Name == "office-renamed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joiner never picked up renamed network: name=%q", nc.Name)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// TestAttachSubnetConflict verifies that attach refuses to join a network
// when its subnet overlaps with an already-joined network.
func TestAttachSubnetConflict(t *testing.T) {
	d, _ := newTestDaemon(t)

	// Simulate an existing network with 10.88.0.0/24
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET001": {
			Name:   "network-1",
			NodeID: "node1",
			IP:     "10.88.0.1",
			Token:  "tok1",
			Subnet: "10.88.0.0/24",
			Active: true,
		},
	}

	// Try to attach a second network with overlapping subnet
	err := d.attach("NET002", "network-2", "node2", "10.88.0.2", "tok2", "", "10.88.0.0/24", false)
	if err == nil {
		t.Fatal("attach should reject overlapping subnet")
	}
	if !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("error should mention conflict: %v", err)
	}

	// Non-overlapping subnet should succeed
	err = d.attach("NET002", "network-2", "node2", "10.88.1.1", "tok2", "", "10.88.1.0/24", false)
	if err != nil {
		t.Fatalf("attach should accept non-overlapping subnet: %v", err)
	}
}

// TestAttachSubnetConflictWithInactive verifies that attach rejects overlapping
// subnet even when the existing network is inactive.
func TestAttachSubnetConflictWithInactive(t *testing.T) {
	d, _ := newTestDaemon(t)

	// Simulate an existing but inactive network
	d.cfg.Networks = map[string]*NetworkCfg{
		"NET001": {
			Name:   "network-1",
			NodeID: "node1",
			IP:     "10.88.0.1",
			Token:  "tok1",
			Subnet: "10.88.0.0/24",
			Active: false, // inactive
		},
	}

	// Should still reject overlapping subnet even if inactive
	err := d.attach("NET002", "network-2", "node2", "10.88.0.2", "tok2", "", "10.88.0.0/24", false)
	if err == nil {
		t.Fatal("attach should reject overlapping subnet even with inactive network")
	}
}

// TestUpdateSettingsSubnetConflict verifies that UpdateSettings rejects
// changing a network's subnet when it would overlap with another network.
func TestUpdateSettingsSubnetConflict(t *testing.T) {
	d, _ := newTestDaemon(t)

	d.cfg.Networks = map[string]*NetworkCfg{
		"NET001": {
			Name:   "network-1",
			NodeID: "node1",
			IP:     "10.88.0.1",
			Token:  "tok1",
			Subnet: "10.88.0.0/24",
			Active: true,
			Owner:  true,
		},
		"NET002": {
			Name:   "network-2",
			NodeID: "node2",
			IP:     "10.88.1.1",
			Token:  "tok2",
			Subnet: "10.88.1.0/24",
			Active: true,
		},
	}

	// Try to change NET001's subnet to overlap with NET002
	err := d.UpdateSettings("NET001", "", "10.88.1.0/24", nil)
	if err == nil {
		t.Fatal("UpdateSettings should reject overlapping subnet")
	}
	if !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("error should mention conflict: %v", err)
	}
}

func TestIsVirtualIface(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"en0", false},
		{"eth0", false},
		{"en1", false},
		{"lo0", true},
		{"lo", true},
		{"utun0", true},
		{"utun5", true},
		{"wg0", true},
		{"wg-quick", true},
		{"tun0", true},
		{"tap0", true},
		{"docker0", true},
		{"br-abcdef", true},
		{"veth1234", true},
		{"virbr0", true},
	}
	for _, c := range cases {
		if got := isVirtualIface(c.name); got != c.want {
			t.Errorf("isVirtualIface(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrivateIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"192.168.0.1", true},
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"192.169.0.1", false},
		{"127.0.0.1", false},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip).To4()
		if ip == nil {
			t.Fatalf("bad IP: %s", c.ip)
		}
		if got := privateIPv4(ip); got != c.want {
			t.Errorf("privateIPv4(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestIpNetCIDR(t *testing.T) {
	cases := []struct {
		cidr string
		want string
	}{
		{"192.168.1.100/24", "192.168.1.0/24"},
		{"10.0.0.5/8", "10.0.0.0/8"},
		{"172.16.3.200/12", "172.16.0.0/12"},
	}
	for _, c := range cases {
		_, ipNet, err := net.ParseCIDR(c.cidr)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", c.cidr, err)
		}
		if got := ipNetCIDR(ipNet); got != c.want {
			t.Errorf("ipNetCIDR(%s) = %q, want %q", c.cidr, got, c.want)
		}
	}
}

func TestDetectLocalSubnets(t *testing.T) {
	d, _ := newTestDaemon(t)
	subnets := d.DetectLocalSubnets()
	// On any machine with a network interface, we should get at least one result.
	// If running in CI with no interfaces, the result may be empty.
	t.Logf("detected local subnets: %v", subnets)
	// Verify all results are valid CIDRs in private ranges
	for _, s := range subnets {
		_, _, err := net.ParseCIDR(s)
		if err != nil {
			t.Errorf("invalid CIDR %q: %v", s, err)
		}
	}
}

func TestBuildCandidates(t *testing.T) {
	// Public endpoint: observed port first, then ±1..±8 interleaved.
	got := buildCandidates("203.0.113.9:51820")
	want := []string{
		"203.0.113.9:51820",
		"203.0.113.9:51819", "203.0.113.9:51821",
		"203.0.113.9:51818", "203.0.113.9:51822",
		"203.0.113.9:51817", "203.0.113.9:51823",
		"203.0.113.9:51816", "203.0.113.9:51824",
		"203.0.113.9:51815", "203.0.113.9:51825",
		"203.0.113.9:51814", "203.0.113.9:51826",
		"203.0.113.9:51813", "203.0.113.9:51827",
		"203.0.113.9:51812", "203.0.113.9:51828",
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Private endpoints are used as-is: no probe window.
	for _, ep := range []string{"10.7.86.111:51820", "192.168.1.5:12345", "127.0.0.1:51820", "169.254.1.1:9"} {
		cands := buildCandidates(ep)
		if len(cands) != 1 || cands[0] != ep {
			t.Errorf("buildCandidates(%q) = %v, want [%q]", ep, cands, ep)
		}
	}

	// Port bounds are respected near 1 and 65535.
	low := buildCandidates("203.0.113.9:2")
	for _, c := range low {
		if _, p, err := net.SplitHostPort(c); err != nil || p == "0" || p == "-1" {
			t.Errorf("invalid low-end candidate %q", c)
		}
	}
}

// TestBuildCandidatesIPv6 verifies global IPv6 endpoints get a single
// candidate (IPv6 has no NAT, so a port window buys nothing).
func TestBuildCandidatesIPv6(t *testing.T) {
	for _, ep := range []string{"[2001:db8::1]:51820", "[2606:65c0:20:493::e3dd]:51820", "[fe80::1%en0]:51820"} {
		cands := buildCandidates(ep)
		if len(cands) != 1 || cands[0] != ep {
			t.Errorf("buildCandidates(%q) = %v, want single candidate [%q]", ep, cands, ep)
		}
	}
}

// TestBuildPeerCandidates verifies the ordered direct-candidate set: global
// IPv6 first, then the v4 endpoint, then live relay mappings (excluding our
// own public IP), then the nearby-port window, all deduplicated.
// TestResolvePeerEndpointsMapped verifies that a peer advertising a stable
// inbound gateway mapping (EndpointMapped) becomes a primary direct target
// even while its relay flow is live — the zero-config path to direct
// connectivity for a symmetric-NAT peer — and that a mapped host equal to our
// own public IP (same router) is left to the LAN path, not probed as a
// hairpin.
func TestResolvePeerEndpointsMapped(t *testing.T) {
	d, _ := newTestDaemon(t)
	mkRT := func() *netRuntime {
		return &netRuntime{peerDirect: make(map[string]*peerDirect)}
	}
	st := func(ep string, mapped bool, relayFlow string) protocol.PeersResp {
		return protocol.PeersResp{
			RelayEndpoint: "66.187.6.46:51820",
			Peers: []protocol.Node{{
				ID:             "p1",
				PublicKey:      testWGKey(t, 1),
				Endpoint:       ep,
				EndpointMapped: mapped,
				RelayFlow:      relayFlow,
				Online:         true,
			}},
		}
	}

	// Mapped peer on a remote router: its mapped endpoint leads the window
	// and becomes the target.
	rt := mkRT()
	out := d.resolvePeerEndpoints("n1", rt, st("39.180.139.102:51820", true, "66.187.6.46:53977"), nil, nil)
	if out[0].Endpoint != "39.180.139.102:51820" {
		t.Fatalf("mapped peer should target its mapped endpoint, got %s", out[0].Endpoint)
	}
	if rt.peerDirect["p1"] == nil || rt.peerDirect["p1"].cands[0] != "39.180.139.102:51820" {
		t.Fatalf("mapped endpoint must lead the candidate window: %v", rt.peerDirect["p1"].cands)
	}

	// Legacy behavior preserved: live relay flow + self-advertised endpoint
	// with no EndpointMapped flag never uses the stale pinhole — the peer
	// stays on its per-node relay flow.
	rt = mkRT()
	out = d.resolvePeerEndpoints("n1", rt, st("39.180.139.102:51820", false, "66.187.6.46:53977"), nil, nil)
	if out[0].Endpoint != "66.187.6.46:53977" {
		t.Fatalf("non-mapped peer with live relay must not use self-advertised endpoint: %s", out[0].Endpoint)
	}

	// Same router (our public IP == mapped host): never punch the hairpin;
	// the relay flow is the fallback.
	d.selfPub["n1"] = "39.180.139.102"
	rt = mkRT()
	out = d.resolvePeerEndpoints("n1", rt, st("39.180.139.102:51820", true, "66.187.6.46:53977"), nil, nil)
	if out[0].Endpoint != "66.187.6.46:53977" {
		t.Fatalf("same-router mapped host must fall back to relay, got %s", out[0].Endpoint)
	}
}

func TestBuildPeerCandidates(t *testing.T) {
	got := buildPeerCandidates(
		"203.0.113.9:51820",
		"[2001:db8::1]:51820",
		[]string{"117.147.106.140:51820", "39.171.241.117:51991", "203.0.113.9:51820"},
		"39.171.241.117", // our own public IP must be excluded
	)
	wantStart := []string{
		"[2001:db8::1]:51820",
		"203.0.113.9:51820",
		"117.147.106.140:51820",
	}
	if len(got) < len(wantStart) {
		t.Fatalf("candidates too short: %v", got)
	}
	for i := range wantStart {
		if got[i] != wantStart[i] {
			t.Fatalf("candidate[%d] = %q, want %q (full: %v)", i, got[i], wantStart[i], got)
		}
	}
	seen := make(map[string]bool)
	for _, c := range got {
		if seen[c] {
			t.Fatalf("duplicate candidate %q in %v", c, got)
		}
		if c == "39.171.241.117:51991" {
			t.Fatalf("own mapping leaked into candidates: %v", got)
		}
		seen[c] = true
	}
	// The v4 window must trail the live relay mappings.
	if got[3] != "203.0.113.9:51819" {
		t.Fatalf("candidate[3] = %q, want window start %q (full: %v)", got[3], "203.0.113.9:51819", got)
	}
}

// TestGlobalIPv6Sanity ensures globalIPv6 either finds nothing or returns a
// genuinely global, non-private endpoint — never a ULA, link-local or
// loopback address (those are invisible / unroutable across NAT-free v6).
func TestGlobalIPv6Sanity(t *testing.T) {
	ep := globalIPv6(51820)
	if ep == "" {
		return // host has no global v6; nothing to advertise
	}
	host, _, err := net.SplitHostPort(ep)
	if err != nil {
		t.Fatalf("bad endpoint %q: %v", ep, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		t.Fatalf("endpoint host %q not an IP", host)
	}
	if ip.To4() != nil {
		t.Fatalf("expected IPv6 endpoint, got %q", ep)
	}
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback() {
		t.Fatalf("globalIPv6 returned non-routable address %q", ep)
	}
}

// TestRotateKeysNoNetworks rotates the identity when there are no networks: the
// key changes, the schedule anchor is set, and no error is returned.
// testWGKey returns a format-valid WireGuard public key for tests.
func testWGKey(t *testing.T, i int) string {
	t.Helper()
	_, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	_ = i
	return pub
}

func TestRotateKeysNoNetworks(t *testing.T) {
	d, _ := newTestDaemon(t)
	oldKey := d.cfg.PrivateKey
	if err := d.RotateKeys(); err != nil {
		t.Fatalf("RotateKeys: %v", err)
	}
	if d.cfg.PrivateKey == "" || d.cfg.PrivateKey == oldKey {
		t.Fatalf("key not rotated: %q -> %q", oldKey, d.cfg.PrivateKey)
	}
	if d.cfg.LastKeyRotatedAt == 0 {
		t.Fatal("lastKeyRotatedAt not anchored")
	}
}

// TestRotateKeysAbortsOnBadToken verifies rotation is all-or-nothing: when a
// network's server exchange fails (bad token), the local key is left unchanged
// so no live network is ever broken by a half-applied rotation.
func TestRotateKeysAbortsOnBadToken(t *testing.T) {
	srv := server.NewStore()
	ts := httptest.NewServer(server.NewHandler(srv, server.Options{}))
	defer ts.Close()

	created, err := srv.CreateNetwork(testWGKey(t, 30), "device-rot", "rot", "", false)
	if err != nil {
		t.Fatal(err)
	}

	d, _ := newTestDaemon(t)
	oldKey := d.cfg.PrivateKey
	d.cfg.ServerAddr = ts.URL
	d.cfg.Networks = map[string]*NetworkCfg{
		created.NetworkID: {NodeID: "node-does-not-exist", Token: "WRONGTOKEN", Active: true},
	}
	if err := d.RotateKeys(); err == nil {
		t.Fatal("RotateKeys succeeded with a bad token, want error")
	}
	if d.cfg.PrivateKey != oldKey {
		t.Fatal("local key changed despite failed rotation")
	}
}

// TestPickRelayByRTT verifies the relay-selection policy: pick the fastest
// reachable candidate, but keep the current relay within tolerance of the best
// (no flapping), and never keep an unreachable current relay.
func TestPickRelayByRTT(t *testing.T) {
	rinkey := func(ep string) string { return "n|" + ep }
	const (
		relayA = "relay-a:51820"
		relayB = "relay-b:51820"
	)
	rtts := map[string]int64{}
	rtts["n|"+relayA] = 40
	rtts["n|"+relayB] = 10
	if got := pickRelayByRTT([]string{relayA, relayB}, "", relayB, 10, rtts, rinkey); got != relayB {
		t.Fatalf("want B (fastest), got %q", got)
	}

	// Current A (20ms) within tolerance of best B (10ms, tol=35): keep A.
	rtts["n|"+relayA] = 20
	if got := pickRelayByRTT([]string{relayA, relayB}, relayA, relayB, 10, rtts, rinkey); got != relayA {
		t.Fatalf("want A (within tolerance), got %q", got)
	}

	// Current A goes unreachable (no RTT entry): switch to best B.
	delete(rtts, "n|"+relayA)
	if got := pickRelayByRTT([]string{relayA, relayB}, relayA, relayB, 10, rtts, rinkey); got != relayB {
		t.Fatalf("want B (current unreachable), got %q", got)
	}
	rtts["n|"+relayA] = 500 // now way above tolerance: 500 > 10*1.5+20
	if got := pickRelayByRTT([]string{relayA, relayB}, relayA, relayB, 10, rtts, rinkey); got != relayB {
		t.Fatalf("want B (current much slower), got %q", got)
	}

	// Only one candidate, current unknown: nothing beats the fastest.
	if got := pickRelayByRTT([]string{relayA}, "", relayA, 40, rtts, rinkey); got != relayA {
		t.Fatalf("want A (single candidate), got %q", got)
	}
}

// TestSelectRelayProbesReachability verifies selectRelay picks a reachable
// relay over an unreachable one by actually probing whoami against a live
// relay (server.Relay) and a dead UDP endpoint.
func TestSelectRelayProbesReachability(t *testing.T) {
	srv := server.NewStore()
	_ = srv
	d, _ := newTestDaemon(t)

	// Grab a free UDP port for the live relay and bind it.
	lc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	livePort := lc.LocalAddr().(*net.UDPAddr).Port
	lc.Close()

	rl := server.NewRelay(livePort, 4)
	t.Cleanup(func() { rl.Close() })
	if err := rl.Ensure(livePort); err != nil {
		t.Fatal(err)
	}

	// A dead endpoint: a UDP port nothing is listening on.
	dead, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	deadPort := dead.LocalAddr().(*net.UDPAddr).Port
	dead.Close()

	liveEP := net.JoinHostPort("127.0.0.1", strconv.Itoa(livePort))
	deadEP := net.JoinHostPort("127.0.0.1", strconv.Itoa(deadPort))

	// Fresh selection with both candidates must prefer the live one.
	sel := d.selectRelay("net-x", []string{liveEP, deadEP})
	if sel == "" {
		t.Fatal("selectRelay returned empty with a reachable candidate")
	}
	if sel == deadEP {
		t.Fatal("selectRelay picked the unreachable candidate")
	}

	// A current dead relay must be abandoned for the live one.
	d.relayEP["net-x"] = deadEP
	d.relaySelAt["net-x"] = 0 // bypass the 30s select throttle
	sel = d.selectRelay("net-x", []string{liveEP, deadEP})
	if sel == deadEP {
		t.Fatal("selectRelay kept the dead current relay")
	}
}

// TestSelectRelayDiesAfterSelection verifies that a relay selected while alive
// is abandoned when it later stops answering whoami: the stale RTT cached from
// the healthy probe must not be mistaken for "within tolerance of the best" and
// keep the data plane pinned to a relay that just died.
func TestSelectRelayDiesAfterSelection(t *testing.T) {
	// Two live relays.
	ports := []int{}
	for i := 0; i < 2; i++ {
		lc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
		if err != nil {
			t.Fatal(err)
		}
		p := lc.LocalAddr().(*net.UDPAddr).Port
		lc.Close()
		rl := server.NewRelay(p, 4)
		defer rl.Close()
		if err := rl.Ensure(p); err != nil {
			t.Fatal(err)
		}
		ports = append(ports, p)
	}
	e0 := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0]))
	e1 := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[1]))

	d, _ := newTestDaemon(t)
	// Select with both alive. RTT is measured on loopback and is not guaranteed
	// to be equal, so capture whichever live relay wins instead of assuming
	// candidate order wins a tie.
	d.relaySelAt["net-x"] = 0
	sel := d.selectRelay("net-x", []string{e0, e1})
	if sel != e0 && sel != e1 {
		t.Fatalf("initial selection = %s, want one of %s/%s", sel, e0, e1)
	}
	if rtt, ok := d.relayRTT["net-x|"+sel]; !ok || rtt < 0 {
		t.Fatalf("expected a cached RTT for the selected relay, got %v/%v", rtt, ok)
	}

	// The selected relay dies; the other stays up. The stale cache must be
	// purged on the failed probe so the dead relay is not kept "within tolerance".
	// (Close over both; the die is simulated by pointing the selected relay at
	// a free port that answers nothing.)
	other := e0
	if sel == e0 {
		other = e1
	}
	dead, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	deadPort := dead.LocalAddr().(*net.UDPAddr).Port
	dead.Close()
	deadEP := net.JoinHostPort("127.0.0.1", strconv.Itoa(deadPort))
	d.relayEP["net-x"] = sel // keep selection pointing at the dying relay
	d.relaySelAt["net-x"] = 0
	sel = d.selectRelay("net-x", []string{deadEP, other})
	if sel != other {
		t.Fatalf("want failover to %s (relay %s died), got %s", other, deadEP, sel)
	}
	if _, ok := d.relayRTT["net-x|"+deadEP]; ok {
		t.Fatal("stale RTT for the dead relay was not purged")
	}
}

// resolveEndpointForPeer returns the relay endpoint constructed for one peer
// given the network relay endpoint and the peer's advertised per-node config,
// using the same logic path as resolvePeerEndpoints.
func resolveEndpointForPeer(relayEP string, p protocol.Node) string {
	endpoint := relayEP
	if relayHost := p.RelayHostOverride; relayHost != "" && p.RelayPort > 0 {
		endpoint = net.JoinHostPort(relayHost, strconv.Itoa(p.RelayPort))
	} else if endpoint != "" && p.RelayPort > 0 {
		if h, _, err := net.SplitHostPort(resolveEndpoint(endpoint)); err == nil {
			endpoint = net.JoinHostPort(h, strconv.Itoa(p.RelayPort))
		}
	}
	return endpoint
}

// TestResolvePeerEndpointsPerPeerRelayHost verifies that with a per-node relay
// port (Phase 3), the endpoint constructed for a peer uses that peer's own
// serving relay instance host from RelayHostOverride, falling back to the
// network relay host when the override is empty.
func TestResolvePeerEndpointsPerPeerRelayHost(t *testing.T) {
	relayEP := "relay-a.example:51820"
	cases := []struct {
		name string
		peer protocol.Node
		want string
	}{
		{name: "override-host", peer: protocol.Node{ID: "n1", RelayPort: 51822, RelayHostOverride: "relay-b.example"}, want: "relay-b.example:51822"},
		{name: "no-override", peer: protocol.Node{ID: "n2", RelayPort: 51823}, want: "relay-a.example:51823"},
		{name: "no-port-legacy", peer: protocol.Node{ID: "n3"}, want: "relay-a.example:51820"},
		{name: "override-no-port", peer: protocol.Node{ID: "n4", RelayHostOverride: "relay-c.example"}, want: "relay-a.example:51820"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveEndpointForPeer(relayEP, c.peer); got != c.want {
				t.Fatalf("endpoint=%q, want %q", got, c.want)
			}
		})
	}
}

// TestResolvePeerEndpointsDirectOnlyNoRelayFlow verifies that a peer with no
// relay mapping (RelayFlow == "") but a self-advertised v4 endpoint is treated
// as a direct-only node: relaying to it is a dead end (the server drops frames
// because the peer never opens a flow toward the relay), so its advertised
// endpoint must be surfaced as a direct target even though the relay is
// present. This is the NAS-behind-a-static-UDP-port-forward case.
func TestResolvePeerEndpointsDirectOnlyNoRelayFlow(t *testing.T) {
	d, _ := newTestDaemon(t)
	rt := &netRuntime{peerDirect: make(map[string]*peerDirect)}
	st := protocol.PeersResp{
		RelayEndpoint: "relay-a.example:51820",
		Peers: []protocol.Node{
			{ID: "p1", PublicKey: "k1", RelayPort: 51840, Endpoint: "203.0.113.9:51820", Online: true},
			{ID: "p2", PublicKey: "k2", RelayPort: 51840, Endpoint: "203.0.113.9:51820", RelayFlow: "203.0.113.9:12345", Online: true},
			{ID: "p3", PublicKey: "k3", RelayPort: 51840, Online: true},
		},
	}
	got := d.resolvePeerEndpoints("net1", rt, st, nil, nil)
	if len(got) != 3 {
		t.Fatalf("got %d peers, want 3", len(got))
	}
	// p1: no relay flow, only a self-advertised endpoint -> direct target.
	ep1 := got[0].Endpoint
	if host, _, err := net.SplitHostPort(ep1); err != nil || host != "203.0.113.9" {
		t.Fatalf("p1 endpoint=%q, want a direct candidate at host 203.0.113.9", ep1)
	}
	// p2: has a live relay mapping -> that mapping (or its port window) is the
	// direct target, not the possibly-stale advertised endpoint.
	ep2 := got[1].Endpoint
	if host, port, err := net.SplitHostPort(ep2); err != nil || host != "203.0.113.9" || port != "12345" {
		t.Fatalf("p2 endpoint=%q, want relay flow 203.0.113.9:12345", ep2)
	}
	// p3: no advertised endpoint and no relay flow -> stays on the relay.
	if got[2].Endpoint != "relay-a.example:51840" {
		t.Fatalf("p3 endpoint=%q, want relay-a.example:51840", got[2].Endpoint)
	}
}

// TestResolvePeerEndpointsFull verifies that resolvePeerEndpoints applies the
// per-peer relay host override end to end: peers with a per-node port get
// relayHost:<port> where the host is the peer's own serving instance, and the
// network relay endpoint is used for the remaining fallbacks.
func TestResolvePeerEndpointsFull(t *testing.T) {
	d, _ := newTestDaemon(t)
	rt := &netRuntime{peerDirect: make(map[string]*peerDirect)}
	st := protocol.PeersResp{
		RelayEndpoint: "relay-a.example:51820",
		Peers: []protocol.Node{
			{ID: "p1", PublicKey: "k1", RelayPort: 51824, RelayHostOverride: "relay-b.example"},
			{ID: "p2", PublicKey: "k2", RelayPort: 51824},
			{ID: "p3", PublicKey: "k3"},
		},
	}
	got := d.resolvePeerEndpoints("net1", rt, st, nil, nil)
	if len(got) != 3 {
		t.Fatalf("got %d peers, want 3", len(got))
	}
	// p1 has no LAN/local candidate and no RelayFlow, so it stays on its
	// per-peer relay endpoint (override host + its own port).
	if got[0].Endpoint != "relay-b.example:51824" {
		t.Fatalf("p1 endpoint=%q, want relay-b.example:51824", got[0].Endpoint)
	}
	// p2 shares a port number but no override -> network relay host.
	if got[1].Endpoint != "relay-a.example:51824" {
		t.Fatalf("p2 endpoint=%q, want relay-a.example:51824", got[1].Endpoint)
	}
	// p3 has no per-node port -> falls back to the network broadcast endpoint.
	if got[2].Endpoint != "relay-a.example:51820" {
		t.Fatalf("p3 endpoint=%q, want relay-a.example:51820", got[2].Endpoint)
	}
}

// TestQualityWatchdogTrip verifies the health watchdog: a relay-served peer
// whose path shows no traffic and an old handshake trips only after the
// configured number of consecutive dead batches, arming both a relay
// re-selection and a direct retry; active traffic clears the count.
func TestQualityWatchdogTrip(t *testing.T) {
	d, _ := newTestDaemon(t)
	// Relay-served peer, path goes dead.
	rt := &netRuntime{
		peerDirect: map[string]*peerDirect{
			"pk1": {mode: "relay"},
		},
	}
	now := int64(1_000_000)
	stats := map[string]PeerStats{
		"pk1": {
			RxBytes:          0,
			TxBytes:          0,
			LastHandshakeSec: now - directLockStaleSec - 1, // stale handshake
		},
	}
	last := make(map[string]PeerStats)
	// Batch 1: marks the peer dead-since, no trip yet.
	if forced := d.qualityTick("net1", rt, stats, now, last); forced {
		t.Fatal("should not trip on the first dead batch")
	}
	// Batches until just under the threshold: no trip. Batch 1 set deadSince
	// (T); with qualityDeadBatches consecutive dead samples required (i.e. the
	// (qualityDeadBatches-1)-sample gap), batches at T+interval..T+(N-2)*interval
	// must not trip yet.
	deadSince := rt.deadSince["pk1"]
	steps := int(qualityDeadBatches) - 2
	for i := 0; i < steps; i++ {
		now += int64(qualityIntervalSec)
		if forced := d.qualityTick("net1", rt, stats, now, last); forced {
			t.Fatalf("should not trip at batch %d", i+2)
		}
	}
	if got := rt.deadSince["pk1"]; got != deadSince {
		t.Fatalf("deadSince moved: %d -> %d", deadSince, got)
	}
	// The next batch crosses the deadline: must trip, arm direct retry and
	// force a fresh relay selection.
	now += int64(qualityIntervalSec)
	if forced := d.qualityTick("net1", rt, stats, now, last); !forced {
		t.Fatal("expected trip once the dead path reaches the threshold")
	}
	if rt.peerDirect["pk1"].dirSince != 0 {
		t.Fatalf("direct retry not re-armed: dirSince=%d", rt.peerDirect["pk1"].dirSince)
	}
	if d.relaySelAt["net1"] != 0 {
		t.Fatalf("relay re-selection not forced: relaySelAt=%d", d.relaySelAt["net1"])
	}
	if _, ok := rt.deadSince["pk1"]; ok {
		t.Fatalf("deadSince not cleared after trip")
	}
	// A further active measurement must not re-trip.
	activeStats := map[string]PeerStats{
		"pk1": {
			RxBytes:          4096,
			TxBytes:          0,
			LastHandshakeSec: now, // fresh handshake
		},
	}
	if forced := d.qualityTick("net1", rt, activeStats, now+int64(qualityIntervalSec), last); forced {
		t.Fatal("active path must not trip")
	}
	// Recover to relay-again: fresh handshake no longer dead.
	if forced := d.qualityTick("net1", rt, stats, now+2*int64(qualityIntervalSec), last); forced {
		t.Fatal("path that tripped must start a fresh dead window")
	}
}

// TestQualityWatchdogIgnoresDirect verifies the watchdog never trips for a
// peer currently on a direct path (that state machine owns its own staleness
// handling), and records quality metrics regardless.
func TestQualityWatchdogIgnoresDirect(t *testing.T) {
	d, _ := newTestDaemon(t)
	rt := &netRuntime{
		peerDirect: map[string]*peerDirect{
			"pkD": {mode: "direct", locked: true},
		},
	}
	now := int64(2_000_000)
	last := make(map[string]PeerStats)
	for i := 0; i < qualityDeadBatches+2; i++ {
		// Even a totally stale direct peer must never trip the watchdog.
		stale := map[string]PeerStats{
			"pkD": {
				RxBytes:          int64(i),
				TxBytes:          int64(i),
				LastHandshakeSec: now - 10_000,
			},
		}
		if forced := d.qualityTick("net1", rt, stale, now+int64(i*qualityIntervalSec), last); forced {
			t.Fatalf("direct peer must not be tripped by the watchdog (batch %d)", i)
		}
	}
	// Regardless of path mode, quality metrics are recorded for every peer.
	if q := rt.quality["pkD"]; q == nil {
		t.Fatal("quality not recorded for direct peer")
	}
}

// TestResolvePeerEndpointsQuarantinesBadDirect verifies that a direct
// candidate which previously carried no return traffic is not retried while
// the peer still advertises the same endpoint.
func TestResolvePeerEndpointsQuarantinesBadDirect(t *testing.T) {
	d, _ := newTestDaemon(t)
	const dirKey = "|39.180.139.141:52100"
	rt := &netRuntime{
		peerDirect: map[string]*peerDirect{
			"p1": {mode: "relay", tried: true, dirSince: 0, badDirect: dirKey},
		},
		peerIDs: make(map[string]string),
	}
	st := protocol.PeersResp{
		RelayEndpoint: "relay-a.example:51820",
		Peers: []protocol.Node{{
			ID:             "p1",
			PublicKey:      "k1",
			Endpoint:       "39.180.139.141:52100",
			EndpointMapped: true,
			RelayFlow:      "39.180.139.141:1538",
			RelayPort:      51840,
			Online:         true,
		}},
	}
	d.resolvePeerEndpoints("net1", rt, st, nil, nil)
	if pd := rt.peerDirect["p1"]; pd.mode != "relay" {
		t.Fatalf("quarantined direct candidate retried: mode=%q", pd.mode)
	}
}

// TestResolvePeerEndpointsQuarantinesFailedProbe verifies that a direct
// candidate which never completes a handshake is quarantined after the probe
// window instead of being re-probed every directRetrySec, which would stall
// traffic each time WireGuard points at the dead endpoint.
func TestResolvePeerEndpointsQuarantinesFailedProbe(t *testing.T) {
	d, _ := newTestDaemon(t)
	const dirKey = "|39.180.139.141:52100"
	rt := &netRuntime{
		peerDirect: map[string]*peerDirect{
			"p1": {mode: "direct", tried: true, dirSince: 0, lastDir: dirKey},
		},
		peerIDs: make(map[string]string),
	}
	st := protocol.PeersResp{
		RelayEndpoint: "relay-a.example:51820",
		Peers: []protocol.Node{{
			ID:             "p1",
			PublicKey:      "k1",
			Endpoint:       "39.180.139.141:52100",
			EndpointMapped: true,
			RelayFlow:      "39.180.139.141:1538",
			RelayPort:      51840,
			Online:         true,
		}},
	}
	d.resolvePeerEndpoints("net1", rt, st, nil, nil)
	pd := rt.peerDirect["p1"]
	if pd.mode != "relay" || pd.badDirect != dirKey {
		t.Fatalf("failed direct probe not quarantined: mode=%q badDirect=%q", pd.mode, pd.badDirect)
	}
}

// TestQualityWatchdogDirectOneWayFallsBack verifies that a direct path which
// passes handshakes but carries no return traffic falls back to the relay
// after the watchdog threshold, instead of staying locked forever.
func TestQualityWatchdogDirectOneWayFallsBack(t *testing.T) {
	d, _ := newTestDaemon(t)
	rt := &netRuntime{
		peerDirect: map[string]*peerDirect{
			"peer-1": {mode: "direct", locked: true, lastDir: "|39.180.139.141:52100"},
		},
		peerIDs: map[string]string{"pk1": "peer-1"},
	}
	now := int64(3_000_000)
	last := make(map[string]PeerStats)
	for i := 0; i < qualityDeadBatches+1; i++ {
		stats := map[string]PeerStats{
			"pk1": {
				RxBytes:          0,
				TxBytes:          int64(directDeadMinBytes * (i + 1)),
				LastHandshakeSec: now + int64(i*qualityIntervalSec),
			},
		}
		if forced := d.qualityTick("net1", rt, stats, now+int64(i*qualityIntervalSec), last); forced {
			t.Fatalf("direct fallback must not force a relay re-select (batch %d)", i)
		}
	}
	pd := rt.peerDirect["peer-1"]
	if pd.mode != "relay" {
		t.Fatalf("one-way direct path not demoted: mode=%q", pd.mode)
	}
	if pd.locked || len(pd.cands) != 0 || !pd.tried {
		t.Fatalf("direct state not reset: locked=%v cands=%v tried=%v", pd.locked, pd.cands, pd.tried)
	}
	if pd.badDirect != "|39.180.139.141:52100" {
		t.Fatalf("bad direct candidate not quarantined: %q", pd.badDirect)
	}
}

// TestPingServerStateRetriesRotatedToken ensures a 404 from a stale node token
// (device sync rotates tokens) does not mark a live network as deleted.
func TestPingServerStateRetriesRotatedToken(t *testing.T) {
	oldStarted := make(chan struct{}, 1)
	releaseOld := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/networks/n1/exists" {
			http.NotFound(w, r)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		switch token {
		case "old":
			select {
			case oldStarted <- struct{}{}:
			default:
			}
			<-releaseOld
			w.WriteHeader(http.StatusNotFound)
		case "new":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"exists":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.ServerAddr = ts.URL
	d.cfg.Networks = map[string]*NetworkCfg{
		"n1": {NodeID: "node1", IP: "10.88.1.5", Token: "old", Subnet: "10.88.1.0/24", Active: true},
	}
	d.mu.Unlock()

	done := make(chan struct{})
	go func() {
		d.pingServerState("n1")
		close(done)
	}()
	<-oldStarted

	d.mu.Lock()
	d.cfg.Networks["n1"].Token = "new"
	delete(d.serverState, "n1")
	d.mu.Unlock()
	close(releaseOld)
	<-done

	d.mu.Lock()
	state := d.serverState["n1"]
	d.mu.Unlock()
	if state == nil || *state != "ok" {
		t.Fatalf("serverState = %v, want ok", state)
	}
}

// TestPingServerStateRemovesRevokedNetwork ensures a deleted network or a
// revoked node token does not leave a stale tab or block the owner limit.
func TestPingServerStateRemovesRevokedNetwork(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.ServerAddr = ts.URL
	d.cfg.Networks = map[string]*NetworkCfg{
		"n1": {NodeID: "node1", IP: "10.88.1.5", Token: "revoked", Subnet: "10.88.1.0/24", Active: true},
	}
	d.mu.Unlock()

	d.pingServerState("n1")

	d.mu.Lock()
	_, exists := d.cfg.Networks["n1"]
	_, stateExists := d.serverState["n1"]
	d.mu.Unlock()
	if exists || stateExists {
		t.Fatalf("revoked network was not removed: cfg=%v state=%v", exists, stateExists)
	}
}

func TestSetRoutesDistinguishesMissingEndpointFromDeletedNetwork(t *testing.T) {
	tests := []struct {
		name       string
		infoStatus int
		want       string
	}{
		{name: "old server without routes endpoint", infoStatus: http.StatusOK, want: "服务端不支持该功能"},
		{name: "network deleted", infoStatus: http.StatusNotFound, want: netGoneErr.Error()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut && r.URL.Path == "/api/v1/networks/n1/routes" {
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/api/v1/networks/n1" {
					w.WriteHeader(tc.infoStatus)
					if tc.infoStatus == http.StatusOK {
						_, _ = w.Write([]byte(`{}`))
					}
					return
				}
				http.NotFound(w, r)
			}))
			defer ts.Close()

			d, _ := newTestDaemon(t)
			d.mu.Lock()
			d.cfg.ServerAddr = ts.URL
			d.cfg.Networks = map[string]*NetworkCfg{
				"n1": {NodeID: "node1", Token: "tok", Subnet: "10.88.1.0/24", Owner: true},
			}
			d.mu.Unlock()

			err := d.SetRoutes("n1", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SetRoutes error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestSyncNetworksRefreshesExistingToken covers the reinstall/startup path:
// DeviceNetworkDetails rotates the node token, and the client must persist it
// even when the network itself is already in the local config.
func TestLocalEndpointOverride(t *testing.T) {
	d, _ := newTestDaemon(t)
	t.Setenv("SNET_LOCAL_IP", "10.7.86.222")
	got, err := d.localEndpointLocked(52100)
	if err != nil {
		t.Fatalf("localEndpointLocked: %v", err)
	}
	if want := "10.7.86.222:52100"; got != want {
		t.Fatalf("local endpoint = %q, want %q", got, want)
	}
}

func TestResolvePeerEndpointsSkipsSameEgressPublicFallback(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.selfPub["net1"] = "203.0.113.9"
	rt := &netRuntime{peerDirect: make(map[string]*peerDirect)}
	st := protocol.PeersResp{
		RelayEndpoint: "relay-a.example:51820",
		Peers: []protocol.Node{
			{ID: "p1", PublicKey: "k1", RelayPort: 51840, Endpoint: "203.0.113.9:52100", LocalEndpoint: "10.7.86.222:52100", Online: true},
		},
	}
	got := d.resolvePeerEndpoints("net1", rt, st, nil, nil)
	if got[0].Endpoint != "relay-a.example:51840" {
		t.Fatalf("endpoint=%q, want relay-a.example:51840", got[0].Endpoint)
	}
}

// TestSyncNetworksRemovesDeletedLocalNetwork ensures a cached network that the
// server no longer returns is removed instead of becoming a permanent ghost.

// TestReinstallWithNewDeviceIDCanCreate reproduces the reported bug: the user
// uninstalls without cleaning data, the daemon fails to keep its identity, and
// the next create is rejected by the server's one-network-per-device rule.
// With a genuinely new random ID the server must accept the new network.
func TestReinstallWithNewDeviceIDCanCreate(t *testing.T) {
	ts := httptest.NewServer(server.NewHandler(server.NewStore(), server.Options{}))
	defer ts.Close()

	oldDaemon, _ := newTestDaemon(t)
	if _, err := oldDaemon.Create(ts.URL, 0, "before-reinstall", "", false); err != nil {
		t.Fatal(err)
	}
	oldID := oldDaemon.cfg.DeviceID

	// A reinstall starts from an empty config and no device.id file.
	newDaemon, _ := newTestDaemon(t)
	resp, err := newDaemon.Create(ts.URL, 0, "after-reinstall", "", false)
	if err != nil {
		t.Fatalf("create after reinstall: %v", err)
	}
	if newDaemon.cfg.DeviceID == oldID {
		t.Fatalf("reinstall reused device id %q", oldID)
	}
	if resp.NetworkID == "" {
		t.Fatal("no network created after reinstall")
	}
}

// TestSyncNetworksRestoresConfig covers the path where device.id survived but
// the local config was lost: the daemon pulls its memberships back from the
// server and persists the rotated node tokens.
func TestSyncNetworksRestoresConfig(t *testing.T) {
	const (
		deviceID = "device-sync-0001"
		nodeID   = "node-sync-0001"
		nid      = "network-sync-01"
		token1   = "node-token-1111"
		token2   = "node-token-2222"
	)
	var token = token1
	var sawRecovery bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/devices":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/publickey":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/device":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/networks/"+nid+"/claim":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/devices/"+deviceID+"/networks":
			sawRecovery = r.Header.Get("X-Device-Token") == "recovery-token"
			_ = json.NewEncoder(w).Encode(protocol.DeviceNetworksResp{Networks: []protocol.DeviceNetworkDetail{{
				Network: protocol.Network{
					ID: nid, Name: "home", Subnet: "10.88.0.0/24", OwnerDeviceID: deviceID,
				},
				NodeID: nodeID, IP: "10.88.0.2", Token: token, Owner: true,
				PublicKey: "old-public-key",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	d, _ := newTestDaemon(t)
	if err := d.ensureKeys(ts.URL, 0); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.cfg.DeviceID = deviceID
	d.cfg.RecoveryToken = "recovery-token"
	d.mu.Unlock()

	if err := d.SyncNetworks(); err != nil {
		t.Fatal(err)
	}
	if !sawRecovery {
		t.Fatal("recovery request did not carry the recovery token")
	}
	d.mu.Lock()
	nc := d.cfg.Networks[nid]
	d.mu.Unlock()
	if nc == nil {
		t.Fatal("network was not restored")
	}
	if nc.NodeID != nodeID || nc.IP != "10.88.0.2" || nc.Token != token1 || !nc.Owner {
		t.Fatalf("restored network = %+v", nc)
	}

	// A later sync rotates the token; the existing config must adopt it.
	token = token2
	if err := d.SyncNetworks(); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	got := d.cfg.Networks[nid].Token
	d.mu.Unlock()
	if got != token2 {
		t.Fatalf("rotated token not persisted: %q", got)
	}
}

// TestDeviceIDSurvivesConfigLossAndRestart verifies the on-disk identity is
// authoritative: a missing/stale config must not create a new device ID.
func TestDeviceIDSurvivesConfigLossAndRestart(t *testing.T) {
	dir := t.TempDir()
	idFile := filepath.Join(dir, "ProgramData", "SNET", "device.id")
	cfgPath := filepath.Join(dir, "config.json")

	first, err := LoadConfigAt(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	d1 := NewDaemonAt(first, cfgPath)
	d1.SetDeviceIDFile(idFile)
	if err := d1.ensureKeys("https://snet.invalid", 0); err != nil {
		t.Fatal(err)
	}
	wantID := d1.cfg.DeviceID
	firstKey := d1.cfg.PrivateKey

	// Reinstall: config.json is gone, but ProgramData/SNET/device.id remains.
	if err := os.Remove(cfgPath); err != nil {
		t.Fatal(err)
	}
	second, err := LoadConfigAt(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	d2 := NewDaemonAt(second, cfgPath)
	d2.SetDeviceIDFile(idFile)
	if err := d2.ensureKeys("https://snet.invalid", 0); err != nil {
		t.Fatal(err)
	}
	if d2.cfg.DeviceID != wantID {
		t.Fatalf("device id changed after config loss: %q -> %q", wantID, d2.cfg.DeviceID)
	}
	if d2.cfg.PrivateKey != firstKey {
		t.Fatal("private key changed after config loss")
	}
	persisted, _, err := LoadDeviceKeypair(idFile)
	if err != nil {
		t.Fatal(err)
	}
	if persisted != wantID {
		t.Fatalf("persisted id = %q, want %q", persisted, wantID)
	}
}

// TestInfoClearsStaleOwnerAfterTransfer verifies that an ownership transfer in
// the admin console is reflected locally even when the cached Owner flag is
// still true.
func TestInfoClearsStaleOwnerAfterTransfer(t *testing.T) {
	const deviceID = "stale-owner-device"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/networks/n1/member-info" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(protocol.NetworkInfoResp{
				Network: protocol.Network{ID: "n1", Name: "HomeNET", OwnerDeviceID: "new-owner-device"},
				Nodes: []protocol.Node{
					{ID: "n1", DeviceID: deviceID, Role: "member"},
					{ID: "n2", DeviceID: "new-owner-device", Role: "owner"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.ServerAddr = ts.URL
	d.cfg.DeviceID = deviceID
	d.cfg.Networks = map[string]*NetworkCfg{
		"n1": {NodeID: "n1", Token: "tok", Subnet: "10.88.0.0/24", Owner: true},
	}
	d.mu.Unlock()

	if _, err := d.Info("n1"); err != nil {
		t.Fatalf("Info: %v", err)
	}
	d.mu.Lock()
	owner := d.cfg.Networks["n1"].Owner
	d.mu.Unlock()
	if owner {
		t.Fatal("stale owner flag was not cleared after admin transfer")
	}
}

func TestSyncNetworksOnlyRemovesOnConfirmed404(t *testing.T) {
	const (
		deviceID = "device-sync-safe-01"
		nid      = "net-safe-01"
	)
	listStatus := http.StatusBadGateway
	existsStatus := http.StatusBadGateway

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/devices":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/networks/"+nid+"/nodes/node-1/device":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/devices/"+deviceID+"/networks":
			if listStatus != http.StatusOK {
				w.WriteHeader(listStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(protocol.DeviceNetworksResp{Networks: nil})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/networks/"+nid+"/exists":
			if existsStatus != http.StatusOK {
				w.WriteHeader(existsStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"exists": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	d, _ := newTestDaemon(t)
	if err := d.ensureKeys(ts.URL, 0); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.cfg.ServerAddr = ts.URL
	d.cfg.DeviceID = deviceID
	d.cfg.RecoveryToken = "rec-tok"
	d.cfg.Networks = map[string]*NetworkCfg{
		nid: {Name: "HomeNET", NodeID: "node-1", Token: "tok-1", Subnet: "10.88.0.0/24"},
	}
	d.mu.Unlock()

	// 1. 502 on DeviceNetworks must not clear local networks.
	if err := d.SyncNetworks(); err == nil {
		t.Fatal("expected error on 502 DeviceNetworks")
	}
	if d.cfg.Networks[nid] == nil {
		t.Fatal("local network was removed on 502 DeviceNetworks")
	}

	// 2. Empty DeviceNetworks + 502 on NetworkExists must not clear local networks.
	listStatus = http.StatusOK
	existsStatus = http.StatusBadGateway
	if err := d.SyncNetworks(); err != nil {
		t.Fatalf("SyncNetworks: %v", err)
	}
	if d.cfg.Networks[nid] == nil {
		t.Fatal("local network was removed when NetworkExists returned 502")
	}

	// 3. Empty DeviceNetworks + 200 OK on NetworkExists must keep local network.
	existsStatus = http.StatusOK
	if err := d.SyncNetworks(); err != nil {
		t.Fatalf("SyncNetworks: %v", err)
	}
	if d.cfg.Networks[nid] == nil {
		t.Fatal("local network was removed when NetworkExists returned 200 OK")
	}

	// 4. Empty DeviceNetworks + explicit 404 Not Found on NetworkExists removes it.
	existsStatus = http.StatusNotFound
	if err := d.SyncNetworks(); err != nil {
		t.Fatalf("SyncNetworks: %v", err)
	}
	if d.cfg.Networks[nid] != nil {
		t.Fatal("local network was not removed after confirmed 404 Not Found")
	}
}

func TestReconcileOwnerRoleUpdatesCachedOwner(t *testing.T) {
	d, _ := newTestDaemon(t)
	d.mu.Lock()
	d.cfg.Networks = map[string]*NetworkCfg{
		"n1": {NodeID: "node-1", Token: "tok", Subnet: "10.88.0.0/24", Owner: false},
	}
	d.mu.Unlock()

	d.reconcileOwnerRole("n1", "owner")
	d.mu.RLock()
	owner := d.cfg.Networks["n1"].Owner
	d.mu.RUnlock()
	if !owner {
		t.Fatal("owner role was not applied to the cached network")
	}

	d.reconcileOwnerRole("n1", "member")
	d.mu.RLock()
	owner = d.cfg.Networks["n1"].Owner
	d.mu.RUnlock()
	if owner {
		t.Fatal("member role did not clear the cached owner flag")
	}

	d.reconcileOwnerRole("n1", "")
	d.mu.RLock()
	owner = d.cfg.Networks["n1"].Owner
	d.mu.RUnlock()
	if owner {
		t.Fatal("empty role must not change the cached owner flag")
	}
}

func TestSyncNetworksReconcilesExistingOwnerFlag(t *testing.T) {
	const (
		deviceID = "device-owner-sync"
		nid      = "net-owner-sync"
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/devices":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/devices/"+deviceID+"/networks":
			_ = json.NewEncoder(w).Encode(protocol.DeviceNetworksResp{Networks: []protocol.DeviceNetworkDetail{{
				Network: protocol.Network{ID: nid, Name: "HomeNET", Subnet: "10.88.0.0/24"},
				NodeID:  "node-1", IP: "10.88.0.75", Token: "tok-1", Owner: true,
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	d, _ := newTestDaemon(t)
	if err := d.ensureKeys(ts.URL, 0); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.cfg.ServerAddr = ts.URL
	d.cfg.DeviceID = deviceID
	d.cfg.RecoveryToken = "rec-tok"
	d.cfg.Networks = map[string]*NetworkCfg{
		nid: {Name: "HomeNET", NodeID: "node-1", Token: "tok-1", Subnet: "10.88.0.0/24", Active: false, Owner: false},
	}
	d.mu.Unlock()

	if err := d.SyncNetworks(); err != nil {
		t.Fatalf("SyncNetworks: %v", err)
	}
	d.mu.RLock()
	owner := d.cfg.Networks[nid].Owner
	d.mu.RUnlock()
	if !owner {
		t.Fatal("existing network owner flag was not refreshed from server")
	}
}
