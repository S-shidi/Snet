package client

import (
	"os"
	"path/filepath"
	"testing"

	"snet/internal/constants"
)

func TestLegacyConfigMigratesSnetServFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"serverAddr": "https://legacy.example:8090/",
		"recoveryToken": "old-recovery-token",
		"networks": {
			"NET1": {
				"nodeId": "node-1",
				"ip": "10.88.0.2",
				"token": "node-token",
				"active": true
			}
		},
		"pendingJoins": {
			"PENDING1": {
				"pendingId": "PENDING1",
				"networkId": "NET1",
				"status": "pending"
			}
		}
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", cfg.SchemaVersion)
	}
	if cfg.ServerAddr != "https://legacy.example:8090/" {
		t.Fatalf("ServerAddr = %q", cfg.ServerAddr)
	}
	wantServer := "https://legacy.example:8090"
	if cfg.DefaultSnetServURL != wantServer {
		t.Fatalf("DefaultSnetServURL = %q, want %q", cfg.DefaultSnetServURL, wantServer)
	}
	if got := cfg.Networks["NET1"].SnetServURL; got != wantServer {
		t.Fatalf("network SnetServURL = %q, want %q", got, wantServer)
	}
	if got := cfg.PendingJoins["PENDING1"].SnetServURL; got != wantServer {
		t.Fatalf("pending SnetServURL = %q, want %q", got, wantServer)
	}
	if got := cfg.RecoveryTokens[wantServer]; got != "old-recovery-token" {
		t.Fatalf("RecoveryTokens[%q] = %q", wantServer, got)
	}
	if cfg.RecoveryToken != "old-recovery-token" {
		t.Fatalf("legacy RecoveryToken = %q", cfg.RecoveryToken)
	}
}

func TestLegacyConfigWithoutServerUsesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"networks": {
			"NET1": {"nodeId": "node-1", "token": "node-token", "active": true}
		}
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultSnetServURL != "" {
		t.Fatalf("DefaultSnetServURL = %q, want empty for legacy config without ServerAddr", cfg.DefaultSnetServURL)
	}
	if got := cfg.Networks["NET1"].SnetServURL; got != constants.DefaultServer {
		t.Fatalf("network SnetServURL = %q, want %q", got, constants.DefaultServer)
	}
}

func TestLegacyNetworkUsesDefaultSnetServURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"defaultSnetServUrl": "https://default.example:8090/",
		"networks": {"NET1": {"nodeId": "node-1", "token": "tok", "active": true}}
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Networks["NET1"].SnetServURL; got != "https://default.example:8090" {
		t.Fatalf("network SnetServURL = %q", got)
	}
}

func TestConfigMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"serverAddr": "https://legacy.example:8090",
		"recoveryToken": "old-token",
		"networks": {"NET1": {"nodeId": "node-1", "token": "tok", "active": true}}
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SaveAt(path); err != nil {
		t.Fatal(err)
	}
	second, err := LoadConfigAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if second.SchemaVersion != 1 ||
		second.DefaultSnetServURL != "https://legacy.example:8090" ||
		second.Networks["NET1"].SnetServURL != "https://legacy.example:8090" {
		t.Fatalf("migration not stable: %+v", second)
	}
	if second.RecoveryTokens["https://legacy.example:8090"] != "old-token" {
		t.Fatalf("RecoveryTokens = %#v", second.RecoveryTokens)
	}
}
