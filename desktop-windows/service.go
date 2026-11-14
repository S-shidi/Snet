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
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

const ctlAddr = "127.0.0.1:19432"

type SnetService struct {
	shutdownOnce sync.Once

	startMu  sync.Mutex
	stateMu  sync.Mutex
	starting bool
	startErr string
}

type desktopDaemonStatus struct {
	Running  bool   `json:"running"`
	Starting bool   `json:"starting"`
	Error    string `json:"error,omitempty"`
}

func (s *SnetService) StartDaemon() error { return s.ensureDaemon() }

func (s *SnetService) setStartState(starting bool, errMsg string) {
	s.stateMu.Lock()
	s.starting = starting
	s.startErr = errMsg
	s.stateMu.Unlock()
}

func (s *SnetService) desktopStatus() desktopDaemonStatus {
	s.stateMu.Lock()
	starting, errMsg := s.starting, s.startErr
	s.stateMu.Unlock()
	return desktopDaemonStatus{Running: ctlPortOpen(), Starting: starting, Error: errMsg}
}

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
	cmd := exec.Command(path, "-version")
	// snetd is a console binary. Hiding this probe prevents a console flash
	// when the GUI starts.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	out, err := cmd.CombinedOutput()
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
	s.startMu.Lock()
	defer s.startMu.Unlock()

	if ctlPortOpen() {
		s.setStartState(false, "")
		return nil
	}

	s.setStartState(true, "")
	fail := func(err error) error {
		s.setStartState(false, err.Error())
		return err
	}

	daemon, err := findDaemonBinary()
	if err != nil {
		return fail(err)
	}
	if err := os.MkdirAll(configDir(), 0o700); err != nil {
		return fail(fmt.Errorf("创建配置目录失败: %w", err))
	}
	if _, err := binaryVersion(daemon); err != nil {
		return fail(fmt.Errorf("snetd 无法运行: %w", err))
	}
	if err := launchElevated(daemon, daemonConfigPath()); err != nil {
		return fail(err)
	}
	if err := waitForCtl(60 * time.Second); err != nil {
		return fail(fmt.Errorf("UAC 已授权，但 snetd 未就绪: %w", err))
	}
	s.setStartState(false, "")
	return nil
}

// launchElevated starts snetd.exe through a UAC prompt using ShellExecuteEx
// directly. This avoids spawning powershell.exe (which briefly creates a
// console window) and starts the daemon hidden. WorkingDirectory is essential:
// wintun-go loads wintun.dll from the executable's application directory,
// which must therefore contain both snetd.exe and wintun.dll.
func launchElevated(daemon, configPath string) error {
	dir := filepath.Dir(daemon)
	if _, err := os.Stat(filepath.Join(dir, "wintun.dll")); err != nil {
		return fmt.Errorf("缺少 wintun.dll，请将其放在 %s", dir)
	}

	args := strings.Join([]string{
		"-config", quoteWindowsArg(configPath),
		"-log-file", quoteWindowsArg(filepath.Join(configDir(), "daemon.log")),
	}, " ")
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(daemon)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return err
	}
	cwd, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, params, cwd, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New("已取消管理员授权，后台服务未启动")
		}
		return fmt.Errorf("启动 snetd 提权进程失败: %w", err)
	}
	return nil
}

// quoteWindowsArg quotes one CreateProcess argument. Paths under ProgramData
// and the application directory normally need no quotes, but quoting keeps
// the command line valid if the app is unpacked into a path with spaces.
func quoteWindowsArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\v\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range s {
		switch r {
		case '\\':
			backslashes++
		case '"':
			b.WriteString(strings.Repeat("\\", backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
		default:
			b.WriteString(strings.Repeat("\\", backslashes))
			backslashes = 0
			b.WriteRune(r)
		}
	}
	b.WriteString(strings.Repeat("\\", backslashes*2))
	b.WriteByte('"')
	return b.String()
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
	if r.URL.Path == "/desktop/status" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.service.desktopStatus())
		return
	}
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
