// Package adminclient is the HTTP client used by admin-server to reach the
// private admin RPC exposed by snetserv. It never exposes the service token to
// browsers.
package adminclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"snet/internal/protocol"
)

const internalAdminPrefix = "/internal/admin"

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Page is the paged-list envelope returned by the admin RPC.
type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// NetworkSummary mirrors the admin network-list item.
type NetworkSummary struct {
	Network        protocol.Network `json:"network"`
	NodeCount      int              `json:"nodeCount"`
	RelayPort      int              `json:"relayPort"`
	Online         bool             `json:"online"`
	Zombie         bool             `json:"zombie"`
	LastActivityAt int64            `json:"lastActivityAt"`
	PendingCount   int              `json:"pendingCount"`
}

type requestResult struct {
	header http.Header
	body   []byte
	status int
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any) (requestResult, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return requestResult{}, err
		}
		reader = bytes.NewReader(data)
	}
	target := c.baseURL + internalAdminPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return requestResult{}, err
	}
	req.Header.Set("X-Internal-Admin-Token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return requestResult{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return requestResult{}, err
	}
	if resp.StatusCode >= 400 {
		var e protocol.ErrResp
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return requestResult{header: resp.Header, body: data, status: resp.StatusCode}, &HTTPError{Code: resp.StatusCode, Message: e.Error}
	}
	return requestResult{header: resp.Header, body: data, status: resp.StatusCode}, nil
}

type HTTPError struct {
	Code    int
	Message string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("admin rpc: HTTP %d: %s", e.Code, e.Message) }

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	res, err := c.request(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	return decode(res.body, out)
}

