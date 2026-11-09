package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"
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
	}
}

func (a *App) beforeClose(ctx context.Context) bool {
	wruntime.WindowHide(ctx)
	return true
}

func (a *App) shutdown(ctx context.Context) {
	systray.Quit()
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
	if daemonRunning() {
		return nil
	}
	bin, err := findDaemonBinary()
	if err != nil {
		return err
	}
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	configPath := filepath.Join(dir, "config.json")
	deviceIDFile := filepath.Join(dir, "device.id")
	logPath := filepath.Join(dir, "snetd.log")

	cmdline := fmt.Sprintf("nohup %s -config %s -device-id-file %s -ctl %s >>%s 2>&1 &",
		shellQuote(bin), shellQuote(configPath), shellQuote(deviceIDFile), ctlAddr, shellQuote(logPath))
	script := fmt.Sprintf("do shell script %s with administrator privileges", appleScriptQuote(cmdline))
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

func daemonRunning() bool {
	for _, args := range [][]string{
		{"-x", "snetd"},
		{"-f", "snetd-darwin"},
	} {
		if err := exec.Command("pgrep", args...).Run(); err == nil {
			return true
		}
	}
	return false
}

func findDaemonBinary() (string, error) {
	execPath, _ := os.Executable()
	base := filepath.Dir(execPath)
	candidates := []string{
		os.Getenv("SNETD_BIN"),
		filepath.Join(base, "snetd"),
		filepath.Join(base, "snetd-darwin-arm64"),
		filepath.Join(base, "..", "Resources", "snetd"),
		filepath.Join(base, "..", "Resources", "snetd-darwin-arm64"),
		filepath.Join(base, "..", "..", "..", "dist", "snetd-darwin-arm64"),
		"/usr/local/snet/bin/snetd",
	}
	if p, err := exec.LookPath("snetd"); err == nil {
		candidates = append(candidates, p)
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", errors.New("找不到 snetd；请设置 SNETD_BIN，或把 snetd-darwin-arm64 放到应用 Resources 目录")
}

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "virtual-net"), nil
}

func ctlTokenPaths() []string {
	var out []string
	if dir, err := configDir(); err == nil {
		out = append(out, filepath.Join(dir, "ctl-token"))
	}
	out = append(out, "/usr/local/snet/ctl-token", "/etc/snet/ctl-token")
	return out
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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
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
