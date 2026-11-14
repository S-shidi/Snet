package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const ctlAddr = "127.0.0.1:19432"

type SnetService struct {
	shutdownOnce sync.Once
}

func (s *SnetService) StartDaemon() error { return s.ensureDaemon() }

// configDir is shared by the GUI and the elevated daemon. ProgramData is
// machine-wide, so the daemon keeps its config/token across users and app
// upgrades instead of writing into an administrator's per-user AppData.
func configDir() string {
	if p := strings.TrimSpace(os.Getenv("ProgramData")); p != "" {
		return filepath.Join(p, "SNET")
	}
	return `C:\ProgramData\SNET`
}

func daemonConfigPath() string { return filepath.Join(configDir(), "config.json") }
func ctlTokenPath() string     { return filepath.Join(configDir(), "ctl-token") }

func binaryVersion(path string) (string, error) {
	out, err := exec.Command(path, "-version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *SnetService) ShutdownDaemon() {
	s.shutdownOnce.Do(func() {
		log.Printf("snetd: shutting down")
		if token, err := readCtlToken(); err == nil {
			req, reqErr := http.NewRequest(http.MethodPost, "http://"+ctlAddr+"/ctl/shutdown", nil)
			if reqErr == nil {
				req.Header.Set("X-Ctl-Token", token)
				client := &http.Client{Timeout: 2 * time.Second}
				if resp, doErr := client.Do(req); doErr == nil {
					_ = resp.Body.Close()
				}
			}
		}
		log.Printf("snetd: shutdown cleanup complete")
	})
}

func (s *SnetService) ensureDaemon() error {
	if ctlPortOpen() {
		return nil
	}
	daemon, err := findDaemonBinary()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	if _, err := binaryVersion(daemon); err != nil {
		return fmt.Errorf("snetd 无法运行: %w", err)
	}
	if err := launchElevated(daemon, daemonConfigPath()); err != nil {
		return err
	}
	if err := waitForCtl(60 * time.Second); err != nil {
		return fmt.Errorf("UAC 已授权，但 snetd 未就绪: %w", err)
	}
	return nil
}

// launchElevated starts snetd.exe through a UAC prompt. WorkingDirectory is
// essential: wintun-go loads wintun.dll from the executable's application
// directory, which must therefore contain both snetd.exe and wintun.dll.
func launchElevated(daemon, configPath string) error {
	dir := filepath.Dir(daemon)
	if _, err := os.Stat(filepath.Join(dir, "wintun.dll")); err != nil {
		return fmt.Errorf("缺少 wintun.dll，请将其放在 %s", dir)
	}
	ps := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; Start-Process -FilePath %s -ArgumentList @(%s,%s,%s,%s) -WorkingDirectory %s -Verb RunAs`,
		psQuote(daemon), psQuote("-config"), psQuote(configPath), psQuote("-log-file"), psQuote(filepath.Join(configDir(), "daemon.log")), psQuote(dir),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", ps)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("启动 snetd 提权进程失败（可能取消了 UAC）: %s", msg)
	}
	return nil
}

// psQuote encodes a value as a PowerShell single-quoted literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func findDaemonBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法定位应用目录: %w", err)
	}
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, "snetd.exe"),
		filepath.Join(dir, "resources", "snetd.exe"),
		filepath.Join(dir, "..", "snetd.exe"),
	}
	for _, p := range candidates {
		if st, statErr := os.Stat(p); statErr == nil && !st.IsDir() {
			return filepath.Clean(p), nil
		}
	}
	return "", errors.New("应用目录缺少 snetd.exe")
}

func ctlPortOpen() bool {
	conn, err := net.DialTimeout("tcp", ctlAddr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForCtl(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctlPortOpen() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("127.0.0.1:19432 未就绪")
}

func readCtlToken() (string, error) {
	b, err := os.ReadFile(ctlTokenPath())
	if err != nil {
		return "", err
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return s, nil
	}
	return "", errors.New("ctl-token is empty")
}

type ctlProxy struct {
	service *SnetService
	next    http.Handler
	proxy   *httputil.ReverseProxy
}

func newCtlProxy(service *SnetService, next http.Handler) http.Handler {
	target, _ := url.Parse("http://" + ctlAddr)
	p := httputil.NewSingleHostReverseProxy(target)
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "snetd 未就绪: "+err.Error(), http.StatusBadGateway)
	}
	p.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode == http.StatusUnauthorized {
			_ = resp.Body.Close()
			return errors.New("snetd rejected ctl-token")
		}
		return nil
	}
	return &ctlProxy{service: service, next: next, proxy: p}
}

func (p *ctlProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/desktop/start" {
		if err := p.service.StartDaemon(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/ctl/") {
		p.next.ServeHTTP(w, r)
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
