// Package snetbind is the gomobile entry point for the Android client.
//
// gomobile bind turns the exported functions below into the Java class
// com.snet.android.Snetbind. Kotlin wraps that class in SnetCore.
package snetbind

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"snet/internal/client"
)

const (
	// internalCtlAddr is the loopback-only daemon API. The public proxy below
	// listens on the port requested by VpnService (normally 8081) and injects
	// the ctl token so the WebView never handles the secret.
	internalCtlAddr = "127.0.0.1:19432"
	ctlStartTimeout = 4 * time.Second
)

var (
	mu        sync.Mutex
	daemon    *client.Daemon
	ctlToken  string
	proxySrv  *http.Server
	proxyAddr net.Listener
)

// Start initialises snetd with the TUN fd supplied by Android's VpnService and
// serves the ctl API on 127.0.0.1:<ctlPort>. An empty return value means
// success; otherwise the returned string is a user-displayable error.
func Start(configDir string, tunFD int32, ctlPort int32) string {
	mu.Lock()
	defer mu.Unlock()

	if configDir == "" {
		return "配置目录不能为空"
	}
	if tunFD <= 0 {
		return fmt.Sprintf("无效的 TUN 文件描述符: %d", tunFD)
	}
	if ctlPort <= 0 || ctlPort > 65535 {
		return fmt.Sprintf("无效的控制端口: %d", ctlPort)
	}
	if int(ctlPort) == 19432 {
		return "控制端口不能使用 19432"
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Sprintf("创建配置目录失败: %v", err)
	}

	// VpnService can restart with a new fd while the daemon is already alive.
	if daemon != nil {
		daemon.SetAndroidTunFD(int(tunFD))
		return ""
	}

	configPath := filepath.Join(configDir, "config.json")
	cfg, err := client.LoadConfigAt(configPath)
	if err != nil {
		return fmt.Sprintf("读取配置失败: %v", err)
	}

	d := client.NewDaemonAt(cfg, configPath)
	d.SetDeviceIDFile(filepath.Join(configDir, "device.id"))
	d.SetAndroidTunFD(int(tunFD))

	token, err := client.LoadOrCreateCtlToken(client.CtlTokenPath(configPath))
	if err != nil {
		d.Close()
		return fmt.Sprintf("创建 ctl-token 失败: %v", err)
	}

	if err := d.Start(); err != nil {
		d.Close()
		return fmt.Sprintf("启动 snetd 失败: %v", err)
	}

	ctlErr := make(chan error, 1)
	go func() {
		ctlErr <- client.ServeCtl(d, token, internalCtlAddr, func(srv *http.Server) {
			if srv == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		})
	}()

	if err := waitForCtl(token, ctlErr); err != nil {
		shutdownDaemon(d, token)
		d.Close()
		return err.Error()
	}

	target, err := url.Parse("http://" + internalCtlAddr)
	if err != nil {
		shutdownDaemon(d, token)
		d.Close()
		return fmt.Sprintf("控制 API 地址无效: %v", err)
	}
	reverse := httputil.NewSingleHostReverseProxy(target)
	reverse.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "snetd unavailable: "+e.Error(), http.StatusBadGateway)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// The WebView never receives the ctl token; this loopback proxy adds it
		// before forwarding to the daemon.
		r.Header.Set("X-Ctl-Token", token)
		reverse.ServeHTTP(w, r)
	})

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", ctlPort))
	if err != nil {
		shutdownDaemon(d, token)
		d.Close()
		return fmt.Sprintf("监听控制端口 %d 失败: %v", ctlPort, err)
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	daemon = d
	ctlToken = token
	proxySrv = srv
	proxyAddr = ln
	return ""
}

// Stop stops the WebView proxy and the snetd daemon. It is safe to call more
// than once.
func Stop() string {
	mu.Lock()
	defer mu.Unlock()
	if daemon == nil {
		return ""
	}
	if proxySrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = proxySrv.Shutdown(ctx)
		cancel()
	}
	if proxyAddr != nil {
		_ = proxyAddr.Close()
	}
	shutdownDaemon(daemon, ctlToken)
	daemon.Close()
	daemon = nil
	ctlToken = ""
	proxySrv = nil
	proxyAddr = nil
	return ""
}

// GetStatus returns the daemon's /ctl/status JSON. When the daemon has not
// started it returns a JSON object with an error field instead of an empty
// string, so Kotlin can display a useful message.
func GetStatus() string {
	mu.Lock()
	running := daemon != nil
	token := ctlToken
	mu.Unlock()
	if !running || token == "" {
		return `{"error":"snetd 未启动"}`
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+internalCtlAddr+"/ctl/status", nil)
	if err != nil {
		return errorJSON(err)
	}
	req.Header.Set("X-Ctl-Token", token)
	httpClient := &http.Client{Timeout: 3 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return errorJSON(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return errorJSON(err)
	}
	if resp.StatusCode != http.StatusOK {
		return errorJSON(fmt.Errorf("status: HTTP %d: %s", resp.StatusCode, string(body)))
	}
	if len(body) == 0 {
		return `{"error":"status 返回为空"}`
	}
	return string(body)
}

func waitForCtl(token string, ctlErr <-chan error) error {
	deadline := time.Now().Add(ctlStartTimeout)
	httpClient := &http.Client{Timeout: 300 * time.Millisecond}
	for time.Now().Before(deadline) {
		select {
		case err := <-ctlErr:
			if err != nil {
				return fmt.Errorf("控制 API 启动失败: %v", err)
			}
			return fmt.Errorf("控制 API 已退出")
		default:
		}
		req, err := http.NewRequest(http.MethodGet, "http://"+internalCtlAddr+"/ctl/status", nil)
		if err == nil {
			req.Header.Set("X-Ctl-Token", token)
			resp, err := httpClient.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("控制 API 启动超时")
}

func shutdownDaemon(d *client.Daemon, token string) {
	if d == nil || token == "" {
		return
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+internalCtlAddr+"/ctl/shutdown", nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Ctl-Token", token)
	httpClient := &http.Client{Timeout: time.Second}
	resp, err := httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func errorJSON(err error) string {
	b, marshalErr := json.Marshal(map[string]string{"error": err.Error()})
	if marshalErr != nil {
		return `{"error":"unknown error"}`
	}
	return string(b)
}
