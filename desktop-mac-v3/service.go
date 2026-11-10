package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	ctlAddr          = "127.0.0.1:19432"
	sandboxConfigDir = "/usr/local/etc/snet"
	daemonLabel      = "com.snet.daemon"
	daemonPlist      = "/Library/LaunchDaemons/com.snet.daemon.plist"
)

type SnetService struct{}

func (s *SnetService) StartDaemon() error { return s.ensureDaemon() }

func (s *SnetService) ensureDaemon() error {
	if _, err := findDaemonBinary(); err != nil {
		return err
	}

	if _, err := os.Stat(daemonPlist); err == nil {
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
