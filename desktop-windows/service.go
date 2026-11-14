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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	ctlAddr           = "127.0.0.1:19432"
	daemonServiceName = "SNetDaemon"
)

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

func ctlTokenPath() string { return filepath.Join(configDir(), "ctl-token") }

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

	if err := startDaemonService(); err != nil {
		return fail(err)
	}
	if err := waitForCtl(30 * time.Second); err != nil {
		return fail(fmt.Errorf("SNetDaemon 服务已启动，但控制端口未就绪: %w", err))
	}
	s.setStartState(false, "")
	return nil
}

// startDaemonService asks the SCM to start SNetDaemon. The installer grants
// authenticated users SERVICE_START, so this path needs no UAC prompt.
func startDaemonService() error {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("打开服务管理器失败: %w", err)
	}
	defer windows.CloseServiceHandle(scm)

	name, err := windows.UTF16PtrFromString(daemonServiceName)
	if err != nil {
		return err
	}
	h, err := windows.OpenService(scm, name, windows.SERVICE_START|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return errors.New("后台服务未安装，请运行 SnetWindows 安装包")
		}
		return fmt.Errorf("打开后台服务失败: %w", err)
	}
	s := &mgr.Service{Name: daemonServiceName, Handle: h}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("查询后台服务状态失败: %w", err)
	}
	for st.State == svc.StopPending {
		time.Sleep(200 * time.Millisecond)
		st, err = s.Query()
		if err != nil {
			return fmt.Errorf("等待后台服务停止失败: %w", err)
		}
	}
	if st.State == svc.Stopped {
		if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return fmt.Errorf("启动后台服务失败: %w", err)
		}
	}
	return nil
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
