package server

import (
	"strings"
	"testing"

	"snet/internal/protocol"
)

const testHubID = "HUB12345"

func TestGatewaysAndHubAutoJoin(t *testing.T) {
	s := NewStore()
	if err := s.SetHubNode(testHubID, testKey(90), "203.0.113.10:51820"); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork(testKey(1), "dev-owner", "", "10.88.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndpoint(created.Token, "198.51.100.7:51820", "", false); err != nil {
		t.Fatal(err)
	}
	gateways, err := s.Gateways(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	var hub *protocol.GatewayInfo
	for i := range gateways {
		if gateways[i].ID == testHubID {
			hub = &gateways[i]
		}
	}
	if hub == nil || hub.Joined || !hub.CanBeGateway {
		t.Fatalf("unjoined hub placeholder = %+v", hub)
	}
	if _, err := s.JoinWithOptions(created.NetworkID, created.PairingCode, testKey(2), "dev-member", true); err != nil {
		t.Fatal(err)
	}
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !info.UseHubNode || info.HubNodeID != testHubID {
		t.Fatalf("hub state = use=%v id=%q", info.UseHubNode, info.HubNodeID)
	}
	var hubNode *protocol.Node
	for i := range info.Nodes {
		if info.Nodes[i].ID == testHubID {
			hubNode = &info.Nodes[i]
		}
	}
	if hubNode == nil || !hubNode.AllowRelay || !hubNode.IsHub || hubNode.IP != "10.88.0.254" {
		t.Fatalf("hub node = %+v", hubNode)
	}
}

func TestGatewayCapabilityAndWireGuardConfig(t *testing.T) {
	s := NewStore()
	if err := s.SetHubNode(testHubID, testKey(90), "203.0.113.10:51820"); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork(testKey(1), "dev-owner", "", "10.88.0.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndpoint(created.Token, "198.51.100.7:51820", "", false); err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, testKey(2), "dev-member")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEndpoint(joined.Token, "192.168.1.20:51820", "", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRoutes(created.Token, []protocol.Route{{TargetNodeID: created.NodeID, CIDR: "10.7.86.0/24"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRelayCapability(created.Token, true, ""); err != nil {
		t.Fatal(err)
	}
	gateways, err := s.Gateways(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	var public, private bool
	for _, g := range gateways {
		if g.ID == created.NodeID && g.CanBeGateway {
			public = true
		}
		if g.ID == joined.NodeID {
			private = !g.CanBeGateway && strings.Contains(g.Reason, "内网 NAT")
		}
	}
	if !public || !private {
		t.Fatalf("gateway classification public=%v private=%v: %+v", public, private, gateways)
	}

	cfg, err := s.GenerateWireGuardConfig(created.Token, testHubID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[Interface]", "PrivateKey = ", "Address = 10.88.0.",
		"PublicKey = " + testKey(90), "Endpoint = 203.0.113.10:51820",
		"AllowedIPs = 10.88.0.0/24, 10.7.86.0/24", "PersistentKeepalive = 25",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
	info, _ := s.NetworkInfo(created.Token)
	if !info.UseHubNode {
		t.Fatal("generating through hub should set UseHubNode")
	}
}

func TestRemoveHubClearsUseHub(t *testing.T) {
	s := NewStore()
	if err := s.SetHubNode(testHubID, testKey(90), "203.0.113.10:51820"); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateNetwork(testKey(1), "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GenerateWireGuardConfig(created.Token, testHubID); err != nil {
		t.Fatal(err)
	}
	if err := s.KickNode(created.Token, testHubID); err != nil {
		t.Fatal(err)
	}
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.UseHubNode {
		t.Fatal("UseHubNode should be cleared after removing hub")
	}
	for _, n := range info.Nodes {
		if n.ID == testHubID {
			t.Fatal("hub node still present")
		}
	}
}
