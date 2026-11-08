package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"snet/internal/protocol"
)

func TestAdminSessionExpiry(t *testing.T) {
	s := NewStore()
	hash, err := s.EnsureAdminPassword("root", "pw")
	if err != nil {
		t.Fatal(err)
	}
	h := &handler{
		s:        s,
		opts:     Options{AdminUser: "root", AdminPass: "pw"},
		logins:   newRateLimiter(5, time.Minute),
		sessions: make(map[string]sessionEntry),
	}
	h.setCreds("root", hash)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin/login", h.adminLogin)
	mux.HandleFunc("GET /admin/stats", h.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var login protocol.AdminLoginResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		map[string]string{"username": "root", "password": "pw"}, &login)
	if resp.StatusCode != http.StatusOK || login.Token == "" {
		t.Fatalf("login = %d token=%q", resp.StatusCode, login.Token)
	}

	h.sessMu.Lock()
	entry, ok := h.sessions[hashToken(login.Token)]
	if !ok {
		h.sessMu.Unlock()
		t.Fatal("login session missing")
	}
	entry.expires = time.Now().Add(-time.Minute)
	h.sessions[hashToken(login.Token)] = entry
	h.sessMu.Unlock()

	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/stats", login.Token, nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired session = %d, want 401", resp.StatusCode)
	}

	h.sessMu.Lock()
	_, exists := h.sessions[hashToken(login.Token)]
	h.sessMu.Unlock()
	if exists {
		t.Fatal("expired session was not deleted from the map")
	}
}
