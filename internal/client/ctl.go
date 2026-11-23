package client

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/S-shidi/snet/shared/constants"
	"github.com/S-shidi/snet/shared/protocol"
)

// resolveServer returns a normalized SnetServ address, falling back to the
// official server when the caller leaves it empty.
func resolveServer(server string) string {
	if strings.TrimSpace(server) == "" {
		return constants.DefaultServer
	}
	return normalizeServer(server)
}

// shutdownGraceDelay is the pause between flushing the shutdown HTTP
// response and actually stopping the server.  Tests may override it.
var shutdownGraceDelay = 200 * time.Millisecond

var (
	pingLossRE      = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%\s+(?:packet )?loss`)
	pingAvgRE       = regexp.MustCompile(`(?:rtt|round-trip)[^=]*=\s*[0-9.]+\/([0-9.]+)\/`)
	pingAvgSimpleRE = regexp.MustCompile(`(?i)(?:average|avg)\s*=\s*([0-9.]+)\s*ms`)
)

func pingBinary() string {
	if p, err := exec.LookPath("ping"); err == nil {
		return p
	}
	for _, p := range []string{"/sbin/ping", "/bin/ping", "/usr/bin/ping", "/usr/local/bin/ping"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func parsePingOutput(out string) (float64, float64) {
	loss, avg := 100.0, 0.0
	if m := pingLossRE.FindStringSubmatch(out); len(m) == 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			loss = v
		}
	}
	if m := pingAvgRE.FindStringSubmatch(out); len(m) == 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			avg = v
		}
	} else if m := pingAvgSimpleRE.FindStringSubmatch(out); len(m) == 2 {
		// Windows: "Average = 12ms".
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			avg = v
		}
	}
	return loss, avg
}

func (d *Daemon) logText(n int) string {
	if d.configPath != "" {
		path := filepath.Join(filepath.Dir(d.configPath), "snetd.log")
		if b, err := os.ReadFile(path); err == nil {
			if text := tailLogLines(string(b), n); text != "" {
				return text
			}
		}
	}
	return LogTail(n)
}

func tailLogLines(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n"
}

// CtlReq is the request body for create/join control endpoints.
type CtlReq struct {
	Server string `json:"server"`
	Port   int    `json:"port"`
	Link   string `json:"link"`
	Nid    string `json:"nid"`
	Name   string `json:"name"`
	Subnet string `json:"subnet"`
	NodeID string `json:"nodeId"`

	ApprovalRequired *bool    `json:"approvalRequired"`
	AllowOverlap     *bool    `json:"allowOverlap"`
	PendingID        string   `json:"pendingId"`
	Subnets          []string `json:"subnets"`
	Enabled          *bool    `json:"enabled"`

	// Community-sharing metadata. Pointers distinguish "omitted" (leave
	// unchanged) from an explicit empty value (clear), so the desktop and
	// Android UIs can both update and remove descriptions, tags and visibility.
	Description *string   `json:"description"`
	Tags        *[]string `json:"tags"`
	Visibility  *string   `json:"visibility"`
}

// CtlJoinReq is the request body of the join endpoint. An invite link is the
// only accepted way in: the network id, the pairing code and the target
// server all travel inside the link, so nothing can be tampered with one by
// one.
type CtlJoinReq struct {
	Port int    `json:"port"`
	Link string `json:"link"`
}

type CtlInspectInviteReq struct {
	Link string `json:"link"`
}

type CtlInspectInviteResp struct {
	NetworkID string `json:"networkId"`
	Name      string `json:"name,omitempty"`
	Server    string `json:"server"`
}

// ServeCtl exposes the local control API for snetctl and the desktop UI.
// The listener is bound to 127.0.0.1 only (no network exposure).
// ctlToken is a shared secret written by the daemon to a 0644 file (readable
// by the desktop GUI which runs as a different privilege domain than the root
// / LocalSystem daemon) and read by all clients; every operational /ctl/*
// request (except /ctl/auth/*) must carry it via the X-Ctl-Token header. Auth
// endpoints (/ctl/auth/*) are protected by their own session mechanism.
// onShutdown is invoked (with the running server) shortly after the
// /ctl/shutdown handler has flushed its response; the caller decides how to
// terminate: a foreground daemon exits the process, while a Windows service
// stops the HTTP server so the service control manager sees a clean stop.
func ServeCtl(d *Daemon, ctlToken, addr string, onShutdown func(*http.Server)) error {
	var srv *http.Server
	mux := http.NewServeMux()

	// Timeout configuration to prevent client hangs and connection leaks.
	// These values are tuned for local control API usage (no network latency).
	const (
		readTimeout  = 10 * time.Second // Time to read request headers/body
		writeTimeout = 10 * time.Second // Time to write response
		idleTimeout  = 60 * time.Second // Time to keep idle connections alive
	)

	// The web console is proxied by nginx, which injects the ctl-token. That
	// token proves the request reached the local daemon, not that the browser
	// user completed the web login. Requests marked as web-proxied therefore
	// also need a valid web session; native clients (desktop/Android/snetctl)
	// keep using the ctl-token alone.
	auth := &authManager{d: d}
	ctlAuthMux := newCtlAuthHandler(mux, ctlToken, auth)

	mux.HandleFunc("POST /ctl/create", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		approval := false
		if req.ApprovalRequired != nil {
			approval = *req.ApprovalRequired
		}
		server := resolveServer(req.Server)
		var tags []string
		if req.Tags != nil {
			tags = *req.Tags
		}
		resp, err := d.Create(server, req.Port, req.Name, req.Subnet, approval,
			ShareOpt(req.Description, tags, req.Visibility),
			AllowOverlapOpt(req.AllowOverlap != nil && *req.AllowOverlap))
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, resp)
	})

	mux.HandleFunc("POST /ctl/join", func(w http.ResponseWriter, r *http.Request) {
		var req CtlJoinReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		// The invite link is the only accepted credential: it carries the
		// network id, the pairing code and the server to join.
		nid, code, linkServer, _, err := protocol.ParseInviteLink(req.Link)
		if err != nil {
			writeCtlErr(w, 400, fmt.Errorf("邀请链接无效: %w", err))
			return
		}
		// The encrypted server reference in the invite is the only server
		// selection for a join. A link without one falls back to the official
		// default, never to a configurable global default.
		server := resolveServer(linkServer)
		resp, err := d.Join(server, req.Port, nid, code)
		if err != nil {
			status, msg := joinErrorDetails(err, server)
			writeCtlErr(w, status, errors.New(msg))
			return
		}
		writeCtlJSON(w, 200, resp)
	})

	// InspectInvite decrypts the server binding without joining. The UI uses
	// this to warn before binding a network to a different SnetServ.
	mux.HandleFunc("POST /ctl/inspect-invite", func(w http.ResponseWriter, r *http.Request) {
		var req CtlInspectInviteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		nid, _, linkServer, name, err := protocol.ParseInviteLink(req.Link)
		if err != nil {
			writeCtlErr(w, 400, fmt.Errorf("邀请链接无效: %w", err))
			return
		}
		writeCtlJSON(w, 200, CtlInspectInviteResp{
			NetworkID: nid,
			Name:      name,
			Server:    resolveServer(linkServer),
		})
	})

	// InviteLink returns the current invite link of a network. The pairing
	// code and the network id are never handed out separately.
	mux.HandleFunc("GET /ctl/invite", func(w http.ResponseWriter, r *http.Request) {
		nid := r.URL.Query().Get("nid")
		if nid == "" {
			writeCtlErr(w, 400, errors.New("nid is required"))
			return
		}
		resp, err := d.InviteLink(nid)
		if err != nil {
			if errors.Is(err, ErrInviteExpired) {
				writeCtlErr(w, 409, err)
				return
			}
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, resp)
	})

	// RotateInvite replaces the invite link of a network. Every link shared
	// before the rotation stops working immediately.
	mux.HandleFunc("POST /ctl/rotate-invite", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		resp, err := d.RotateInvite(req.Nid)
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, resp)
	})

	mux.HandleFunc("GET /ctl/status", func(w http.ResponseWriter, r *http.Request) {
		st, err := d.Status()
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, st)
	})

	mux.HandleFunc("POST /ctl/ping", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Target string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		target := strings.TrimSpace(req.Target)
		if net.ParseIP(target) == nil {
			writeCtlErr(w, 400, errors.New("target must be an IP address"))
			return
		}
		bin := pingBinary()
		if bin == "" {
			writeCtlErr(w, 500, errors.New("诊断失败：容器内未找到 ping 命令，请更新客户端镜像"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		args := []string{"-c", "3", "-W", "2"}
		switch runtime.GOOS {
		case "darwin":
			// macOS ping waits in milliseconds.
			args = []string{"-c", "3", "-W", "2000"}
		case "windows":
			// Windows uses -n for count and -w for timeout, both in milliseconds.
			args = []string{"-n", "3", "-w", "2000"}
		}
		out, err := exec.CommandContext(ctx, bin, append(args, target)...).CombinedOutput()
		if err != nil && len(out) == 0 {
			writeCtlErr(w, 500, fmt.Errorf("诊断失败：无法执行 ping：%w", err))
			return
		}
		loss, avg := parsePingOutput(string(out))
		if loss >= 100 {
			writeCtlErr(w, 502, errors.New("诊断失败：目标不可达"))
			return
		}
		writeCtlJSON(w, 200, map[string]any{"loss": loss, "avgRtt": avg})
	})

	// Poke sends a few UDP datagrams through the tunnel to force WireGuard to
	// re-handshake with a sleeping peer and refresh NAT mappings.
	mux.HandleFunc("POST /ctl/poke", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Target string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Poke(strings.TrimSpace(req.Target)); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("GET /ctl/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="snet-diagnostic.log"`)
		_, _ = io.WriteString(w, d.logText(2000))
	})

	mux.HandleFunc("POST /ctl/leave", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if err := d.Leave(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/rejoin", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Rejoin(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/remove", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Remove(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/rename", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.UpdateSettings(req.Nid, req.Name, "", nil); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/rename-node-device", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.RenameNetworkDevice(req.Nid, req.NodeID, req.Name); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/set-node-ip", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Nid    string `json:"nid"`
			NodeID string `json:"nodeId"`
			IP     string `json:"ip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.SetNodeIP(req.Nid, req.NodeID, req.IP); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/rename-device", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.SetDeviceName(req.Name); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	// Automatic NAT mapping is a device-wide setting, so it has its own
	// endpoint instead of overloading the per-network settings API.
	mux.HandleFunc("POST /ctl/nat", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if req.Enabled == nil {
			writeCtlErr(w, 400, errors.New("缺少 enabled"))
			return
		}
		if err := d.SetNATMapping(*req.Enabled); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	// Relay switches are device-wide: allowRelay makes this device a relay
	// gateway for the networks it belongs to, useRelay controls whether it
	// honours the network's delegated routes.
	mux.HandleFunc("POST /ctl/route-enabled", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Nid     string `json:"nid"`
			CIDR    string `json:"cidr"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.SetRouteEnabled(req.Nid, req.CIDR, req.Enabled); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/relay", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AllowRelay bool `json:"allowRelay"`
			UseRelay   bool `json:"useRelay"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.SetRelayOptions(req.AllowRelay, req.UseRelay); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/settings", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		var tags []string
		if req.Tags != nil {
			tags = *req.Tags
		}
		if err := d.UpdateSettings(req.Nid, req.Name, req.Subnet, req.ApprovalRequired,
			ShareOpt(req.Description, tags, req.Visibility)); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/approve", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.ApprovePending(req.Nid, req.PendingID); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/deny", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.DenyPending(req.Nid, req.PendingID); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/cancel-pending", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.CancelPending(req.PendingID); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/delete", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.DeleteNetwork(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/kick", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Kick(req.Nid, req.NodeID); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/reset-code", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		code, err := d.ResetCode(req.Nid)
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, map[string]string{"pairingCode": code})
	})

	// Delegated relay routes. The network owner assigns a CIDR to a node; the
	// route stays pending until that node confirms it can act as the gateway.
	mux.HandleFunc("POST /ctl/routes", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Nid    string           `json:"nid"`
			Routes []protocol.Route `json:"routes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.SetRoutes(req.Nid, req.Routes); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("GET /ctl/gateways", func(w http.ResponseWriter, r *http.Request) {
		nid := r.URL.Query().Get("nid")
		gateways, err := d.Gateways(nid)
		if err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		writeCtlJSON(w, 200, gateways)
	})

	mux.HandleFunc("POST /ctl/wireguard-config", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Nid           string `json:"nid"`
			GatewayNodeID string `json:"gatewayNodeID"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		cfg, nodeID, err := d.WireGuardConfig(req.Nid, req.GatewayNodeID)
		if err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		writeCtlJSON(w, 200, map[string]string{"nodeId": nodeID, "config": cfg})
	})

	mux.HandleFunc("GET /ctl/wireguard-exports", func(w http.ResponseWriter, r *http.Request) {
		nid := r.URL.Query().Get("nid")
		writeCtlJSON(w, 200, d.WireGuardExports(nid))
	})

	mux.HandleFunc("GET /ctl/netinfo", func(w http.ResponseWriter, r *http.Request) {
		nid := r.URL.Query().Get("nid")
		info, err := d.Info(nid)
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, info)
	})

	mux.HandleFunc("GET /ctl/peers", func(w http.ResponseWriter, r *http.Request) {
		nid := r.URL.Query().Get("nid")
		peers, err := d.Peers(nid)
		if err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		writeCtlJSON(w, 200, peers)
	})

	// Legacy UI/diagnostic endpoint. It uses the global default server only for
	// display and never participates in network routing.
	mux.HandleFunc("GET /ctl/local-subnets", func(w http.ResponseWriter, r *http.Request) {
		subnets := d.DetectLocalSubnets()
		writeCtlJSON(w, 200, map[string]any{"subnets": subnets})
	})

	mux.HandleFunc("POST /ctl/device-id", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DeviceID string `json:"deviceId"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.DeviceID != "" {
			if err := d.SetDeviceID(req.DeviceID); err != nil {
				writeCtlErr(w, 500, err)
				return
			}
		}
		d.mu.Lock()
		id := d.cfg.DeviceID
		d.mu.Unlock()
		writeCtlJSON(w, 200, map[string]string{"deviceId": id})
	})

	mux.HandleFunc("POST /ctl/claim", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.Claim(req.Nid); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	})

	mux.HandleFunc("POST /ctl/shutdown", func(w http.ResponseWriter, r *http.Request) {
		// Gracefully stop the daemon: close all tunnels in memory (config on
		// disk keeps Active=true so a later start restores them), then let the
		// caller terminate the process. A foreground daemon exits with code 0
		// so launchd (KeepAlive SuccessfulExit=false) does not restart the job;
		// a Windows service stops the HTTP server so SCM records a clean
		// SERVICE_STOPPED and does not trigger crash-recovery restarts.
		d.Close()
		w.WriteHeader(204)
		go func() {
			time.Sleep(shutdownGraceDelay)
			onShutdown(srv)
		}()
	})

	// ── Auth endpoints ──────────────────────────────────────────────
	mux.HandleFunc("POST /ctl/auth/login", auth.handleLogin)
	mux.HandleFunc("POST /ctl/auth/logout", auth.handleLogout)
	mux.HandleFunc("GET /ctl/auth/check", auth.handleCheck)
	mux.HandleFunc("POST /ctl/auth/password", auth.handlePassword)

	srv = &http.Server{
		Addr:           addr,
		Handler:        ctlAuthMux,
		ReadTimeout:    readTimeout,
		WriteTimeout:   writeTimeout,
		IdleTimeout:    idleTimeout,
		MaxHeaderBytes: 1 << 20, // 1MB
	}
	return srv.ListenAndServe()
}

