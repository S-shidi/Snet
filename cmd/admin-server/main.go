package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"snet/internal/adminclient"
	"snet/internal/adminui"
	"snet/internal/buildinfo"
	"snet/internal/protocol"
)

type adminServer struct {
	client   *adminclient.Client
	username string
	password string

	mu       sync.Mutex
	sessions map[string]time.Time
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8095", "admin listen address")
	snetServer := flag.String("snet-server", "http://127.0.0.1:8096", "snetserv internal admin RPC base URL")
	internalToken := flag.String("internal-token", os.Getenv("SNET_INTERNAL_ADMIN_TOKEN"), "internal admin service token")
	adminUser := flag.String("admin-user", os.Getenv("SNET_ADMIN_USER"), "admin login username")
	adminPass := flag.String("admin-pass", os.Getenv("SNET_ADMIN_PASSWORD"), "admin login password")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM); enables HTTPS when set with -tls-key")
	tlsKey := flag.String("tls-key", "", "TLS key file (PEM)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		log.Println(buildinfo.String())
		return
	}
	if strings.TrimSpace(*internalToken) == "" {
		log.Fatal("admin-server: SNET_INTERNAL_ADMIN_TOKEN/-internal-token is required")
	}
	if strings.TrimSpace(*adminUser) == "" || *adminPass == "" {
		log.Printf("admin-server: no admin credentials configured; login is disabled")
	}

	s := &adminServer{
		client:   adminclient.New(*snetServer, *internalToken),
		username: strings.TrimSpace(*adminUser),
		password: *adminPass,
		sessions: make(map[string]time.Time),
	}
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		scheme := "http"
		if *tlsCert != "" && *tlsKey != "" {
			scheme = "https"
		}
		log.Printf("admin-server listening on %s://%s (snetserv: %s)", scheme, *addr, strings.TrimRight(*snetServer, "/"))
		var err error
		if *tlsCert != "" && *tlsKey != "" {
			err = httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		log.Fatalf("admin-server: %v", err)
	case <-stop:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

func (s *adminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "admin-server", "version": buildinfo.Version})
	case r.URL.Path == "/" || r.URL.Path == "/admin/":
		http.Redirect(w, r, "/admin", http.StatusMovedPermanently)
	case r.URL.Path == "/admin":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; "+
				"img-src data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		_, _ = w.Write(adminui.Page)
	case r.URL.Path == "/admin/login" && r.Method == http.MethodPost:
		s.login(w, r)
	case r.URL.Path == "/admin/logout" && r.Method == http.MethodPost:
		s.logout(w, r)
	case r.URL.Path == "/admin/bootstrap" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]bool{"available": false})
	case strings.HasPrefix(r.URL.Path, "/admin/"):
		if !s.authorized(r) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		suffix := strings.TrimPrefix(r.URL.Path, "/admin")
		s.client.Forward(r.Context(), w, r, suffix)
	default:
		http.NotFound(w, r)
	}
}

func (s *adminServer) authorized(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		delete(s.sessions, token)
		return false
	}
	return true
}

func (s *adminServer) login(w http.ResponseWriter, r *http.Request) {
	if s.username == "" || s.password == "" {
		writeErr(w, http.StatusNotFound, "login disabled")
		return
	}
	var req protocol.AdminLoginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(s.username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(req.Password), []byte(s.password)) == 1
	if !userOK || !passOK {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	token, err := sessionToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	expires := time.Now().Add(24 * time.Hour)
	s.mu.Lock()
	s.sessions[token] = expires
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, protocol.AdminLoginResp{Token: token, Expires: expires.Unix()})
}

func (s *adminServer) logout(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token != "" {
		s.mu.Lock()
		delete(s.sessions, token)
		s.mu.Unlock()
	}
	w.WriteHeader(http.StatusNoContent)
}

func sessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
