package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"snet/internal/protocol"
)

// httpStatusErr carries the HTTP status code from the server so callers can
// match on the code instead of parsing error strings.
type httpStatusErr struct {
	code int
	msg  string
}

func (e *httpStatusErr) Error() string { return fmt.Sprintf("server %d: %s", e.code, e.msg) }

type apiClient struct {
	server string
	http   *http.Client
	ctx    context.Context

	// smu guards the one-time incompatible-version warning emitted by
	// noteServerVersion.
	smu    sync.Mutex
	warned bool
}

// newAPIClient builds an HTTP client for the coordination server. TLS uses
// the system trust store; there is no per-server CA pinning or skip-verify
// escape hatch.
func newAPIClient(server string, ctx context.Context) *apiClient {
	if !strings.HasPrefix(server, "http") {
		server = "http://" + server
	}
	// Keep-alive transport: the daemon polls every couple of seconds and
	// reuses the pooled connection (and TLS session) instead of paying a full
	// handshake per request over slow/jittery links.
	// Disable HTTP/2 to avoid stream errors on unreliable connections.
	transport := &http.Transport{
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   false, // disabled to avoid HTTP/2 stream errors
	}
	httpClient := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	return &apiClient{server: strings.TrimRight(server, "/"), http: httpClient, ctx: ctx}
}

func (c *apiClient) do(method, path string, token string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.server+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set(protocol.VersionHeader, fmt.Sprint(protocol.APIVersion))
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	c.noteServerVersion(resp.Header.Get(protocol.VersionHeader))
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var e protocol.ErrResp
		_ = json.Unmarshal(data, &e)
		return &httpStatusErr{code: resp.StatusCode, msg: e.Error}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// noteServerVersion records the API major version the server advertises and
// logs a one-time warning when it is incompatible with this client, so a
// mismatch surfaces instead of silently misbehaving. A missing header means a
// legacy server without version negotiation: nothing to check.
func (c *apiClient) noteServerVersion(v string) {
	if v == "" {
		return
	}
	major, err := strconv.Atoi(v)
	if err != nil {
		return
	}
	c.smu.Lock()
	defer c.smu.Unlock()
	if major == protocol.APIVersion || c.warned {
		return
	}
	c.warned = true
	log.Printf("warn: server %s runs api version %d, this client speaks %d; upgrade one side", c.server, major, protocol.APIVersion)
}

func (c *apiClient) CreateNetwork(publicKey, deviceID, name, subnet string, approvalRequired bool,
	description string, tags []string, visibility string, allowOverlap bool) (protocol.CreateNetworkResp, error) {
	var out protocol.CreateNetworkResp
	err := c.do(http.MethodPost, "/api/v1/networks", "",
		protocol.CreateNetworkReq{PublicKey: publicKey, DeviceID: deviceID, Name: name, Subnet: subnet,
			ApprovalRequired: approvalRequired, Description: description, Tags: tags, Visibility: visibility,
			AllowOverlap: allowOverlap}, &out)
	return out, err
}

func (c *apiClient) Join(nid, code, publicKey, deviceID string) (protocol.JoinResp, error) {
	var out protocol.JoinResp
	err := c.do(http.MethodPost, "/api/v1/networks/"+nid+"/join", "",
		protocol.JoinReq{Code: code, PublicKey: publicKey, DeviceID: deviceID}, &out)
	return out, err
}

// RegisterDevice records (or refreshes) this device on the server.
func (c *apiClient) RegisterDevice(deviceID, publicKey, name string) error {
	return c.do(http.MethodPost, "/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: deviceID, PublicKey: publicKey, Name: name}, nil)
}

func (c *apiClient) RenameDevice(nid, token, name string) error {
	return c.do(http.MethodPatch, "/api/v1/networks/"+nid+"/device-name", token,
		protocol.DeviceNameReq{Name: name}, nil)
}

func (c *apiClient) SetNodeDevice(nid, nodeID, token, deviceID string) error {
	return c.do(http.MethodPost, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/device", token,
		protocol.SetNodeDeviceReq{DeviceID: deviceID}, nil)
}

func (c *apiClient) ClaimNetwork(nid, token, deviceID string) error {
	return c.do(http.MethodPost, "/api/v1/networks/"+nid+"/claim", token,
		protocol.ClaimReq{DeviceID: deviceID}, nil)
}

func (c *apiClient) NetworkInfo(nid, token string) (protocol.NetworkInfoResp, error) {
	var out protocol.NetworkInfoResp
	err := c.do(http.MethodGet, "/api/v1/networks/"+nid, token, nil, &out)
	return out, err
}

func (c *apiClient) MemberNetworkInfo(nid, token string) (protocol.NetworkInfoResp, error) {
	var out protocol.NetworkInfoResp
	err := c.do(http.MethodGet, "/api/v1/networks/"+nid+"/member-info", token, nil, &out)
	return out, err
}

func (c *apiClient) NetworkExists(nid, token string) (bool, error) {
	var out struct{ Exists bool }
	err := c.do(http.MethodGet, "/api/v1/networks/"+nid+"/exists", token, nil, &out)
	return out.Exists, err
}

func (c *apiClient) UpdateNetworkSettings(nid, token, name, subnet string, approvalRequired *bool,
	description *string, tags []string, visibility *string) error {
	return c.do(http.MethodPatch, "/api/v1/networks/"+nid, token,
		protocol.NetworkSettingsReq{Name: name, Subnet: subnet, ApprovalRequired: approvalRequired,
			Description: description, Tags: tags, Visibility: visibility}, nil)
}

func (c *apiClient) SetNodeRole(nid, token, nodeID, role string) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/role", token,
		protocol.SetNodeRoleReq{Role: role}, nil)
}

