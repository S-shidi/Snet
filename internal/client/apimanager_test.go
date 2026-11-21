package client

import (
	"context"
	"testing"
)

func TestAPIManagerCachesByNormalizedServer(t *testing.T) {
	m := NewAPIManager(context.Background())
	a := m.Get("https://api.example:8090/")
	b := m.Get("https://api.example:8090")
	if a == nil || b == nil {
		t.Fatal("APIManager returned nil client")
	}
	if a != b {
		t.Fatal("same normalized server should reuse the cached client")
	}
	if got := a.server; got != "https://api.example:8090" {
		t.Fatalf("client server = %q", got)
	}
	other := m.Get("https://other.example:8090")
	if other == a {
		t.Fatal("different server must not reuse the client")
	}
	if m.Get("") != nil {
		t.Fatal("empty server must not fall back to the official server")
	}
}

func TestDaemonAPIClientUsesAPIManager(t *testing.T) {
	cfg := &Config{
		ServerAddr: "https://legacy.example:8090/",
		Networks: map[string]*NetworkCfg{
			"NET1": {NodeID: "node-1", Token: "tok", Active: true},
		},
	}
	migrateConfig(cfg)
	d := NewDaemonAt(cfg, "")
	if d.apiManager == nil {
		t.Fatal("NewDaemonAt did not initialize APIManager")
	}
	got, err := d.apiForServerLocked("https://legacy.example:8090/")
	if err != nil {
		t.Fatal(err)
	}
	want := d.apiManager.Get("https://legacy.example:8090")
	if got != want {
		t.Fatal("apiForServerLocked did not use the APIManager client")
	}
	if _, err := d.apiForServerLocked(""); err == nil {
		t.Fatal("apiForServerLocked must reject an empty server")
	}
}

func TestAPIManagerWithMixedMigratedNetworkConfigs(t *testing.T) {
	cfg := &Config{
		ServerAddr: "https://legacy.example:8090",
		Networks: map[string]*NetworkCfg{
			"NEW": {NodeID: "node-new", Token: "tok-new", Active: true, SnetServURL: "https://new.example:8090"},
			"OLD": {NodeID: "node-old", Token: "tok-old", Active: true},
		},
	}
	migrateConfig(cfg)
	if cfg.Networks["NEW"].SnetServURL != "https://new.example:8090" {
		t.Fatalf("explicit network server changed: %q", cfg.Networks["NEW"].SnetServURL)
	}
	if cfg.Networks["OLD"].SnetServURL != "https://legacy.example:8090" {
		t.Fatalf("legacy network server = %q", cfg.Networks["OLD"].SnetServURL)
	}
	m := NewAPIManager(context.Background())
	if m.Get(cfg.Networks["NEW"].SnetServURL) == m.Get(cfg.Networks["OLD"].SnetServURL) {
		t.Fatal("mixed networks on different servers reused one API client")
	}
}