// newCtlAuthHandler gates all /ctl/* requests (except /ctl/auth/*) with the
// shared ctl token. Requests arriving through the Docker web proxy also need a
// valid web session, because nginx injects the ctl token for the browser.
func newCtlAuthHandler(next http.Handler, ctlToken string, auth *authManager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ctl/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-Ctl-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(ctlToken)) != 1 {
			writeCtlErr(w, http.StatusUnauthorized, errors.New("missing or invalid ctl token"))
			return
		}
		if r.Header.Get("X-Snet-Web") == "1" && (auth == nil || !auth.authorized(r)) {
			writeCtlErr(w, http.StatusUnauthorized, errors.New("web login required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authManager handles web console authentication with bcrypt passwords
// and in-memory session tokens.
type authManager struct {
	d        *Daemon
	mu       sync.Mutex
	sessions map[string]time.Time // token -> expiry
}

// authorized reports whether the request carries a valid web session. A daemon
// with no web password configured is still in first-run setup, so web access
// remains open until the operator sets a password.
func (a *authManager) authorized(r *http.Request) bool {
	if a == nil || a.d == nil || a.d.cfg == nil {
		return false
	}
	a.d.mu.RLock()
	hasPassword := a.d.cfg.WebPasswordHash != ""
	a.d.mu.RUnlock()
	if !hasPassword {
		return true
	}
	token := extractToken(r)
	if token == "" {
		return false
	}
	a.mu.Lock()
	expiry, ok := a.sessions[token]
	a.mu.Unlock()
	return ok && time.Now().Before(expiry)
}

func (a *authManager) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCtlErr(w, 400, err)
		return
	}
	a.d.mu.Lock()
	hash := a.d.cfg.WebPasswordHash
	a.d.mu.Unlock()
	if hash == "" {
		writeCtlErr(w, 400, errors.New("未设置密码"))
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		writeCtlErr(w, 401, errors.New("密码错误"))
		return
	}
	token, err := generateToken(32)
	if err != nil {
		writeCtlErr(w, 500, err)
		return
	}
	a.mu.Lock()
	if a.sessions == nil {
		a.sessions = make(map[string]time.Time)
	}
	expiry := time.Now().Add(24 * time.Hour)
	if req.Remember {
		expiry = time.Now().Add(30 * 24 * time.Hour)
	}
	a.sessions[token] = expiry
	a.mu.Unlock()
	writeCtlJSON(w, 200, map[string]string{"token": token})
}

func (a *authManager) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r)
	if token != "" {
		a.mu.Lock()
		delete(a.sessions, token)
		a.mu.Unlock()
	}
	w.WriteHeader(204)
}

