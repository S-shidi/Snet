package client

import (
	"context"
	"testing"
	"time"

	"github.com/S-shidi/snet/shared/protocol"
)

func TestPathManagerStatusRelayFallbackAfterTimeout(t *testing.T) {
	pm := NewPathManager(context.Background(), "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9")
	defer pm.Close()
	key := protocol.RelayNodeKey("node-b")
	pm.mu.Lock()
	pm.peerKeys[key] = "node-b"
	pm.relayLinks = []*relayLink{{endpoint: "relay.example:8092", ready: true}}
	pm.directPeers[key] = &directPeer{
		v6Addr:    mustPathAddr(t, "[2001:db8::1]:51820"),
		pathSince: time.Now().Add(-31 * time.Second),
	}
	pm.mu.Unlock()

	entry := peerDirectEntry(t, pm, "node-b")
	if entry["path"] != "relay" {
		t.Fatalf("path = %v, want relay after timeout with ready relay", entry["path"])
	}
}

func TestPathManagerStatusRelayFallbackRequiresReadyRelay(t *testing.T) {
	pm := NewPathManager(context.Background(), "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9")
	defer pm.Close()
	key := protocol.RelayNodeKey("node-b")
	pm.mu.Lock()
	pm.peerKeys[key] = "node-b"
	pm.directPeers[key] = &directPeer{
		v6Addr:    mustPathAddr(t, "[2001:db8::1]:51820"),
		pathSince: time.Now().Add(-31 * time.Second),
	}
	pm.mu.Unlock()

	entry := peerDirectEntry(t, pm, "node-b")
	if entry["path"] != "discovering" {
		t.Fatalf("path = %v, want discovering without a ready relay", entry["path"])
	}
}

func TestPathManagerStatusDirectIsNotOverriddenByTimeout(t *testing.T) {
	pm := NewPathManager(context.Background(), "net-1", &NetworkCfg{NodeID: "node-a", Token: "tok-a", Port: 1}, "127.0.0.1:9")
	defer pm.Close()
	key := protocol.RelayNodeKey("node-b")
	addr := mustPathAddr(t, "192.168.1.10:51820")
	pm.mu.Lock()
	pm.peerKeys[key] = "node-b"
	pm.relayLinks = []*relayLink{{endpoint: "relay.example:8092", ready: true}}
	pm.directPeers[key] = &directPeer{
		addr:      addr,
		lanAddr:   addr,
		kind:      "lan",
		ready:     true,
		pathSince: time.Now().Add(-time.Minute),
	}
	pm.mu.Unlock()

	entry := peerDirectEntry(t, pm, "node-b")
	if entry["path"] != "direct" {
		t.Fatalf("path = %v, want direct", entry["path"])
	}
}

func peerDirectEntry(t *testing.T, pm *PathManager, id string) map[string]any {
	t.Helper()
	status := pm.Status()
	peerDirect, ok := status["peerDirect"].(map[string]any)
	if !ok {
		t.Fatalf("peerDirect missing: %+v", status)
	}
	entry, ok := peerDirect[id].(map[string]any)
	if !ok {
		t.Fatalf("peerDirect[%s] missing: %+v", id, peerDirect)
	}
	return entry
}
