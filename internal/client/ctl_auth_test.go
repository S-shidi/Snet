package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCtlAuthHandlerWebSession(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	auth := &authManager{
		d:        &Daemon{cfg: &Config{WebPasswordHash: "set"}},
		sessions: map[string]time.Time{"valid": time.Now().Add(time.Hour), "expired": time.Now().Add(-time.Minute)},
	}
	h := newCtlAuthHandler(next, "ctl-secret", auth)

	cases := []struct {
		name    string
		path    string
		token   string
		web     bool
		session string
		want    int
	}{
		{name: "native ctl token", path: "/ctl/status", token: "ctl-secret", want: http.StatusNoContent},
		{name: "missing ctl token", path: "/ctl/status", want: http.StatusUnauthorized},
		{name: "web without session", path: "/ctl/status", token: "ctl-secret", web: true, want: http.StatusUnauthorized},
		{name: "web bad session", path: "/ctl/status", token: "ctl-secret", web: true, session: "bad", want: http.StatusUnauthorized},
		{name: "web expired session", path: "/ctl/status", token: "ctl-secret", web: true, session: "expired", want: http.StatusUnauthorized},
		{name: "web valid session", path: "/ctl/status", token: "ctl-secret", web: true, session: "valid", want: http.StatusNoContent},
		{name: "auth path bypass", path: "/ctl/auth/check", want: http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("X-Ctl-Token", tc.token)
			}
			if tc.web {
				req.Header.Set("X-Snet-Web", "1")
			}
			if tc.session != "" {
				req.Header.Set("Authorization", "Bearer "+tc.session)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestCtlAuthHandlerNoPasswordAllowsWeb(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := newCtlAuthHandler(next, "ctl-secret", &authManager{d: &Daemon{cfg: &Config{}}})
	req := httptest.NewRequest(http.MethodGet, "/ctl/status", nil)
	req.Header.Set("X-Ctl-Token", "ctl-secret")
	req.Header.Set("X-Snet-Web", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
