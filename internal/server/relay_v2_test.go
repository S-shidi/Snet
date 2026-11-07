package server

import (
	"testing"
	"time"

	"snet/internal/protocol"
	snetrelay "snet/internal/relay"
)

const relayV2TestSecret = "0123456789abcdef0123456789abcdef"

func TestRelayV2TicketAndPeersInfo(t *testing.T) {
	st := NewStore()
	signer, err := snetrelay.NewTicketSigner(relayV2TestSecret)
	if err != nil {
		t.Fatal(err)
	}
	st.SetRelayV2("relay.example:8092", signer)
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := st.IssueRelayTicket(created.Token, created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Endpoint != "relay.example:8092" || ticket.Ticket == "" || ticket.SessionID == "" {
		t.Fatalf("bad ticket response: %+v", ticket)
	}
	if ticket.NodeKey != protocol.RelayNodeKeyHex(created.NodeID) {
		t.Fatalf("node key = %s", ticket.NodeKey)
	}
	claims, err := signer.Verify(ticket.Ticket, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if claims.NetworkID != created.NetworkID || claims.NodeID != created.NodeID || claims.SessionID != ticket.SessionID {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if _, err := st.IssueRelayTicket(created.Token, "other-network"); err != ErrUnauthorized {
		t.Fatalf("wrong nid err = %v", err)
	}
	peers, err := st.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if peers.RelayV2 == nil || !peers.RelayV2.Enabled || peers.RelayV2.Endpoint != "relay.example:8092" {
		t.Fatalf("relay v2 info missing: %+v", peers.RelayV2)
	}
}

func TestRelayV2MultipleEndpoints(t *testing.T) {
	st := NewStore()
	signer, err := snetrelay.NewTicketSigner(relayV2TestSecret)
	if err != nil {
		t.Fatal(err)
	}
	st.SetRelayV2Endpoints([]string{" relay-a:8092 ", "relay-b:8092", "relay-a:8092", ""}, signer)
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := st.IssueRelayTicket(created.Token, created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Endpoint != "relay-a:8092" {
		t.Fatalf("primary endpoint = %q", ticket.Endpoint)
	}
	if len(ticket.Endpoints) != 2 || ticket.Endpoints[1] != "relay-b:8092" {
		t.Fatalf("ticket endpoints = %+v", ticket.Endpoints)
	}
	peers, err := st.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if peers.RelayV2 == nil || len(peers.RelayV2.Endpoints) != 2 || peers.RelayV2.Endpoints[0] != "relay-a:8092" {
		t.Fatalf("relay v2 info = %+v", peers.RelayV2)
	}
}

func TestSetDirectEndpointsPublishesToPeers(t *testing.T) {
	st := NewStore()
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetDirectEndpoints(created.Token, "192.168.1.5:51000", "[2001:db8::5]:51000", "cone"); err != nil {
		t.Fatal(err)
	}
	peers, err := st.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	n := peers.Self
	if n == nil {
		t.Fatal("missing self node")
	}
	if n.DirectEndpoint != "192.168.1.5:51000" || n.DirectEndpointV6 != "[2001:db8::5]:51000" || n.NATType != "cone" {
		t.Fatalf("direct endpoints = %+v", n)
	}
	if err := st.SetDirectEndpoints(created.Token, "192.168.1.5:51000", "", "bogus"); err != nil {
		t.Fatal(err)
	}
	peers, _ = st.ListPeers(created.Token)
	if peers.Self.NATType != "unknown" || peers.Self.DirectEndpointV6 != "" {
		t.Fatalf("cleared/invalid fields = %+v", peers.Self)
	}
}

func TestRelayV2DisabledByDefault(t *testing.T) {
	st := NewStore()
	created, err := st.CreateNetwork("qPw1bG7fV8xY2zA3bC4dE5fG6hI7jK8lM9nO0pQ1R2s=", "dev-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.IssueRelayTicket(created.Token, created.NetworkID); err != ErrRelayV2Disabled {
		t.Fatalf("err = %v", err)
	}
	peers, err := st.ListPeers(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if peers.RelayV2 != nil {
		t.Fatalf("relay v2 advertised while disabled: %+v", peers.RelayV2)
	}
}
