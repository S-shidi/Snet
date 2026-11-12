package client

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"snet/internal/constants"
	"snet/internal/protocol"
)

// resolveServer returns the configured SnetServ address, falling back to the
// official server when the caller leaves it empty. Custom self-hosted servers
// are accepted.
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
	pingLossRE = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%\s+(?:packet )?loss`)
	pingAvgRE  = regexp.MustCompile(`(?:rtt|round-trip)[^=]*=\s*[0-9.]+\/([0-9.]+)\/`)
)

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
	Code   string `json:"code"`
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

// ServeCtl exposes the local control API for snetctl and the Tauri UI.
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
	// also need a valid web session; native clients (Tauri/Android/snetctl)
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
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		nid, code := req.Nid, req.Code
		linkServer := ""
		if req.Link != "" {
			var err error
			nid, code, linkServer, _, err = protocol.ParseLink(req.Link)
			if err != nil {
				writeCtlErr(w, 400, err)
				return
			}
		}
		// An invite link that carries its own server address wins over the
		// request field: the join targets the network's own server. Both must
		// point at the fixed coordination server.
		server := resolveServer(req.Server)
		if linkServer != "" {
			server = resolveServer(linkServer)
		}
		resp, err := d.Join(server, req.Port, nid, code)
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
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		wait := "2"
		if runtime.GOOS == "darwin" {
			wait = "2000"
		}
		out, err := exec.CommandContext(ctx, "ping", "-c", "3", "-W", wait, target).CombinedOutput()
		if err != nil && len(out) == 0 {
			writeCtlErr(w, 500, err)
			return
		}
		loss, avg := parsePingOutput(string(out))
		writeCtlJSON(w, 200, map[string]any{"loss": loss, "avgRtt": avg})
	})

	mux.HandleFunc("GET /ctl/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="snet-diagnostic.log"`)
		_, _ = io.WriteString(w, d.logText(200))
	})

	mux.HandleFunc("POST /ctl/check-server", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ServerAddr string `json:"serverAddr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.CheckServer(req.ServerAddr); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /ctl/config", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ServerAddr string `json:"serverAddr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.SetServer(req.ServerAddr); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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

	mux.HandleFunc("POST /ctl/subnets", func(w http.ResponseWriter, r *http.Request) {
		var req CtlReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCtlErr(w, 400, err)
			return
		}
		if err := d.UpdateSubnets(req.Nid, req.Subnets); err != nil {
			writeCtlErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
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