// SetRoutes replaces the network's delegated relay routes. Only the network
// owner may call it; the server keeps every route pending until its target
// node confirms it can act as the gateway.
func (c *apiClient) SetRoutes(nid, token string, routes []protocol.Route) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/routes", token,
		protocol.SetRoutesReq{Routes: routes}, nil)
}

// SetRelayCapability reports whether this node is able to act as a relay
// gateway for the network. relayErr carries the reason when the local
// gateway setup failed.
func (c *apiClient) SetRelayCapability(nid, token string, allowRelay bool, relayErr string) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/relay-capability", token,
		protocol.UpdateRelayCapabilityReq{AllowRelay: allowRelay, RelayError: relayErr}, nil)
}

func (c *apiClient) ApprovePending(nid, token, pendingID string) error {
	return c.do(http.MethodPost, "/api/v1/networks/"+nid+"/pending/"+pendingID+"/approve", token, nil, nil)
}

func (c *apiClient) DenyPending(nid, token, pendingID string) error {
	return c.do(http.MethodPost, "/api/v1/networks/"+nid+"/pending/"+pendingID+"/deny", token, nil, nil)
}

func (c *apiClient) DeleteNetwork(nid, token string) error {
	return c.do(http.MethodDelete, "/api/v1/networks/"+nid, token, nil, nil)
}

func (c *apiClient) KickNode(nid, token, nodeID string) error {
	return c.do(http.MethodDelete, "/api/v1/networks/"+nid+"/members/"+nodeID, token, nil, nil)
}

func (c *apiClient) ResetCode(nid, token string) (string, error) {
	var out protocol.ResetCodeResp
	err := c.do(http.MethodPost, "/api/v1/networks/"+nid+"/code", token, nil, &out)
	return out.PairingCode, err
}

// RotateInvite replaces the pairing code of a network the caller owns and
// returns the fresh invite link. Links shared before the rotation stop
// working.
func (c *apiClient) RotateInvite(nid, token string) (protocol.RotateInviteResp, error) {
	var out protocol.RotateInviteResp
	err := c.do(http.MethodPost, "/api/v1/networks/"+nid+"/rotate-invite", token, nil, &out)
	return out, err
}

