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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	configPath := filepath.Join(dir, "config.json")
	deviceIDFile := filepath.Join(dir, "device.id")
	logPath := filepath.Join(dir, "snetd.log")
	if daemonRunning(bin, configPath) {
		return nil
	}

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

func configDir() (string, error) {
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
