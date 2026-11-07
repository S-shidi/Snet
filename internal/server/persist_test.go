package server

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"go.etcd.io/bbolt"
)

func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snet.db")

	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndpoint(joined.Token, "203.0.113.7:51821", "", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// "restart": reopen the same database
	s2, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	// network + owner + joiner survive
	peersResp, err := s2.ListPeers(created.Token)
	if err != nil {
		t.Fatalf("owner peers after restart: %v", err)
	}
	if len(peersResp.Peers) != 1 || peersResp.Peers[0].PublicKey != "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=" || peersResp.Peers[0].Endpoint != "203.0.113.7:51821" {
		t.Fatalf("owner peers wrong after restart: %+v", peersResp.Peers)
	}

	// pairing code still usable after restart
	j2, err := s2.Join(created.NetworkID, created.PairingCode, "sRw3bI7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2u=", "dev-joiner-2")
	if err != nil {
		t.Fatalf("join after restart failed: %v", err)
	}
	if j2.IP != "10.88.0.3" {
		t.Fatalf("ipam not restored, got %q want 10.88.0.3", j2.IP)
	}

	// remove works after restart
	if err := s2.RemoveNode(joined.Token); err != nil {
		t.Fatal(err)
	}
	peersResp, err = s2.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range peersResp.Peers {
		if p.ID == joined.NodeID {
			t.Fatalf("removed node still present: %+v", p)
		}
	}
}

func TestManagedNetworkPersists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snet.db")

	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.AdminCreateNetwork("永久网络", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !s.networks[created.NetworkID].n.Managed {
		t.Fatal("managed flag not set in memory")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ns := s2.networks[created.NetworkID]
	if ns == nil {
		t.Fatal("managed network lost after restart")
	}
	if !ns.n.Managed {
		t.Fatal("managed flag not restored after restart")
	}
	// unlimited, non-expiring code survives restart
	if ns.pairing.remaining != codeUnlimited || !ns.pairing.expiresAt.IsZero() {
		t.Fatalf("long-lived code not restored: %+v", ns.pairing)
	}
}

func TestTokenStoredHashed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snet.db")
	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := bbolt.Open(dbPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.View(func(tx *bbolt.Tx) error {
		tkb := tx.Bucket(bktTokens)
		if tkb == nil {
			t.Fatal("tokens bucket missing")
		}
		return tkb.ForEach(func(k, v []byte) error {
			if string(k) == created.Token {
				t.Fatalf("plaintext token found in db")
			}
			if string(k) != hashToken(created.Token) {
				t.Fatalf("token key not hashed: %q", k)
			}
			var te tokenEntry
			if err := json.Unmarshal(v, &te); err != nil {
				t.Fatal(err)
			}
			if te.NodeID != created.NodeID {
				t.Fatalf("token entry mismatch: %+v", te)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPairingCodeStoredHashed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "snet.db")
	s, err := NewStoreAt(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := bbolt.Open(dbPath, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.View(func(tx *bbolt.Tx) error {
		nb := tx.Bucket(bktNetworks)
		if nb == nil {
			t.Fatal("networks bucket missing")
		}
		v := nb.Get([]byte(created.NetworkID))
		if v == nil {
			t.Fatal("network row missing")
		}
		var r networkRecord
		if err := json.Unmarshal(v, &r); err != nil {
			t.Fatal(err)
		}
		if r.CodeHash != hashCode(created.PairingCode) {
			t.Fatalf("code hash mismatch")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLastSeenTracking(t *testing.T) {
	s := NewStore()
	created, _ := s.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)

	if err := s.SetEndpoint(created.Token, "203.0.113.1:51820", "", false); err != nil {
		t.Fatal(err)
	}
	peersResp, err := s.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(peersResp.Peers) != 0 {
		t.Fatalf("expected no peers for sole member, got %+v", peersResp.Peers)
	}
	if created.Token == "" {
		t.Fatal("empty token")
	}
	// owner lastSeen updated by ListPeers; verify via store internals
	time.Sleep(5 * time.Millisecond)
	_ = peersResp
	// direct check
	ns := s.networks[created.NetworkID]
	me := ns.nodes[created.NodeID]
	if me.LastSeen == 0 {
		t.Fatal("lastSeen not updated by ListPeers")
	}
}

func TestDeviceRecordPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snet.db")
	s, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := genOneCode(t, s)
	devTok, err := s.BindDevice(code, "dev-persist-1", testKey(1), "Device One")
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork(testKey(1), "dev-persist-1", "net", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	devs := s2.AdminDevices()
	if len(devs) != 1 {
		t.Fatalf("devices after restart = %+v, want 1", devs)
	}
	d := devs[0]
	if d.ID != "dev-persist-1" || d.Name != "Device One" || d.PublicKey != testKey(1) {
		t.Fatalf("device after restart = %+v", d)
	}
	nets := s2.DeviceNetworks("dev-persist-1")
	if len(nets) != 1 || nets[0].ID != created.NetworkID {
		t.Fatalf("device networks after restart = %+v, want [%s]", nets, created.NetworkID)
	}
	if !s2.ValidateDeviceToken("dev-persist-1", devTok) {
		t.Fatal("device token invalid after restart")
	}
}

func TestPersistPendingAndDeviceRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snet.db")
	s, err := NewStoreAt(path)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := genOneCode(t, s)
	devTok, err := s.BindDevice(code, "dev-row-1", testKey(1), "Row Device")
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork(testKey(2), "dev-owner", "office", "192.168.50.0/24", true)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, testKey(3), "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if joined.Status != "pending" || joined.PendingID == "" {
		t.Fatalf("join = %+v, want pending", joined)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *bbolt.Tx) error {
		pb := tx.Bucket([]byte("pending"))
		if pb == nil {
			t.Fatal("pending bucket missing")
		}
		raw := pb.Get([]byte(joined.PendingID))
		if raw == nil {
			t.Fatal("pending row missing")
		}
		var p pendingNode
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if p.Status != "pending" || p.NetworkID != created.NetworkID {
			t.Fatalf("pending row = %+v", p)
		}

		dvb := tx.Bucket([]byte("devices"))
		if dvb == nil {
			t.Fatal("devices bucket missing")
		}
		draw := dvb.Get([]byte("dev-row-1"))
		if draw == nil {
			t.Fatal("device row missing")
		}
		var d deviceRecord
		if err := json.Unmarshal(draw, &d); err != nil {
			t.Fatal(err)
		}
		if d.DeviceToken != devTok || d.PublicKey != testKey(1) {
			t.Fatalf("device row = %+v", d)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