func (c *apiClient) SetEndpointFor(nid, nodeID, token, endpoint, localEndpoint string, mapped bool) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/endpoint",
		token, protocol.SetEndpointReq{Endpoint: endpoint, LocalEndpoint: localEndpoint, EndpointMapped: mapped}, nil)
}

func (c *apiClient) SetEndpointV6For(nid, nodeID, token, endpointV6 string) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/endpoint",
		token, protocol.SetEndpointReq{EndpointV6: endpointV6}, nil)
}

// SetDirectEndpointsFor publishes the Relay v2 peer-to-peer socket addresses
// and NAT classification. The node ID is part of the path for diagnosability;
// authorization still comes from the node token.
func (c *apiClient) SetDirectEndpointsFor(nid, nodeID, token, endpoint, endpointV6, natType string) error {
	return c.do(http.MethodPut, "/api/v1/networks/"+nid+"/nodes/"+nodeID+"/direct-endpoints",
		token, protocol.SetDirectEndpointsReq{
			DirectEndpoint:   endpoint,
			DirectEndpointV6: endpointV6,
			NATType:          natType,
		}, nil)
}

// PeersState returns peers plus the caller's own node (with current IP) and
// the network subnet, so the daemon can detect subnet/IP changes. relayPorts
// declares per-node unicast relay port support (Phase 3): the server then
// allocates a unicast port for the caller and advertises peers' ports.
func (c *apiClient) PeersState(nid, token string, relayPorts bool) (protocol.PeersResp, error) {
	var out protocol.PeersResp
	var path string
	if relayPorts {
		path = "/api/v1/networks/" + nid + "/peers?relayPorts=1"
	} else {
		path = "/api/v1/networks/" + nid + "/peers"
	}
	err := c.do(http.MethodGet, path, token, nil, &out)
	return out, err
}

// RelayTicket fetches a short-lived Relay v2 ticket for this node. The
// endpoint in the response is the standalone relay to dial.
func (c *apiClient) RelayTicket(nid, token string) (protocol.RelayTicketResp, error) {
	var out protocol.RelayTicketResp
	err := c.do(http.MethodGet, "/api/v1/networks/"+nid+"/relay-ticket", token, nil, &out)
	return out, err
}

// PendingJoinStatus polls a join request awaiting owner approval.
func (c *apiClient) PendingJoinStatus(pendingID string) (protocol.PendingStatusResp, error) {
	var out protocol.PendingStatusResp
	err := c.do(http.MethodGet, "/api/v1/pending/"+pendingID, "", nil, &out)
	return out, err
}

func (c *apiClient) RemoveNode(nid, nodeID, token string) error {
	return c.do(http.MethodDelete, "/api/v1/networks/"+nid+"/nodes/"+nodeID, token, nil, nil)
}

func (c *apiClient) UpdateNodePublicKey(nid, nodeID, deviceID, deviceToken, publicKey string) error {
	path := fmt.Sprintf("/api/v1/networks/%s/nodes/%s/publickey", nid, nodeID)
	body := protocol.UpdateNodePublicKeyReq{PublicKey: publicKey, DeviceID: deviceID}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.server+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Token", deviceToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		var e protocol.ErrResp
		_ = json.Unmarshal(data, &e)
		return &httpStatusErr{code: resp.StatusCode, msg: e.Error}
	}
	return nil
}

// UpdateNodePublicKeyByToken rotates a node's WireGuard public key under the
// node's own bearer token. Used by the daemon's periodic key rotation, which
// must work without a persisted device token.
func (c *apiClient) UpdateNodePublicKeyByToken(nid, nodeID, token, publicKey string) error {
	path := fmt.Sprintf("/api/v1/networks/%s/nodes/%s/publickey", nid, nodeID)
	body := protocol.UpdateNodePublicKeyReq{PublicKey: publicKey}
	return c.do(http.MethodPost, path, token, body, nil)
}