func (a *authManager) handleCheck(w http.ResponseWriter, r *http.Request) {
	a.d.mu.Lock()
	hasPassword := a.d.cfg.WebPasswordHash != ""
	a.d.mu.Unlock()
	if !hasPassword {
		writeCtlJSON(w, 200, map[string]any{"hasPassword": false, "authenticated": true})
		return
	}
	token := extractToken(r)
	a.mu.Lock()
	expiry, ok := a.sessions[token]
	a.mu.Unlock()
	authenticated := ok && time.Now().Before(expiry)
	writeCtlJSON(w, 200, map[string]any{"hasPassword": true, "authenticated": authenticated})
}

func (a *authManager) handlePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeCtlErr(w, 400, err)
		return
	}
	if len(req.New) < 8 {
		writeCtlErr(w, 400, errors.New("密码至少需要8个字符"))
		return
	}
	a.d.mu.Lock()
	hasPassword := a.d.cfg.WebPasswordHash != ""
	a.d.mu.Unlock()

	if hasPassword {
		if !a.authorized(r) {
			writeCtlErr(w, 401, errors.New("未登录"))
			return
		}
		a.d.mu.Lock()
		if err := bcrypt.CompareHashAndPassword([]byte(a.d.cfg.WebPasswordHash), []byte(req.Current)); err != nil {
			a.d.mu.Unlock()
			writeCtlErr(w, 401, errors.New("当前密码错误"))
			return
		}
		a.d.mu.Unlock()
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	if err != nil {
		writeCtlErr(w, 500, err)
		return
	}
	a.d.mu.Lock()
	a.d.cfg.WebPasswordHash = string(hash)
	a.d.mu.Unlock()
	if err := a.d.SaveConfig(); err != nil {
		writeCtlErr(w, 500, err)
		return
	}
	if hasPassword {
		token := extractToken(r)
		a.mu.Lock()
		a.sessions = map[string]time.Time{token: a.sessions[token]}
		a.mu.Unlock()
	}
	w.WriteHeader(204)
}

