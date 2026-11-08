package server

import (
	"testing"

	"snet/internal/protocol"
)

func TestEnrichNodeWithIPInfoRelayV2(t *testing.T) {
	node := protocol.Node{
		IP:               "10.88.0.1",
		Endpoint:         "10.7.86.111:51820",
		DirectEndpoint:   "10.7.86.111:43971",
		DirectEndpointV6: "[2409:8a2b:e14:b4c0:204:e1ff:fec1:329]:36372",
	}
	enrichNodeWithIPInfo(&node, "10.88.0.0/24")
	if node.PublicIPv4 != "" {
		t.Fatalf("PublicIPv4 = %q, want empty for private endpoints", node.PublicIPv4)
	}
	if node.PublicIPv6 != "2409:8a2b:e14:b4c0:204:e1ff:fec1:329" {
		t.Fatalf("PublicIPv6 = %q", node.PublicIPv6)
	}
}

func TestEnrichNodeWithIPInfoPublicFallbacks(t *testing.T) {
	tests := []struct {
		name     string
		node     protocol.Node
		wantIPv4 string
		wantIPv6 string
	}{
		{
			name: "relay flow wins over private endpoint",
			node: protocol.Node{
				RelayFlow:      "203.0.113.9:41000",
				Endpoint:       "10.0.0.2:51820",
				DirectEndpoint: "10.0.0.2:43971",
			},
			wantIPv4: "203.0.113.9",
		},
		{
			name:     "public endpoint",
			node:     protocol.Node{Endpoint: "198.51.100.10:51820"},
			wantIPv4: "198.51.100.10",
		},
		{
			name: "public direct endpoint",
			node: protocol.Node{
				Endpoint:       "10.0.0.2:51820",
				DirectEndpoint: "203.0.113.7:43971",
			},
			wantIPv4: "203.0.113.7",
		},
		{
			name:     "legacy endpoint v6",
			node:     protocol.Node{EndpointV6: "[2001:db8::1]:51820"},
			wantIPv6: "2001:db8::1",
		},
		{
			name: "direct endpoint v6 fallback",
			node: protocol.Node{
				EndpointV6:       "[fd00::1]:51820",
				DirectEndpointV6: "[2001:db8::2]:36372",
			},
			wantIPv6: "2001:db8::2",
		},
		{
			name: "private loopback and link-local rejected",
			node: protocol.Node{
				Endpoint:         "127.0.0.1:51820",
				DirectEndpoint:   "169.254.10.20:43971",
				EndpointV6:       "[::1]:51820",
				DirectEndpointV6: "[fe80::1]:36372",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enrichNodeWithIPInfo(&tc.node, "")
			if tc.node.PublicIPv4 != tc.wantIPv4 {
				t.Fatalf("PublicIPv4 = %q, want %q", tc.node.PublicIPv4, tc.wantIPv4)
			}
			if tc.node.PublicIPv6 != tc.wantIPv6 {
				t.Fatalf("PublicIPv6 = %q, want %q", tc.node.PublicIPv6, tc.wantIPv6)
			}
		})
	}
}

func TestAdminNodesIncludesRelayV2PublicIPv6(t *testing.T) {
	st := NewStore()
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetDirectEndpoints(created.Token, "10.7.86.111:43971", "[2409:8a2b:e14:b4c0:204:e1ff:fec1:329]:36372", "cone"); err != nil {
		t.Fatal(err)
	}
	nodes, err := st.AdminNodes(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if node.ID != created.NodeID {
			continue
		}
		if node.PublicIPv4 != "" {
			t.Fatalf("PublicIPv4 = %q, want empty", node.PublicIPv4)
		}
		if node.PublicIPv6 != "2409:8a2b:e14:b4c0:204:e1ff:fec1:329" {
			t.Fatalf("PublicIPv6 = %q", node.PublicIPv6)
		}
		return
	}
	t.Fatalf("node %s not found", created.NodeID)
}
