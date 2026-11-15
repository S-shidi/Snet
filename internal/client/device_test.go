package client

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var deviceIDFormatRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,63}$`)

func TestMain(m *testing.M) {
	// Many tests spawn several daemons in one process against the same server.
	// A shared device identity would make them all appear as one device, so
	// tests use a fresh random identity per daemon.
	deviceIDGen = func() (string, error) {
		b := make([]byte, deviceIDBytes)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		return hex.EncodeToString(b), nil
	}
	os.Exit(m.Run())
}

func TestNewDeviceIDFormat(t *testing.T) {
	id, err := newDeviceID()
	if err != nil {
		t.Fatalf("newDeviceID: %v", err)
	}
	if len(id) != deviceIDBytes*2 {
		t.Fatalf("newDeviceID length = %d, want %d", len(id), deviceIDBytes*2)
	}
	if !deviceIDFormatRe.MatchString(id) {
		t.Fatalf("newDeviceID %q fails server deviceID regex", id)
	}
}

func TestNewDeviceIDIsRandom(t *testing.T) {
	seen := make(map[string]struct{}, 64)
	for i := 0; i < 64; i++ {
		id, err := newDeviceID()
		if err != nil {
			t.Fatalf("newDeviceID: %v", err)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate random device ID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestLoadOrCreateDeviceIDFileWinsAndStaysStable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "device.id")
	stored := "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(file, []byte(stored+"\nkey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrCreateDeviceID(file, "cfg-id-000000")
	if err != nil {
		t.Fatal(err)
	}
	if id != stored {
		t.Fatalf("file should take precedence, got %q", id)
	}
	// Reading the same file again must return the exact same identity.
	again, err := LoadOrCreateDeviceID(file, "")
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("device id changed between reads: %q vs %q", id, again)
	}
}

func TestLoadOrCreateDeviceIDGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "nested", "device.id")
	id, err := LoadOrCreateDeviceID(file, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != deviceIDBytes*2 {
		t.Fatalf("generated id length = %d, want %d", len(id), deviceIDBytes*2)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != id {
		t.Fatalf("persisted id = %q, want %q", strings.TrimSpace(string(b)), id)
	}
	// A second load must not generate a new identity.
	again, err := LoadOrCreateDeviceID(file, "")
	if err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("reload changed device id: %q vs %q", id, again)
	}
}

func TestLoadOrCreateDeviceIDPersistsExistingConfigValue(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "device.id")
	existing := "fedcba9876543210fedcba9876543210"
	id, err := LoadOrCreateDeviceID(file, existing)
	if err != nil {
		t.Fatal(err)
	}
	if id != existing {
		t.Fatalf("existing config id = %q, want %q", id, existing)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != existing {
		t.Fatalf("existing config id not persisted: %q", strings.TrimSpace(string(b)))
	}
}

func TestLoadOrCreateDeviceIDEnvOverride(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "device.id")
	t.Setenv("SNET_DEVICE_ID", "deadbeefcafebabe")
	id, err := LoadOrCreateDeviceID(file, "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "deadbeefcafebabe" {
		t.Fatalf("env override = %q, want deadbeefcafebabe", id)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != id {
		t.Fatalf("persisted id = %q, want %q", strings.TrimSpace(string(b)), id)
	}
}

func TestLoadOrCreateDeviceIDReplacesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "device.id")
	if err := os.WriteFile(file, []byte("not a valid id!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := LoadOrCreateDeviceID(file, "")
	if err != nil {
		t.Fatal(err)
	}
	if !deviceIDFormatRe.MatchString(id) {
		t.Fatalf("replacement id %q is invalid", id)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != id {
		t.Fatalf("replacement id not persisted: %q", strings.TrimSpace(string(b)))
	}
}

func TestDeviceIdentityKeepsKeyAndRecoveryToken(t *testing.T) {
	file := filepath.Join(t.TempDir(), "device.id")
	if err := SaveDeviceKeypair(file, "0123456789abcdef", "privkey", "recovery-token"); err != nil {
		t.Fatal(err)
	}
	id, key, token, err := LoadDeviceIdentity(file)
	if err != nil {
		t.Fatal(err)
	}
	if id != "0123456789abcdef" || key != "privkey" || token != "recovery-token" {
		t.Fatalf("identity = %q, %q, %q", id, key, token)
	}
	legacyID, legacyKey, err := LoadDeviceKeypair(file)
	if err != nil || legacyID != id || legacyKey != key {
		t.Fatalf("LoadDeviceKeypair = %q, %q, %v", legacyID, legacyKey, err)
	}
}
