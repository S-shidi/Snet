package main

import (
	"encoding/json"
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

const (
	ctlAddr          = "127.0.0.1:19432"
	sandboxConfigDir = "/usr/local/etc/snet"
	daemonLabel      = "com.snet.daemon"
	daemonPlist      = "/Library/LaunchDaemons/com.snet.daemon.plist"
)

type SnetService struct {
	shutdownOnce sync.Once
}

func (s *SnetService) StartDaemon() error { return s.ensureDaemon() }

func binaryVersion(path string) (string, error) {
	out, err := exec.Command(path, "-version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// upgradeDaemonIfNeeded replaces /usr/local/bin/snetd when the App bundle
// carries a different build. Overwriting the .app does not update the
// LaunchDaemon binary, so this keeps the running daemon in sync with the UI.
func (s *SnetService) upgradeDaemonIfNeeded() error {
	appBinary, err := findDaemonBinary()
	if err != nil {
		return err
	}
	appVersion, err := binaryVersion(appBinary)
	if err != nil {
		return fmt.Errorf("读取应用内 snetd 版本失败: %w", err)
	}
	systemBinary := "/usr/local/bin/snetd"
	systemVersion, systemErr := binaryVersion(systemBinary)
	if systemErr == nil && systemVersion == appVersion {
		return nil
	}
	log.Printf("[Snet] daemon version mismatch app=%q system=%q", appVersion, systemVersion)
	scriptPath, appBundle, err := installPaths()
	if err != nil {
		return err
	}
	if err := installDaemon(scriptPath, appBundle); err != nil {
		return err
	}
	log.Printf("[Snet] upgraded daemon binary from App bundle")
	return nil
}

func installDaemon(scriptPath, appBundle string) error {
	home, _ := os.UserHomeDir()
	script := fmt.Sprintf(`set installScript to %s
set appBundle to %s
set userHome to %s
do shell script "sh " & quoted form of installScript & " " & quoted form of appBundle & " " & quoted form of userHome with administrator privileges`,
		appleScriptQuote(scriptPath), appleScriptQuote(appBundle), appleScriptQuote(home))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装系统服务失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	if err := waitForCtl(20 * time.Second); err != nil {
		return fmt.Errorf("系统服务安装后未就绪: %w", err)
	}
	return nil
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
		_ = exec.Command("launchctl", "kill", "SIGTERM", "system/"+daemonLabel).Run()
		_ = exec.Command("pkill", "-f", "/usr/local/bin/snetd").Run()
		log.Printf("snetd: shutdown cleanup complete")
	})
}

func (s *SnetService) ensureDaemon() error {
	if _, err := findDaemonBinary(); err != nil {
		return err
	}

	if _, err := os.Stat(daemonPlist); err == nil {
		if err := s.upgradeDaemonIfNeeded(); err != nil {
			return err
		}
		if ctlPortOpen() {
			return nil
		}
		if err := exec.Command("launchctl", "kickstart", "-k", "system/"+daemonLabel).Run(); err != nil {
			_ = exec.Command("launchctl", "load", "-w", daemonPlist).Run()
		}
		if err := waitForCtl(10 * time.Second); err != nil {
			return fmt.Errorf("系统服务未就绪: %w", err)
		}
		return nil
	}

	scriptPath, appBundle, err := installPaths()
	if err != nil {
		return err
	}
	return installDaemon(scriptPath, appBundle)
}

func findDaemonBinary() (string, error) {
	appBundle, err := appBundlePath()
	if err != nil {
		return "", errors.New("应用包损坏，缺少 snetd 核心")
	}
	resource := filepath.Join(appBundle, "Contents", "Resources", "snetd")
	st, err := os.Stat(resource)
	if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return "", errors.New("应用包损坏，缺少 snetd 核心")
	}
	return resource, nil
}

func installPaths() (script, appBundle string, err error) {
	appBundle, err = appBundlePath()
	if err != nil {
		return "", "", errors.New("应用包损坏，缺少安装脚本")
	}
	script = filepath.Join(appBundle, "Contents", "Resources", "install-daemon.sh")
	st, statErr := os.Stat(script)
	if statErr != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return "", "", errors.New("应用包损坏，缺少安装脚本")
	}
	return script, appBundle, nil
}

func appBundlePath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(filepath.Dir(execPath), "..", "..")), nil
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

func ctlTokenPath() string { return filepath.Join(sandboxConfigDir, "ctl-token") }

// setRelayOptions updates the daemon-wide relay switches used by the tray
// menu. It is deliberately a small ctl call so the tray and web UI share the
// same source of truth.
func setRelayOptions(allowRelay, useRelay bool) error {
	token, err := readCtlToken()
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`{"allowRelay":%t,"useRelay":%t}`, allowRelay, useRelay)
	req, err := http.NewRequest(http.MethodPost, "http://"+ctlAddr+"/ctl/relay", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ctl-Token", token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("设置中继选项失败: HTTP %d", resp.StatusCode)
	}
	return nil
}

func setRouteEnabled(nid, cidr string, enabled bool) error {
	token, err := readCtlToken()
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`{"nid":%q,"cidr":%q,"enabled":%t}`, nid, cidr, enabled)
	req, err := http.NewRequest(http.MethodPost, "http://"+ctlAddr+"/ctl/route-enabled", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ctl-Token", token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("设置路由开关失败: HTTP %d", resp.StatusCode)
	}
	return nil
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

func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
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
	if r.URL.Path == "/desktop/upgrade" && r.Method == http.MethodPost {
		var req struct {
			Version  string `json:"version"`
			AssetURL string `json:"assetUrl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		st, err := p.service.StartUpgrade(req.Version, req.AssetURL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(st)
		return
	}
	if r.URL.Path == "/desktop/upgrade-status" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.service.UpgradeStatus())
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
