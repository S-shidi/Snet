package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const ctlAddr = "127.0.0.1:19432"

type App struct {
	ctx context.Context
	mu  sync.RWMutex
	err error
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.ensureDaemon(); err != nil {
		a.setError(err)
		log.Printf("snetd: %v", err)
		wruntime.WindowShow(ctx)
		_, _ = wruntime.MessageDialog(ctx, wruntime.MessageDialogOptions{
			Type:    wruntime.ErrorDialog,
			Title:   "SNET 启动失败",
			Message: err.Error(),
		})
	}
}

func (a *App) ShowWindow() {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx == nil {
		return
	}
	wruntime.WindowUnminimise(ctx)
	wruntime.WindowShow(ctx)
}

func (a *App) HideWindow() {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		wruntime.WindowHide(ctx)
	}
}

func (a *App) Quit() {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		wruntime.Quit(ctx)
	}
}

func (a *App) StartDaemon() error { return a.ensureDaemon() }

func (a *App) DaemonError() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.err == nil {
		return ""
	}
	return a.err.Error()
}

func (a *App) setError(err error) {
	a.mu.Lock()
	a.err = err
	a.mu.Unlock()
}

func (a *App) ensureDaemon() error {
	bin, err := findDaemonBinary()
	if err != nil {
		return err
	}
	dir, err := configDir()
	if err != nil {
		return err
	}
	configPath := filepath.Join(dir, "config.json")
	deviceIDFile := filepath.Join(dir, "device.id")
	logPath := filepath.Join(dir, "snetd.log")
	if daemonRunning(bin, configPath) {
		return nil
	}
	sanitizeSandboxConfig(configPath)

	// Use AppleScript's quoted form for every path. The daemon runs as root,
	// so create /usr/local/etc/snet with root:wheel permissions in the same
	// privileged script. This avoids SIP-protected user directories and keeps
	// "Application Support" out of the privileged command entirely.
	legacyPath := ""
	if legacy, err := legacyConfigDir(); err == nil {
		if st, statErr := os.Stat(legacy); statErr == nil && st.IsDir() {
			legacyPath = legacy
		}
	}
	script := fmt.Sprintf(`set binPath to %s
set dataDir to %s
set configPath to %s
set deviceIDPath to %s
set logPath to %s
set legacyPath to %s
set cleanupCmd to ""
if legacyPath is not "" then
  set cleanupCmd to "rm -rf " & quoted form of legacyPath & " && "
end if
do shell script cleanupCmd & "mkdir -p " & quoted form of dataDir & " && chown root:wheel " & quoted form of dataDir & " && chmod 755 " & quoted form of dataDir & " && nohup " & quoted form of binPath & " -config " & quoted form of configPath & " -device-id-file " & quoted form of deviceIDPath & " -ctl " & quoted form of "%s" & " >> " & quoted form of logPath & " 2>&1 &" with administrator privileges`,
		appleScriptQuote(bin), appleScriptQuote(dir), appleScriptQuote(configPath), appleScriptQuote(deviceIDFile), appleScriptQuote(logPath), appleScriptQuote(legacyPath), ctlAddr)
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("无法通过管理员权限启动 snetd: %v %s", err, strings.TrimSpace(string(out)))
	}
	for i := 0; i < 50; i++ {
		if _, err := readCtlToken(); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("snetd 已启动，但 ctl-token 尚未生成")
}

func daemonRunning(bin, configPath string) bool {
	// Only reuse a daemon started from this exact app bundle and sandbox
	// config. A system-wide snetd from /usr/local must never be adopted.
	pattern := regexp.QuoteMeta(bin) + ".*" + regexp.QuoteMeta(configPath)
	return exec.Command("pgrep", "-f", pattern).Run() == nil
}

func findDaemonBinary() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", errors.New("应用包损坏，缺少 snetd 核心")
	}
	resource := filepath.Clean(filepath.Join(filepath.Dir(execPath), "..", "Resources", "snetd"))
	st, err := os.Stat(resource)
	if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return "", errors.New("应用包损坏，缺少 snetd 核心")
	}
	return resource, nil
}

func sanitizeSandboxConfig(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var cfg struct {
		ServerAddr string `json:"serverAddr"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	server := strings.ToLower(strings.TrimSpace(cfg.ServerAddr))
	if strings.Contains(server, "127.0.0.1:8090") || strings.Contains(server, "localhost:8090") {
		if err := os.Remove(configPath); err == nil {
			log.Printf("removed legacy sandbox config with old server address: %s", configPath)
		}
	}
}

const sandboxConfigDir = "/usr/local/etc/snet"

func configDir() (string, error) {
	return sandboxConfigDir, nil
}

func legacyConfigDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "virtual-net"), nil
}

func ctlTokenPaths() []string {
	dir, err := configDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(dir, "ctl-token")}
}

func readCtlToken() (string, error) {
	for _, p := range ctlTokenPaths() {
		b, err := os.ReadFile(p)
		if err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s, nil
			}
		}
	}
	return "", errors.New("ctl-token not found")
}

func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

type ctlProxy struct {
	proxy *httputil.ReverseProxy
}

func newCtlProxy() http.Handler {
	target, _ := url.Parse("http://" + ctlAddr)
	p := httputil.NewSingleHostReverseProxy(target)
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "snetd 未就绪: "+err.Error(), http.StatusBadGateway)
	}
	return &ctlProxy{proxy: p}
}

func (p *ctlProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/ctl/") {
		http.NotFound(w, r)
		return
	}
	token, err := readCtlToken()
	if err != nil {
		http.Error(w, "snetd ctl-token 未生成", http.StatusServiceUnavailable)
		return
	}
	r.Header.Set("X-Ctl-Token", token)
	r.Header.Del("X-Snet-Web")
	p.proxy.ServeHTTP(w, r)
}
