package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"snet/internal/adminclient"
	"snet/internal/protocol"
)

func TestCoordinationHandlerDoesNotExposeAdmin(t *testing.T) {
	store, err := NewStoreAt("")
	if err != nil {
		t.Fatal(err)
	}
	h := NewCoordinationHandler(store, Options{AdminToken: "legacy-admin"})
	for _, path := range []string{"/admin", "/admin/", "/admin/networks", "/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, rr.Code)
		}
	}
}

func TestInternalAdminRequiresLoopbackAndToken(t *testing.T) {
	store, err := NewStoreAt("")
	if err != nil {
		t.Fatal(err)
	}
	h := NewInternalAdminHandler(store, Options{}, "service-token")

	req := httptest.NewRequest(http.MethodGet, "/internal/admin/networks", nil)
	req.RemoteAddr = "203.0.113.4:1234"
	req.Header.Set("X-Internal-Admin-Token", "service-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d, want 403", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/internal/admin/networks", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("missing token status = %d, want 403", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/internal/admin/login", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Internal-Admin-Token", "service-token")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("login RPC status = %d, want 404", rr.Code)
	}
}

func TestInternalAdminRPCClientCreatesAndDeletesNetwork(t *testing.T) {
	store, err := NewStoreAt("")
	if err != nil {
		t.Fatal(err)
	}
	h := NewInternalAdminHandler(store, Options{}, "service-token")
	ts := httptest.NewServer(h)
	defer ts.Close()
	client := adminclient.New(ts.URL, "service-token")

	created, err := client.AdminCreateNetwork(t.Context(), protocol.AdminCreateNetworkReq{Name: "rpc-test"})
	if err != nil {
		t.Fatal(err)
	}
	if created.NetworkID == "" {
		t.Fatal("empty network id")
	}
	page, err := client.AdminNetworksPage(t.Context(), "", "", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		if item.Network.ID == created.NetworkID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created network not in page: %+v", page.Items)
	}
	if err := client.AdminDeleteNetwork(t.Context(), created.NetworkID); err != nil {
		t.Fatal(err)
	}
	info, err := client.AdminNetworkInfo(t.Context(), created.NetworkID)
	if err == nil || info.ID != "" {
		t.Fatalf("deleted network still readable: %+v err=%v", info, err)
	}
}

func TestInternalAdminRPCForwardPreservesJSON(t *testing.T) {
	store, err := NewStoreAt("")
	if err != nil {
		t.Fatal(err)
	}
	h := NewInternalAdminHandler(store, Options{}, "service-token")
	ts := httptest.NewServer(h)
	defer ts.Close()
	client := adminclient.New(ts.URL, "service-token")

	req := httptest.NewRequest(http.MethodGet, "/admin/networks?page=1&page_size=20", nil)
	rr := httptest.NewRecorder()
	client.Forward(t.Context(), rr, req, "/networks?page=1&page_size=20")
	if rr.Code != http.StatusOK {
		t.Fatalf("forward status = %d: %s", rr.Code, rr.Body.String())
	}
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("forward body is not JSON: %v: %s", err, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "\"pageSize\"") {
		t.Fatalf("unexpected page body: %s", rr.Body.String())
	}
}
