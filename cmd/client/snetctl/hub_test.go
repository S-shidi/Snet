// SPDX-License-Identifier: BSD-3-Clause
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testHubClient() *http.Client {
	return &http.Client{}
}

func TestExecuteHubRegister(t *testing.T) {
	var gotMethod, gotPath, gotToken, gotContentType string
	var gotBody map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Device-Token")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"peerId":"dev-12345","publicKey":"pk","allowedIps":"10.0.0.2/32","endpoint":"203.0.113.10:51820","lastSeenAt":1700000000}`)
	}))
	defer srv.Close()

	peer, raw, err := executeHubRegister(context.Background(), testHubClient(), hubRegisterOptions{
		Server:     srv.URL + "/",
		DeviceID:   "dev-12345",
		Network:    "home",
		Token:      "device-token",
		WGEndpoint: "203.0.113.10:51820",
	})
	if err != nil {
		t.Fatalf("executeHubRegister: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/hub/register" {
		t.Fatalf("request = %s %s, want POST /api/hub/register", gotMethod, gotPath)
	}
	if gotToken != "device-token" {
		t.Fatalf("X-Device-Token = %q", gotToken)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	for _, key := range []string{"deviceId", "networkSlug", "capabilities", "wgEndpoint", "timestamp"} {
		if _, ok := gotBody[key]; !ok {
			t.Errorf("request missing camelCase key %q", key)
		}
	}
	for _, key := range []string{"device_id", "network_slug", "wg_endpoint", "last_seen_at"} {
		if _, ok := gotBody[key]; ok {
			t.Errorf("request contains snake_case key %q", key)
		}
	}
	if peer.PeerID != "dev-12345" || peer.Endpoint != "203.0.113.10:51820" || peer.PublicKey != "pk" || peer.AllowedIPs != "10.0.0.2/32" {
		t.Fatalf("peer = %+v", peer)
	}
	if !json.Valid(raw) {
		t.Fatalf("raw response is not valid JSON: %s", raw)
	}
}

func TestExecuteHubRegisterUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer srv.Close()

	_, _, err := executeHubRegister(context.Background(), testHubClient(), hubRegisterOptions{
		Server:   srv.URL,
		DeviceID: "dev-12345",
		Network:  "home",
		Token:    "bad-token",
	})
	var apiErr *hubHTTPError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want 401 hubHTTPError", err)
	}
	if err.Error() != "设备令牌无效" {
		t.Fatalf("err = %q, want 设备令牌无效", err.Error())
	}
}

func TestExecuteHubRegisterBadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"networkSlug required"}`)
	}))
	defer srv.Close()

	_, _, err := executeHubRegister(context.Background(), testHubClient(), hubRegisterOptions{
		Server:   srv.URL,
		DeviceID: "dev-12345",
		Network:  "home",
		Token:    "device-token",
	})
	var apiErr *hubHTTPError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("err = %v, want 400 hubHTTPError", err)
	}
	if err.Error() != "networkSlug required" {
		t.Fatalf("err = %q, want server message", err.Error())
	}
}

func TestExecuteHubRegisterRejectsShortDeviceIDBeforeRequest(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, _, err := executeHubRegister(context.Background(), testHubClient(), hubRegisterOptions{
		Server:   srv.URL,
		DeviceID: "short",
		Network:  "home",
		Token:    "device-token",
	})
	if err == nil || !strings.Contains(err.Error(), "deviceId") {
		t.Fatalf("err = %v, want local deviceId error", err)
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("server received %d requests, want 0", n)
	}
}

func TestValidateHubRegisterDeviceID(t *testing.T) {
	tests := []struct {
		name     string
		deviceID string
		ok       bool
	}{
		{name: "eight chars", deviceID: "abcdefgh", ok: true},
		{name: "sixty four chars", deviceID: strings.Repeat("a", 64), ok: true},
		{name: "mixed allowed", deviceID: "A1_b-c9x", ok: true},
		{name: "empty", deviceID: "", ok: false},
		{name: "seven chars", deviceID: "abcdefg", ok: false},
		{name: "sixty five chars", deviceID: strings.Repeat("a", 65), ok: false},
		{name: "leading dash", deviceID: "-abcdefg", ok: false},
		{name: "space", deviceID: "abcd efgh", ok: false},
		{name: "non ascii", deviceID: "abcdefgé", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHubRegister("https://snet.example.com", tc.deviceID, "home", "device-token")
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestNormalizeHubServer(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "https://snet.example.com/", want: "https://snet.example.com"},
		{in: "http://127.0.0.1:8090", want: "http://127.0.0.1:8090"},
		{in: "snet.example.com", wantErr: true},
		{in: "ftp://snet.example.com", wantErr: true},
		{in: "https://", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := normalizeHubServer(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