func decode(data []byte, out any) error {
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

// AdminNetworksPage fetches one page of networks.
func (c *Client) AdminNetworksPage(ctx context.Context, q, status string, page, pageSize int) (Page[NetworkSummary], error) {
	query := url.Values{}
	if q != "" {
		query.Set("q", q)
	}
	if status != "" {
		query.Set("status", status)
	}
	query.Set("page", fmt.Sprint(page))
	query.Set("page_size", fmt.Sprint(pageSize))
	var out Page[NetworkSummary]
	err := c.get(ctx, "/networks", query, &out)
	return out, err
}

func (c *Client) AdminCreateNetwork(ctx context.Context, req protocol.AdminCreateNetworkReq) (protocol.AdminCreateNetworkResp, error) {
	var out protocol.AdminCreateNetworkResp
	res, err := c.request(ctx, http.MethodPost, "/networks", nil, req)
	if err != nil {
		return out, err
	}
	err = decode(res.body, &out)
	return out, err
}

func (c *Client) AdminDeleteNetwork(ctx context.Context, nid string) error {
	_, err := c.request(ctx, http.MethodDelete, "/networks/"+url.PathEscape(nid), nil, nil)
	return err
}

func (c *Client) AdminNetworkInfo(ctx context.Context, nid string) (protocol.NetworkInfoResp, error) {
	var out protocol.NetworkInfoResp
	err := c.get(ctx, "/networks/"+url.PathEscape(nid), nil, &out)
	return out, err
}

func (c *Client) AdminUpdateNetwork(ctx context.Context, nid string, req protocol.NetworkSettingsReq) error {
	_, err := c.request(ctx, http.MethodPatch, "/networks/"+url.PathEscape(nid), nil, req)
	return err
}

func (c *Client) AdminDeleteRoute(ctx context.Context, nid, target, cidr string) error {
	query := url.Values{"target": {target}, "cidr": {cidr}}
	_, err := c.request(ctx, http.MethodDelete, "/networks/"+url.PathEscape(nid)+"/routes", query, nil)
	return err
}

func (c *Client) AdminNodes(ctx context.Context, nid string) ([]protocol.Node, error) {
	var out []protocol.Node
	err := c.get(ctx, "/networks/"+url.PathEscape(nid)+"/nodes", nil, &out)
	return out, err
}

func (c *Client) AdminRemoveNode(ctx context.Context, nid, nodeID string) error {
	_, err := c.request(ctx, http.MethodDelete, "/networks/"+url.PathEscape(nid)+"/nodes/"+url.PathEscape(nodeID), nil, nil)
	return err
}

func (c *Client) AdminSetNodeRole(ctx context.Context, nid, nodeID, role string) error {
	_, err := c.request(ctx, http.MethodPatch, "/networks/"+url.PathEscape(nid)+"/nodes/"+url.PathEscape(nodeID)+"/role", nil,
		protocol.SetNodeRoleReq{Role: role})
	return err
}

func (c *Client) AdminPending(ctx context.Context, nid string) ([]protocol.PendingNode, error) {
	var out []protocol.PendingNode
	err := c.get(ctx, "/networks/"+url.PathEscape(nid)+"/pending", nil, &out)
	return out, err
}

func (c *Client) AdminApprovePending(ctx context.Context, nid, pendingID string) (protocol.PendingStatusResp, error) {
	var out protocol.PendingStatusResp
	res, err := c.request(ctx, http.MethodPost, "/networks/"+url.PathEscape(nid)+"/pending/"+url.PathEscape(pendingID)+"/approve", nil, nil)
	if err != nil {
		return out, err
	}
	err = decode(res.body, &out)
	return out, err
}

func (c *Client) AdminDenyPending(ctx context.Context, nid, pendingID string) error {
	_, err := c.request(ctx, http.MethodPost, "/networks/"+url.PathEscape(nid)+"/pending/"+url.PathEscape(pendingID)+"/deny", nil, nil)
	return err
}

func (c *Client) AdminResetCode(ctx context.Context, nid string) (string, error) {
	var out protocol.ResetCodeResp
	res, err := c.request(ctx, http.MethodPost, "/networks/"+url.PathEscape(nid)+"/code", nil, nil)
	if err != nil {
		return "", err
	}
	err = decode(res.body, &out)
	return out.PairingCode, err
}

func (c *Client) AdminAddExternalNode(ctx context.Context, nid, publicKey string) (map[string]string, error) {
	var out map[string]string
	res, err := c.request(ctx, http.MethodPost, "/networks/"+url.PathEscape(nid)+"/external-node", nil,
		map[string]string{"publicKey": publicKey})
	if err != nil {
		return nil, err
	}
	err = decode(res.body, &out)
	return out, err
}

// AdminInviteLink asks snetserv for the canonical invite link. The link is
// built server-side from the network's SnetServ binding, never from the
// admin-server request host.
func (c *Client) AdminInviteLink(ctx context.Context, nid string) (string, error) {
	var out struct {
		Link string `json:"link"`
	}
	err := c.get(ctx, "/networks/"+url.PathEscape(nid)+"/invite-link", nil, &out)
	return out.Link, err
}

func (c *Client) AdminStats(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := c.get(ctx, "/stats", nil, &out)
	return out, err
}

func (c *Client) AdminDevicesPage(ctx context.Context, q string, page, pageSize int) (Page[protocol.Device], error) {
	query := url.Values{"page": {fmt.Sprint(page)}, "page_size": {fmt.Sprint(pageSize)}}
	if q != "" {
		query.Set("q", q)
	}
	var out Page[protocol.Device]
	err := c.get(ctx, "/devices", query, &out)
	return out, err
}

func (c *Client) AdminDeviceNetworks(ctx context.Context, deviceID string) ([]protocol.Network, error) {
	var out protocol.NetworksResp
	err := c.get(ctx, "/devices/"+url.PathEscape(deviceID)+"/networks", nil, &out)
	return out.Networks, err
}

func (c *Client) AdminDeleteDevice(ctx context.Context, deviceID string) error {
	_, err := c.request(ctx, http.MethodDelete, "/devices/"+url.PathEscape(deviceID), nil, nil)
	return err
}

func (c *Client) AdminRenameDevice(ctx context.Context, deviceID, name string) error {
	_, err := c.request(ctx, http.MethodPatch, "/devices/"+url.PathEscape(deviceID), nil, protocol.DeviceNameReq{Name: name})
	return err
}

// Forward proxies one already-authenticated admin request to snetserv. The
// browser's admin session token is discarded; only the service token is sent.
func (c *Client) Forward(ctx context.Context, w http.ResponseWriter, r *http.Request, suffix string) {
	target := c.baseURL + internalAdminPrefix + suffix
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "admin rpc unavailable", http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Authorization")
	req.Header.Set("X-Internal-Admin-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		http.Error(w, "admin rpc unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
