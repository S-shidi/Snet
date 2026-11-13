package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"snet/internal/protocol"
)

// relayRouteFixture creates a network with an owner node and a member node,
// and returns the store plus both tokens and node IDs.
func relayRouteFixture(t *testing.T) (*Store, protocol.CreateNetworkResp, protocol.JoinResp) {
	t.Helper()
	s := NewStore()
	created, err := s.CreateNetwork(testKey(31), "dev-owner", "office", "10.88.7.0/24", false)
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.Join(created.NetworkID, created.PairingCode, testKey(32), "dev-member")
	if err != nil {
		t.Fatal(err)
	}
	return s, created, joined
}

func TestSetRoutesRequiresOwner(t *testing.T) {
	s, created, joined := relayRouteFixture(t)

	// A member must not be able to delegate routes.
	err := s.SetRoutes(joined.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	})
	if err != ErrUnauthorized {
		t.Fatalf("member SetRoutes err = %v, want ErrUnauthorized", err)
	}

	// The owner may.
	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatalf("owner SetRoutes: %v", err)
	}
}

func TestSetRoutesStartsPendingAndActivatesOnConfirmation(t *testing.T) {
	s, created, joined := relayRouteFixture(t)

	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatalf("SetRoutes: %v", err)
	}

	// The route exists but is not active yet: the target node never agreed
	// to act as the gateway.
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Routes) != 1 {
		t.Fatalf("routes = %+v, want 1", info.Routes)
	}
	if info.Routes[0].Status != protocol.RouteStatusPending {
		t.Fatalf("status = %q, want pending", info.Routes[0].Status)
	}

	// Members must not see a pending route as active.
	peers, err := s.ListPeers(joined.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers.Routes) != 0 {
		t.Fatalf("pending route must not be pushed to members: %+v", peers.Routes)
	}

	// The target node reports that it can act as the gateway.
	if err := s.UpdateRelayCapability(joined.Token, true, ""); err != nil {
		t.Fatalf("UpdateRelayCapability: %v", err)
	}

	info, err = s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Routes[0].Status != protocol.RouteStatusActive {
		t.Fatalf("status = %q, want active", info.Routes[0].Status)
	}

	// Now every member receives the active route.
	peers, err = s.ListPeers(joined.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers.Routes) != 1 || peers.Routes[0].CIDR != "192.168.9.0/24" {
		t.Fatalf("active route not pushed: %+v", peers.Routes)
	}
	if peers.Routes[0].TargetNodeID != joined.NodeID {
		t.Fatalf("route target = %q, want %q", peers.Routes[0].TargetNodeID, joined.NodeID)
	}
}

func TestAdminDeleteRoute(t *testing.T) {
	s, created, joined := relayRouteFixture(t)
	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatalf("SetRoutes: %v", err)
	}

	if err := s.AdminDeleteRoute(created.NetworkID, joined.NodeID, "192.168.9.0/24"); err != nil {
		t.Fatalf("AdminDeleteRoute: %v", err)
	}
	info, err := s.AdminNetworkInfo(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Routes) != 0 {
		t.Fatalf("routes after delete = %+v, want empty", info.Routes)
	}
	if err := s.AdminDeleteRoute(created.NetworkID, joined.NodeID, "192.168.9.0/24"); err != ErrNotFound {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestRelayCapabilityErrorKeepsRoutePending(t *testing.T) {
	s, created, joined := relayRouteFixture(t)

	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	// The node wants to be a gateway but the local setup failed.
	if err := s.UpdateRelayCapability(joined.Token, true, "iptables: permission denied"); err != nil {
		t.Fatalf("UpdateRelayCapability: %v", err)
	}

	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Routes[0].Status != protocol.RouteStatusPending {
		t.Fatalf("status = %q, want pending after a gateway error", info.Routes[0].Status)
	}
	// The owner sees why the route is not active.
	for _, n := range info.Nodes {
		if n.ID == joined.NodeID {
			if n.RelayError == "" {
				t.Fatal("expected the node to carry the gateway error")
			}
			if n.AllowRelay {
				t.Fatal("a node with a gateway error must not advertise allowRelay")
			}
		}
	}
	peers, err := s.ListPeers(joined.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(peers.Routes) != 0 {
		t.Fatalf("route must not be pushed while pending: %+v", peers.Routes)
	}
}

func TestRelayCapabilityRevokedDemotesRoute(t *testing.T) {
	s, created, joined := relayRouteFixture(t)

	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRelayCapability(joined.Token, true, ""); err != nil {
		t.Fatal(err)
	}
	// The node turns the gateway role off again.
	if err := s.UpdateRelayCapability(joined.Token, false, ""); err != nil {
		t.Fatal(err)
	}
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Routes[0].Status != protocol.RouteStatusPending {
		t.Fatalf("status = %q, want pending after the gateway opted out", info.Routes[0].Status)
	}
}

func TestSetRoutesValidation(t *testing.T) {
	s, created, joined := relayRouteFixture(t)

	cases := []struct {
		name  string
		route protocol.Route
	}{
		{"invalid cidr", protocol.Route{TargetNodeID: joined.NodeID, CIDR: "8.8.8.0/24"}},
		{"unknown node", protocol.Route{TargetNodeID: "node-that-does-not-exist", CIDR: "192.168.9.0/24"}},
		{"missing node", protocol.Route{CIDR: "192.168.9.0/24"}},
	}
	for _, tc := range cases {
		if err := s.SetRoutes(created.Token, []protocol.Route{tc.route}); err == nil {
			t.Fatalf("%s: expected an error", tc.name)
		}
	}

	// The same route twice is a duplicate.
	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err == nil {
		t.Fatal("expected a duplicate route to be rejected")
	}
}

func TestSetRoutesRejectsDuplicateCIDROnDifferentNodes(t *testing.T) {
	s, created, joined := relayRouteFixture(t)
	other, err := s.Join(created.NetworkID, created.PairingCode, testKey(33), "dev-member-2")
	if err != nil {
		t.Fatal(err)
	}

	err = s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
		{TargetNodeID: other.NodeID, CIDR: "192.168.9.0/24"},
	})
	if !errors.Is(err, ErrRouteCIDRConflict) {
		t.Fatalf("duplicate CIDR err = %v, want ErrRouteCIDRConflict", err)
	}
}

