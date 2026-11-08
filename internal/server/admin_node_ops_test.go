package server

import "testing"

func TestAdminSetNodeRole(t *testing.T) {
	st := NewStore()
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := st.Join(created.NetworkID, created.PairingCode, "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdminSetNodeRole(created.NetworkID, joined.NodeID, "admin"); err != nil {
		t.Fatal(err)
	}
	assertNodeRole(t, st, created.NetworkID, joined.NodeID, "admin")

	if err := st.AdminSetNodeRole(created.NetworkID, joined.NodeID, "owner"); err != nil {
		t.Fatal(err)
	}
	info, err := st.AdminNetworkInfo(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if info.OwnerNodeID != joined.NodeID || info.OwnerDeviceID != "dev-joiner" {
		t.Fatalf("owner not transferred: node=%s device=%s", info.OwnerNodeID, info.OwnerDeviceID)
	}
	assertNodeRole(t, st, created.NetworkID, created.NodeID, "admin")
	assertNodeRole(t, st, created.NetworkID, joined.NodeID, "owner")

	if err := st.AdminSetNodeRole(created.NetworkID, joined.NodeID, "member"); err == nil {
		t.Fatal("demoting the owner should fail")
	}
}

func TestAdminSetNodeRoleRejectsExternalOwner(t *testing.T) {
	st := NewStore()
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, _, err := st.AdminAddExternalNode(created.NetworkID, "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdminSetNodeRole(created.NetworkID, nodeID, "owner"); err == nil {
		t.Fatal("external node should not become owner")
	}
}

func TestAdminSetNodeAllowedSubnets(t *testing.T) {
	st := NewStore()
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := st.Join(created.NetworkID, created.PairingCode, "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdminSetNodeAllowedSubnets(created.NetworkID, joined.NodeID, []string{"192.168.50.0/24"}); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.AdminNodes(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.ID == joined.NodeID {
			if len(n.AllowedSubnets) != 1 || n.AllowedSubnets[0] != "192.168.50.0/24" {
				t.Fatalf("AllowedSubnets = %v", n.AllowedSubnets)
			}
			return
		}
	}
	t.Fatalf("node %s not found", joined.NodeID)
}

func TestAdminSetNodeAllowedSubnetsRejectsOverlap(t *testing.T) {
	st := NewStore()
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := st.Join(created.NetworkID, created.PairingCode, "rQw2bH7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2t=", "dev-joiner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AdminSetNodeAllowedSubnets(created.NetworkID, joined.NodeID, []string{created.Subnet}); err == nil {
		t.Fatal("network subnet overlap should fail")
	}
	if err := st.AdminSetNodeAllowedSubnets(created.NetworkID, joined.NodeID, []string{"192.168.50.0/24"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AdminSetNodeAllowedSubnets(created.NetworkID, created.NodeID, []string{"192.168.50.0/24"}); err == nil {
		t.Fatal("another node's subnet overlap should fail")
	}
}

func assertNodeRole(t *testing.T, st *Store, nid, nodeID, want string) {
	t.Helper()
	nodes, err := st.AdminNodes(nid)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.ID == nodeID {
			if n.Role != want {
				t.Fatalf("node %s role = %q, want %q", nodeID, n.Role, want)
			}
			return
		}
	}
	t.Fatalf("node %s not found", nodeID)
}
