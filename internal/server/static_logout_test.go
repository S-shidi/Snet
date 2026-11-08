package server

import (
	"net/http"
	"testing"
)

func TestAdminLogoutStaticTokenNoop(t *testing.T) {
	ts, _ := newTestServerOpts(t, Options{AdminToken: "secret"})

	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/logout", "secret", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("static-token logout = %d, want 204", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodGet, ts.URL+"/admin/stats", "secret", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin stats after static-token logout = %d, want 200", resp.StatusCode)
	}
}