func TestSetRoutesKeepsActiveRoutesAcrossUpdates(t *testing.T) {
	s, created, joined := relayRouteFixture(t)

	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRelayCapability(joined.Token, true, ""); err != nil {
		t.Fatal(err)
	}
	// The owner rewrites the route list with the same route: it must stay
	// active instead of being downgraded to pending.
	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if info.Routes[0].Status != protocol.RouteStatusActive {
		t.Fatalf("status = %q, want active", info.Routes[0].Status)
	}
}

func TestRoutesDroppedWhenTargetNodeLeaves(t *testing.T) {
	s, created, joined := relayRouteFixture(t)

	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.KickNode(created.Token, joined.NodeID); err != nil {
		t.Fatal(err)
	}
	info, err := s.NetworkInfo(created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Routes) != 0 {
		t.Fatalf("routes = %+v, want none after the target left", info.Routes)
	}
}

// ---- HTTP surface ----

func TestRoutesHTTPRoutes(t *testing.T) {
	s, created, joined := relayRouteFixture(t)
	h := NewHandler(s, Options{})

	// PUT /api/v1/networks/{nid}/routes is owner-only.
	body := `{"routes":[{"targetNodeId":"` + joined.NodeID + `","cidr":"192.168.9.0/24"}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/networks/"+created.NetworkID+"/routes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+joined.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("member PUT routes = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/networks/"+created.NetworkID+"/routes", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+created.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner PUT routes = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}

	// The same CIDR may only be delegated once in the network.
	dupBody := `{"routes":[{"targetNodeId":"` + joined.NodeID + `","cidr":"192.168.9.0/24"},{"targetNodeId":"` + joined.NodeID + `","cidr":"192.168.9.0/24"}]}`
	req = httptest.NewRequest(http.MethodPut, "/api/v1/networks/"+created.NetworkID+"/routes", strings.NewReader(dupBody))
	req.Header.Set("Authorization", "Bearer "+created.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate CIDR PUT = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), ErrRouteCIDRConflict.Error()) {
		t.Fatalf("duplicate CIDR body = %s", rec.Body.String())
	}

	// The node reports its gateway capability.
	req = httptest.NewRequest(http.MethodPut, "/api/v1/networks/"+created.NetworkID+"/relay-capability",
		strings.NewReader(`{"allowRelay":true}`))
	req.Header.Set("Authorization", "Bearer "+joined.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("relay-capability = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}

	// The route is now active and part of the peers payload.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/networks/"+created.NetworkID+"/peers", nil)
	req.Header.Set("Authorization", "Bearer "+joined.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("peers = %d, want 200", rec.Code)
	}
	var peers protocol.PeersResp
	if err := json.Unmarshal(rec.Body.Bytes(), &peers); err != nil {
		t.Fatal(err)
	}
	if len(peers.Routes) != 1 || peers.Routes[0].Status != protocol.RouteStatusActive {
		t.Fatalf("routes = %+v, want one active route", peers.Routes)
	}
}

// TestAdminRoutesHTTPCoversRoutesAndDelete verifies the admin surface: the
// network detail carries the delegated routes and an admin can revoke one.
func TestAdminRoutesHTTPCoversRoutesAndDelete(t *testing.T) {
	s, created, joined := relayRouteFixture(t)
	ts := httptest.NewServer(NewHandler(s, Options{AdminToken: "adm"}))
	defer ts.Close()

	if err := s.SetRoutes(created.Token, []protocol.Route{
		{TargetNodeID: joined.NodeID, CIDR: "192.168.9.0/24"},
	}); err != nil {
		t.Fatal(err)
	}

	// The network detail exposes the delegated routes.
	req := httptest.NewRequest(http.MethodGet, "/admin/networks/"+created.NetworkID, nil)
	req.Header.Set("Authorization", "Bearer adm")
	rec := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin network info = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var info protocol.NetworkInfoResp
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Routes) != 1 || info.Routes[0].CIDR != "192.168.9.0/24" {
		t.Fatalf("admin routes = %+v, want the delegated route", info.Routes)
	}

	// An admin can revoke the route.
	req = httptest.NewRequest(http.MethodDelete,
		"/admin/networks/"+created.NetworkID+"/routes?target="+joined.NodeID+"&cidr=192.168.9.0/24", nil)
	req.Header.Set("Authorization", "Bearer adm")
	rec = httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete route = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	after, err := s.AdminNetworkInfo(created.NetworkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Routes) != 0 {
		t.Fatalf("routes after admin delete = %+v, want empty", after.Routes)
	}
}
