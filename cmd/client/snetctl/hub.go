// SPDX-License-Identifier: BSD-3-Clause
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/S-shidi/snet/shared/protocol"
)

const hubRegisterPath = "/api/hub/register"

var hubDeviceIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)

type hubHTTPError struct {
	StatusCode int
	Message    string
}

func (e *hubHTTPError) Error() string { return e.Message }

func hubCommand(args []string) {
	if len(args) == 0 {
		hubUsage(2)
	}
	switch args[0] {
	case "register":
		hubRegister(args[1:])
	case "-h", "--help", "help":
		hubUsage(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown hub command %q\n\n", args[0])
		hubUsage(2)
	}
}

func hubUsage(code int) {
	fmt.Fprintln(os.Stderr, `usage: snetctl hub register --server URL --device-id ID --network SLUG --token TOKEN [--wg-endpoint IP:PORT] [--json]

Register this device with the Hub embedded in a SnetServ instance.
The device token is sent as X-Device-Token.`)
	os.Exit(code)
}

func hubRegister(args []string) {
	fs := flag.NewFlagSet("hub register", flag.ExitOnError)
	server := fs.String("server", "", "SnetServ base URL, e.g. https://snet.example.com")
	deviceID := fs.String("device-id", "", "device id (8-64 chars: letters, digits, '-' or '_')")
	network := fs.String("network", "", "network slug")
	token := fs.String("token", "", "device token (X-Device-Token)")
	wgEndpoint := fs.String("wg-endpoint", "", "optional WireGuard endpoint (ip:port)")
	asJSON := fs.Bool("json", false, "print the raw JSON response")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: snetctl hub register --server URL --device-id ID --network SLUG --token TOKEN [--wg-endpoint IP:PORT] [--json]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	opts := hubRegisterOptions{
		Server:     *server,
		DeviceID:   *deviceID,
		Network:    *network,
		Token:      *token,
		WGEndpoint: *wgEndpoint,
	}
	client := &http.Client{Timeout: 15 * time.Second}
	peer, raw, err := executeHubRegister(context.Background(), client, opts)
	if err != nil {
		fatal(err)
	}
	if *asJSON {
		fmt.Println(strings.TrimSpace(string(raw)))
		return
	}
	printHubPeer(peer)
}

type hubRegisterOptions struct {
	Server     string
	DeviceID   string
	Network    string
	Token      string
	WGEndpoint string
}

func executeHubRegister(ctx context.Context, client *http.Client, opts hubRegisterOptions) (*protocol.WGPeerSummary, json.RawMessage, error) {
	if err := validateHubRegister(opts.Server, opts.DeviceID, opts.Network, opts.Token); err != nil {
		return nil, nil, err
	}
	baseURL, err := normalizeHubServer(opts.Server)
	if err != nil {
		return nil, nil, err
	}
	reg := protocol.DeviceRegistration{
		DeviceID:     opts.DeviceID,
		NetworkSlug:  opts.Network,
		Capabilities: []string{"wireguard"},
		WGEndpoint:   opts.WGEndpoint,
		Timestamp:    time.Now().Unix(),
	}
	return postHubRegistration(ctx, client, baseURL, opts.Token, reg)
}

func validateHubRegister(server, deviceID, network, token string) error {
	if strings.TrimSpace(server) == "" {
		return errors.New("--server is required")
	}
	if !hubDeviceIDRe.MatchString(deviceID) {
		return errors.New("deviceId 无效：需 8-64 位，首字符为字母或数字，其余可为字母、数字、'-' 或 '_'")
	}
	if strings.TrimSpace(network) == "" {
		return errors.New("--network is required")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("--token is required")
	}
	return nil
}

func normalizeHubServer(raw string) (string, error) {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("--server 无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("--server 需以 http:// 或 https:// 开头")
	}
	if u.Host == "" {
		return "", errors.New("--server 缺少主机名")
	}
	return s, nil
}

func postHubRegistration(ctx context.Context, client *http.Client, baseURL, token string, reg protocol.DeviceRegistration) (*protocol.WGPeerSummary, json.RawMessage, error) {
	body, err := json.Marshal(reg)
	if err != nil {
		return nil, nil, fmt.Errorf("构造 Hub 注册请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+hubRegisterPath, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("构造 Hub 注册请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Token", token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("无法连接 Hub 服务器 %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("读取 Hub 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, nil, &hubHTTPError{StatusCode: resp.StatusCode, Message: "设备令牌无效"}
		}
		msg := hubErrorMessage(raw)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		if resp.StatusCode == http.StatusBadRequest {
			return nil, nil, &hubHTTPError{StatusCode: resp.StatusCode, Message: msg}
		}
		return nil, nil, &hubHTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("Hub 注册失败 (HTTP %d): %s", resp.StatusCode, msg),
		}
	}
	if !json.Valid(raw) {
		return nil, nil, errors.New("Hub 返回了无效 JSON")
	}
	var peer protocol.WGPeerSummary
	if err := json.Unmarshal(raw, &peer); err != nil {
		return nil, nil, fmt.Errorf("解析 Hub 响应失败: %w", err)
	}
	return &peer, raw, nil
}

func hubErrorMessage(raw []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		return strings.TrimSpace(payload.Error)
	}
	return strings.TrimSpace(string(raw))
}

func printHubPeer(peer *protocol.WGPeerSummary) {
	fmt.Println("已注册到 Hub")
	fmt.Printf("  peerId:     %s\n", peer.PeerID)
	if peer.PublicKey != "" {
		fmt.Printf("  publicKey:  %s\n", peer.PublicKey)
	}
	if peer.AllowedIPs != "" {
		fmt.Printf("  allowedIps: %s\n", peer.AllowedIPs)
	}
	if peer.Endpoint != "" {
		fmt.Printf("  endpoint:   %s\n", peer.Endpoint)
	}
	if peer.LastSeenAt > 0 {
		fmt.Printf("  lastSeenAt: %s\n", time.Unix(peer.LastSeenAt, 0).Format(time.RFC3339))
	}
}
