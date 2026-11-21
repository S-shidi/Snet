package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"snet/internal/protocol"
)

func TestEnsureServerIDGeneratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snet.db")

	s1, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	id1, err := s1.EnsureServerID("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id1, "srv_") || len(id1) != len("srv_")+16 {
		t.Fatalf("server id = %q, want srv_<16 hex>", id1)
	}
	if got := s1.ServerID(); got != id1 {
		t.Fatalf("ServerID() = %q, want %q", got, id1)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	id2, err := s2.EnsureServerID("")
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id1 {
		t.Fatalf("server id after restart = %q, want stable %q", id2, id1)
	}
}

func TestEnsureServerIDExplicitValueWins(t *testing.T) {
	s, err := NewStoreAt(filepath.Join(t.TempDir(), "snet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.EnsureServerID("srv_explicit000000")
	if err != nil {
		t.Fatal(err)
	}
	if id != "srv_explicit000000" {
		t.Fatalf("server id = %q", id)
	}
	if got := s.ServerID(); got != id {
		t.Fatalf("ServerID() = %q, want %q", got, id)
	}
}

func TestCreateNetworkStampsSnetServIdentity(t *testing.T) {
	s, err := NewStoreAt("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.EnsureServerID("")
	if err != nil {
		t.Fatal(err)
	}
	const publicURL = "https://snet.example:8090"
	s.SetSnetServURL(publicURL)

	created, err := s.CreateNetwork(testKey(100), "dev-phase0", "phase0", "", false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.AdminNetworkInfo(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if info.SnetServID != id {
		t.Fatalf("SnetServID = %q, want %q", info.SnetServID, id)
	}
	if info.SnetServURL != publicURL {
		t.Fatalf("SnetServURL = %q, want %q", info.SnetServURL, publicURL)
	}
}

func TestBackfillNetworkIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snet.db")

	// Seed a legacy record: no server identity yet.
	s1, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s1.CreateNetwork(testKey(101), "dev-legacy", "legacy", "", false)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s1.AdminNetworkInfo(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SnetServID != "" || legacy.SnetServURL != "" {
		t.Fatalf("legacy identity = %q/%q, want empty", legacy.SnetServID, legacy.SnetServURL)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart, resolve identity, then backfill.
	s2, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s2.EnsureServerID("")
	if err != nil {
		t.Fatal(err)
	}
	const publicURL = "https://snet.example:8090"
	s2.SetSnetServURL(publicURL)
	changed, err := s2.BackfillNetworkIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if changed != 1 {
		t.Fatalf("backfilled %d networks, want 1", changed)
	}
	info, err := s2.AdminNetworkInfo(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if info.SnetServID != id || info.SnetServURL != publicURL {
		t.Fatalf("backfilled identity = %q/%q, want %q/%q",
			info.SnetServID, info.SnetServURL, id, publicURL)
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}

	// The backfill must survive a restart.
	s3, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	info, err = s3.AdminNetworkInfo(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if info.SnetServID != id || info.SnetServURL != publicURL {
		t.Fatalf("persisted identity = %q/%q, want %q/%q",
			info.SnetServID, info.SnetServURL, id, publicURL)
	}
}

func TestInternalAdminHealth(t *testing.T) {
	s, err := NewStoreAt("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.EnsureServerID("")
	if err != nil {
		t.Fatal(err)
	}
	const publicURL = "https://snet.example:8090"
	s.SetSnetServURL(publicURL)
	if _, err := s.AdminCreateNetwork("health", "", false); err != nil {
		t.Fatal(err)
	}

	h := NewInternalAdminHandler(s, Options{}, "service-token")
	req := httptest.NewRequest(http.MethodGet, "/internal/admin/health", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Internal-Admin-Token", "service-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	var got protocol.SnetServHealth
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ServerID != id {
		t.Fatalf("serverId = %q, want %q", got.ServerID, id)
	}
	if got.SnetServURL != publicURL {
		t.Fatalf("snetServUrl = %q, want %q", got.SnetServURL, publicURL)
	}
	if got.APIVersion != protocol.APIVersion {
		t.Fatalf("apiVersion = %d, want %d", got.APIVersion, protocol.APIVersion)
	}
	if got.StartedAt == 0 {
		t.Fatal("startedAt is zero")
	}
	if got.Networks != 1 {
		t.Fatalf("networks = %d, want 1", got.Networks)
	}
	if got.Version == "" {
		t.Fatal("version is empty")
	}
}

func TestInternalAdminHealthRequiresToken(t *testing.T) {
	s, err := NewStoreAt("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	h := NewInternalAdminHandler(s, Options{}, "service-token")
	req := httptest.NewRequest(http.MethodGet, "/internal/admin/health", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}
