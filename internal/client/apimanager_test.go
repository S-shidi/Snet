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
}

func TestDaemonAPILockedUsesAPIManager(t *testing.T) {
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
	got := d.apiLocked()
	want := d.apiManager.Get(cfg.EffectiveServerAddr())
	if got != want {
		t.Fatal("apiLocked did not use the APIManager client")
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
