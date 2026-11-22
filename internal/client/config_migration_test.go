package client

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/S-shidi/snet/shared/constants"
)

func TestLegacyGlobalServerFieldsAreIgnoredAndRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"serverAddr": "https://legacy.example:8090/",
		"defaultSnetServUrl": "https://default.example:8090/",
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
	if got := cfg.Networks["NET1"].SnetServURL; got != constants.DefaultServer {
		t.Fatalf("network SnetServURL = %q, want %q", got, constants.DefaultServer)
	}
	if got := cfg.PendingJoins["PENDING1"].SnetServURL; got != constants.DefaultServer {
		t.Fatalf("pending SnetServURL = %q, want %q", got, constants.DefaultServer)
	}
	if got := cfg.RecoveryTokens[constants.DefaultServer]; got != "old-recovery-token" {
		t.Fatalf("RecoveryTokens[%q] = %q", constants.DefaultServer, got)
	}
	if cfg.RecoveryToken != "old-recovery-token" {
		t.Fatalf("legacy RecoveryToken = %q", cfg.RecoveryToken)
	}
	if err := cfg.SaveAt(path); err != nil {
		t.Fatal(err)
	}
	cleaned, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"serverAddr", "defaultSnetServUrl"} {
		if strings.Contains(string(cleaned), key) {
			t.Fatalf("saved config still contains legacy field %q: %s", key, cleaned)
		}
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
	if got := cfg.Networks["NET1"].SnetServURL; got != constants.DefaultServer {
		t.Fatalf("network SnetServURL = %q, want %q", got, constants.DefaultServer)
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
		second.Networks["NET1"].SnetServURL != constants.DefaultServer {
		t.Fatalf("migration not stable: %+v", second)
	}
	if second.RecoveryTokens[constants.DefaultServer] != "old-token" {
		t.Fatalf("RecoveryTokens = %#v", second.RecoveryTokens)
	}
}
