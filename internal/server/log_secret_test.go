package server

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"
)

func TestSanitizeLogEscapesControlChars(t *testing.T) {
	got := sanitizeLog("a\nb\x7fc\x00d")
	want := `a\x0ab\x7fc\x00d`
	if got != want {
		t.Fatalf("sanitizeLog = %q, want %q", got, want)
	}
	if got := sanitizeLog("normal/path"); got != "normal/path" {
		t.Fatalf("sanitizeLog changed safe path: %q", got)
	}
}

func TestServerLogsDoNotContainSecrets(t *testing.T) {
	old := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	ts, _ := newTestServer(t)

	// 401 path: the bearer token must never appear in the access log.
	doJSON(t, http.MethodGet, ts.URL+"/api/v1/networks/FOO/peers", "SECRET_NODE_TOKEN_MARKER", nil, nil)

	// Bad JSON path.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/networks", strings.NewReader("{bad json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Invalid subnet triggers handleStoreErr's internal-error log; the body
	// also carries secret-shaped fields that must not be logged.
	doJSON(t, http.MethodPost, ts.URL+"/api/v1/networks", "", map[string]any{
		"publicKey":   testKey(3),
		"subnet":      "8.8.8.0/24",
		"privateKey":  "PRIVATE_KEY_MARKER",
		"pairingCode": "PAIRING_CODE_MARKER",
	}, nil)

	logs := buf.String()
	if !strings.Contains(logs, "GET /api/v1/networks/FOO/peers") {
		t.Fatalf("access log missing expected path: %q", logs)
	}
	if !strings.Contains(logs, "internal error:") {
		t.Fatalf("internal error path was not logged: %q", logs)
	}
	for _, secret := range []string{
		"SECRET_NODE_TOKEN_MARKER",
		"PRIVATE_KEY_MARKER",
		"PAIRING_CODE_MARKER",
		"privateKey",
		"pairingCode",
	} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain secret marker %q: %q", secret, logs)
		}
	}
}
