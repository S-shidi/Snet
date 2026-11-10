package server

import (
	"net/http"
	"testing"

	"snet/internal/protocol"
)

type adminAuthFixture struct {
	url           string
	staticToken   string
	sessionToken  string
	nid           string
	codeID        string
	deleteStatic  string
	deleteSession string
	renameStatic  string
	renameSession string
}

func newAdminAuthFixture(t *testing.T) *adminAuthFixture {
	t.Helper()
	ts, s := newTestServerOpts(t, Options{
		AdminToken: "secret",
		AdminUser:  "root",
		AdminPass:  "pw",
	})
	created, err := s.CreateNetwork(testKey(1), "dev-admin", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := s.AdminGenerateAuthCodes(1, 1, nil)
	if err != nil || len(codes) != 1 {
		t.Fatalf("AdminGenerateAuthCodes: %v, codes=%d", err, len(codes))
	}
	for _, id := range []string{"dev-delete-static", "dev-delete-session", "dev-rename-static", "dev-rename-session"} {
		if err := s.RegisterDevice(id, testKey(len(id)), ""); err != nil {
			t.Fatalf("RegisterDevice(%s): %v", id, err)
		}
	}
	var login protocol.AdminLoginResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/admin/login", "",
		map[string]string{"username": "root", "password": "pw"}, &login)
	if resp.StatusCode != http.StatusOK || login.Token == "" {
		t.Fatalf("admin login = %d token=%q", resp.StatusCode, login.Token)
	}
	return &adminAuthFixture{
		url:           ts.URL,
		staticToken:   "secret",
		sessionToken:  login.Token,
		nid:           created.NetworkID,
		codeID:        codes[0].ID,
		deleteStatic:  "dev-delete-static",
		deleteSession: "dev-delete-session",
		renameStatic:  "dev-rename-static",
		renameSession: "dev-rename-session",
	}
}

func TestAdminRoutesAuthMatrix(t *testing.T) {
	f := newAdminAuthFixture(t)

	cases := []struct {
		name    string
		method  string
		path    func(*adminAuthFixture, string) string
		body    any
		success int
	}{
		{
			name:    "network-info",
			method:  http.MethodGet,
			path:    func(f *adminAuthFixture, _ string) string { return "/admin/networks/" + f.nid },
			success: http.StatusOK,
		},
		{
			name:    "authcode-renew",
			method:  http.MethodPatch,
			path:    func(f *adminAuthFixture, _ string) string { return "/admin/devices/authcodes/" + f.codeID + "/renew" },
			body:    protocol.AdminRenewAuthCodeReq{},
			success: http.StatusNoContent,
		},
		{
			name:   "device-delete",
			method: http.MethodDelete,
			path: func(f *adminAuthFixture, state string) string {
				if state == "session-token" {
					return "/admin/devices/" + f.deleteSession
				}
				return "/admin/devices/" + f.deleteStatic
			},
			success: http.StatusNoContent,
		},
		{
			name:   "device-rename",
			method: http.MethodPatch,
			path: func(f *adminAuthFixture, state string) string {
				if state == "session-token" {
					return "/admin/devices/" + f.renameSession
				}
				return "/admin/devices/" + f.renameStatic
			},
			body:    protocol.AdminRenameDeviceReq{Name: "renamed"},
			success: http.StatusNoContent,
		},
	}

	states := []struct {
		name  string
		token string
	}{
		{"no-token", ""},
		{"garbage", "garbage"},
		{"static-token", f.staticToken},
		{"session-token", f.sessionToken},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for _, st := range states {
				st := st
				t.Run(st.name, func(t *testing.T) {
					want := http.StatusUnauthorized
					if st.name == "static-token" || st.name == "session-token" {
						want = tc.success
					}
					resp := doJSON(t, tc.method, f.url+tc.path(f, st.name), st.token, tc.body, nil)
					if resp.StatusCode != want {
						t.Fatalf("%s %s with %s = %d, want %d",
							tc.method, tc.path(f, st.name), st.name, resp.StatusCode, want)
					}
				})
			}
		})
	}
}

func TestAdminDisabledReturns404(t *testing.T) {
	ts, _ := newTestServer(t)
	cases := []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"network-info", http.MethodGet, "/admin/networks/NOPE", nil},
		{"authcode-renew", http.MethodPatch, "/admin/devices/authcodes/NOPE/renew", protocol.AdminRenewAuthCodeReq{}},
		{"device-delete", http.MethodDelete, "/admin/devices/dev-nobody", nil},
		{"device-rename", http.MethodPatch, "/admin/devices/dev-nobody", protocol.AdminRenameDeviceReq{Name: "renamed"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, tc.method, ts.URL+tc.path, "", tc.body, nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("%s %s with admin disabled = %d, want 404", tc.method, tc.path, resp.StatusCode)
			}
		})
	}
}
