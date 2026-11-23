// SPDX-License-Identifier: BSD-3-Clause
package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.4", -1},
		{"v1.2.5", "v1.2.4", 1},
		{"1.2.9", "1.10.0", -1},
		{"1.10.0", "1.2.9", 1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCheckLatestReleaseWithFallback(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer failSrv.Close()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.2.6",
			"html_url": "https://github.com/S-shidi/Snet/releases/tag/v1.2.6",
			"published_at": "2026-11-23T12:00:00Z",
			"assets": [
				{"name": "Snet-v1.2.6-mac.dmg", "browser_download_url": "https://example.com/Snet-v1.2.6-mac.dmg"},
				{"name": "Snet-Setup-v1.2.6.exe", "browser_download_url": "https://example.com/Snet-Setup-v1.2.6.exe"},
				{"name": "SHA256SUMS-windows-v1.2.6.txt", "browser_download_url": "https://example.com/SHA256SUMS-windows-v1.2.6.txt"},
				{"name": "Snet-v1.2.6-android.apk", "browser_download_url": "https://example.com/Snet-v1.2.6-android.apk"}
			]
		}`))
	}))
	defer okSrv.Close()

	resp, err := CheckLatestRelease(context.Background(), "1.2.5", "darwin", []string{failSrv.URL, okSrv.URL})
	if err != nil {
		t.Fatalf("CheckLatestRelease failed: %v", err)
	}
	if !resp.HasUpdate || resp.LatestVersion != "1.2.6" || !resp.UpgradeSupported {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.AssetURL != "https://example.com/Snet-v1.2.6-mac.dmg" {
		t.Fatalf("AssetURL = %q", resp.AssetURL)
	}
	if resp.Assets["windows"] != "https://example.com/Snet-Setup-v1.2.6.exe" {
		t.Fatalf("windows asset = %q", resp.Assets["windows"])
	}
	if resp.Assets["android"] != "https://example.com/Snet-v1.2.6-android.apk" {
		t.Fatalf("android asset = %q", resp.Assets["android"])
	}
}

func TestDownloadAndVerifyAsset(t *testing.T) {
	// Minimal valid ZIP/APK header ("PK\x03\x04") + payload.
	payload := append([]byte{'P', 'K', 0x03, 0x04}, []byte(strings.Repeat("snet-apk-payload-", 1024))...)
	sum := sha256.Sum256(payload)
	wantSHA := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Snet-v1.2.6-android.apk":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		case "/bad.apk":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<!DOCTYPE html><html>error</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	dest := filepath.Join(tmpDir, "Snet-v1.2.6-android.apk")
	var lastBytes int64
	gotSHA, err := DownloadAndVerifyAsset(context.Background(), srv.URL+"/Snet-v1.2.6-android.apk", wantSHA, dest, func(dl, total int64) {
		lastBytes = dl
	})
	if err != nil {
		t.Fatalf("DownloadAndVerifyAsset failed: %v", err)
	}
	if gotSHA != wantSHA {
		t.Fatalf("gotSHA = %q, want %q", gotSHA, wantSHA)
	}
	if lastBytes != int64(len(payload)) {
		t.Fatalf("lastBytes = %d, want %d", lastBytes, len(payload))
	}
	if st, err := os.Stat(dest); err != nil || st.Size() != int64(len(payload)) {
		t.Fatalf("dest stat mismatch: %v", err)
	}

	// HTML error page disguised as 200 OK must be rejected.
	badDest := filepath.Join(tmpDir, "bad.apk")
	if _, err := DownloadAndVerifyAsset(context.Background(), srv.URL+"/bad.apk", "", badDest, nil); err == nil {
		t.Fatal("expected HTML payload to be rejected")
	}
}
