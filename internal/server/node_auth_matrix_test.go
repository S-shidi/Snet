package server

import (
	"net/http"
	"testing"

	"snet/internal/protocol"
	"snet/internal/relay"
)

const serverMatrixRelaySecret = "0123456789abcdef0123456789abcdef"

type nodeAuthFixture struct {
	url              string
	nid              string
	ownerToken       string
	ownerNodeID      string
	memberToken      string
	otherNID         string
	otherToken       string
	otherNodeID      string
	approvePendingID string
	denyPendingID    string
}

func newNodeAuthFixture(t *testing.T) *nodeAuthFixture {
	t.Helper()
	ts, s := newTestServer(t)
	signer, err := relay.NewTicketSigner(serverMatrixRelaySecret)
	if err != nil {
		t.Fatal(err)
	}
	s.SetRelayV2("127.0.0.1:8092", signer)

	owner, err := s.CreateNetwork(testKey(1), "dev-owner", "matrix", "", true)
	if err != nil {
		t.Fatal(err)
	}
	pendingMember, err := s.Join(owner.NetworkID, owner.PairingCode, testKey(2), "dev-member1")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.OwnerApprove(owner.Token, pendingMember.PendingID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Token == "" {
		t.Fatal("approved member has no token")
	}
	approvePending, err := s.Join(owner.NetworkID, owner.PairingCode, testKey(3), "dev-member2")
	if err != nil {
		t.Fatal(err)
	}
	denyPending, err := s.Join(owner.NetworkID, owner.PairingCode, testKey(4), "dev-member3")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateNetwork(testKey(5), "dev-other", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return &nodeAuthFixture{
		url:              ts.URL,
		nid:              owner.NetworkID,
		ownerToken:       owner.Token,
		ownerNodeID:      owner.NodeID,
		memberToken:      approved.Token,
		otherNID:         other.NetworkID,
		otherToken:       other.Token,
		otherNodeID:      other.NodeID,
		approvePendingID: approvePending.PendingID,
		denyPendingID:    denyPending.PendingID,
	}
}

func TestNodeTokenRoutesAuthMatrix(t *testing.T) {
	f := newNodeAuthFixture(t)

	// Current behavior note: direct-endpoints, subnets and delete-node ignore
	// the path nid/nodeID and act on the bearer token's own network/node. A
	// valid token from another network therefore returns 204, not 401/404.
	// This matrix locks the current behavior; atlas §4/§5 compliance is
	// pending a decision.
	cases := []struct {
		name      string
		method    string
		path      func(*nodeAuthFixture) string
		body      any
		success   int
		noToken   int
		garbage   int
		cross     int
		member    int
		memberSet bool
	}{
		{
			name:   "direct-endpoints",
			method: http.MethodPut,
			path: func(f *nodeAuthFixture) string {
				return "/api/v1/networks/" + f.nid + "/nodes/" + f.ownerNodeID + "/direct-endpoints"
			},
			body:    protocol.SetDirectEndpointsReq{DirectEndpoint: "127.0.0.1:12345"},
			success: http.StatusNoContent, noToken: http.StatusUnauthorized, garbage: http.StatusUnauthorized, cross: http.StatusNoContent,
		},
		{
			name:    "relay-ticket",
			method:  http.MethodGet,
			path:    func(f *nodeAuthFixture) string { return "/api/v1/networks/" + f.nid + "/relay-ticket" },
			success: http.StatusOK, noToken: http.StatusUnauthorized, garbage: http.StatusUnauthorized, cross: http.StatusUnauthorized,
		},
		{
			name:    "subnets",
			method:  http.MethodPatch,
			path:    func(f *nodeAuthFixture) string { return "/api/v1/networks/" + f.nid + "/subnets" },
			body:    protocol.UpdateSubnetsReq{Subnets: []string{}},
			success: http.StatusNoContent, noToken: http.StatusUnauthorized, garbage: http.StatusUnauthorized, cross: http.StatusNoContent,
		},
		{
			name:    "exists",
			method:  http.MethodGet,
			path:    func(f *nodeAuthFixture) string { return "/api/v1/networks/" + f.nid + "/exists" },
			success: http.StatusOK, noToken: http.StatusUnauthorized, garbage: http.StatusNotFound, cross: http.StatusNotFound,
		},
		{
			name:   "pending-approve",
			method: http.MethodPost,
			path: func(f *nodeAuthFixture) string {
				return "/api/v1/networks/" + f.nid + "/pending/" + f.approvePendingID + "/approve"
			},
			success:   http.StatusOK,
			noToken:   http.StatusUnauthorized,
			garbage:   http.StatusNotFound,
			cross:     http.StatusNotFound,
			member:    http.StatusUnauthorized,
			memberSet: true,
		},
		{
			name:   "pending-deny",
			method: http.MethodPost,
			path: func(f *nodeAuthFixture) string {
				return "/api/v1/networks/" + f.nid + "/pending/" + f.denyPendingID + "/deny"
			},
			success:   http.StatusNoContent,
			noToken:   http.StatusUnauthorized,
			garbage:   http.StatusNotFound,
			cross:     http.StatusNotFound,
			member:    http.StatusUnauthorized,
			memberSet: true,
		},
		{
			name:    "delete-node",
			method:  http.MethodDelete,
			path:    func(f *nodeAuthFixture) string { return "/api/v1/networks/" + f.nid + "/nodes/" + f.ownerNodeID },
			success: http.StatusNoContent, noToken: http.StatusUnauthorized, garbage: http.StatusUnauthorized, cross: http.StatusNoContent,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Keep DELETE last in the case list: its success state removes the
			// owner node, so later owner-token cases would no longer be valid.
			states := []struct {
				name  string
				token string
				want  int
			}{
				{"no-token", "", tc.noToken},
				{"garbage", "garbage", tc.garbage},
				{"cross-network", f.otherToken, tc.cross},
			}
			if tc.memberSet {
				states = append(states, struct {
					name  string
					token string
					want  int
				}{"member", f.memberToken, tc.member})
			}
			states = append(states, struct {
				name  string
				token string
				want  int
			}{"valid-owner", f.ownerToken, tc.success})

			for _, st := range states {
				st := st
				t.Run(st.name, func(t *testing.T) {
					resp := doJSON(t, tc.method, f.url+tc.path(f), st.token, tc.body, nil)
					if resp.StatusCode != st.want {
						t.Fatalf("%s %s with %s = %d, want %d",
							tc.method, tc.path(f), st.name, resp.StatusCode, st.want)
					}
				})
			}
		})
	}
}