func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func generateToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeCtlJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeCtlErr(w http.ResponseWriter, status int, err error) {
	writeCtlJSON(w, status, map[string]string{"error": err.Error()})
}

// joinErrorDetails turns server and transport failures into stable,
// user-facing join messages. Keeping the mapping here means every UI adapter
// (web, desktop, Android) receives the same text and HTTP status.
func joinErrorDetails(err error, server string) (int, string) {
	var se *httpStatusErr
	if errors.As(err, &se) {
		switch se.code {
		case http.StatusNotFound:
			return http.StatusNotFound, "网络不存在或已被删除"
		case http.StatusUnauthorized, http.StatusForbidden:
			// The web adapter treats local 401 as an expired console session;
			// use 403 for a bad invite code so joining cannot log the user out.
			return http.StatusForbidden, "配对码不正确或已过期"
		case http.StatusConflict:
			return http.StatusConflict, "该设备已在网络内，请勿重复加入"
		case http.StatusTooManyRequests:
			return http.StatusTooManyRequests, "请求太频繁，请稍后再试"
		}
	}
	if isTimeoutError(err) {
		return http.StatusGatewayTimeout, fmt.Sprintf("服务端 %s 不可达，请检查网络或服务器状态", server)
	}
	if strings.Contains(err.Error(), "已加入") {
		return http.StatusConflict, "该设备已在网络内，请勿重复加入"
	}
	return http.StatusInternalServerError, fmt.Sprintf("加入失败：%v", err)
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
