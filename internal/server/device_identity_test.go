package server

import (
	"errors"
	"net/http"
	"testing"

	"snet/internal/protocol"
)

func TestFreshRegistrationRejectsDeviceIDCollision(t *testing.T) {
	s := NewStore()
	if err := s.RegisterDevice("device-collision", testKey(1), ""); err != nil {
		t.Fatal(err)
	}
	// The same device may re-register idempotently (restart, key rotation).
	if err := s.RegisterDevice("device-collision", testKey(2), ""); err != nil {
		t.Fatalf("idempotent re-register failed: %v", err)
	}
	// A just-generated identity must never silently inherit an existing one.
	err := s.RegisterDevice("device-collision", testKey(3), "", true)
	if !errors.Is(err, ErrDeviceIDCollision) {
		t.Fatalf("fresh registration error = %v, want ErrDeviceIDCollision", err)
	}
}

func TestCreateNetworkAfterReinstallWithNewDeviceID(t *testing.T) {
	s := NewStore()
	first, err := s.CreateNetwork(testKey(1), "old-device-0001", "before-reinstall", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNetwork(testKey(1), "old-device-0001", "same-device", "", false); !errors.Is(err, ErrDeviceNetworkExists) {
		t.Fatalf("same device second create error = %v, want owner limit", err)
	}
	// Reinstall cleared every local byte, so the client presents a new random
	// deviceId. Even with the same public key (or the same NAT IP), the server
	// must treat it as a brand-new device and allow a network.
	second, err := s.CreateNetwork(testKey(1), "new-device-0002", "after-reinstall", "", false)
	if err != nil {
		t.Fatalf("new deviceId after reinstall should create: %v", err)
	}
	if second.NetworkID == "" || second.NetworkID == first.NetworkID {
		t.Fatalf("unexpected network after reinstall: %+v", second)
	}
}

func TestRecoveryTokenRequiresNodeOwnership(t *testing.T) {
	s := NewStore()
	created, err := s.CreateNetwork(testKey(1), "recovery-device-1", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueRecoveryToken("recovery-device-1", "not-a-node-token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bogus node token error = %v, want unauthorized", err)
	}
	token, err := s.IssueRecoveryToken("recovery-device-1", created.Token)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || !s.ValidateDeviceToken("recovery-device-1", token) {
		t.Fatal("recovery token was not persisted")
	}
	// Issuing again is stable, so a client can safely persist it.
	again, err := s.IssueRecoveryToken("recovery-device-1", created.Token)
	if err != nil || again != token {
		t.Fatalf("recovery token changed: %q -> %q (%v)", token, again, err)
	}
}

func TestDeviceNetworksEndpointRequiresRecoveryToken(t *testing.T) {
	ts, s := newTestServer(t)
	created, err := s.CreateNetwork(testKey(1), "recovery-device-2", "office", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var tokenResp protocol.RecoveryTokenResp
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/recovery-device-2/recovery-token", created.Token, nil, &tokenResp)
	if resp.StatusCode != http.StatusOK || tokenResp.RecoveryToken == "" {
		t.Fatalf("recovery token endpoint = %d %+v", resp.StatusCode, tokenResp)
	}

	resp = doJSON(t, http.MethodGet, ts.URL+"/api/v1/devices/recovery-device-2/networks", "", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated recovery status = %d, want 401", resp.StatusCode)
	}

	var nets protocol.DeviceNetworksResp
	resp = doJSONH(t, http.MethodGet, ts.URL+"/api/v1/devices/recovery-device-2/networks", "",
		map[string]string{"X-Device-Token": tokenResp.RecoveryToken}, nil, &nets)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recovery status = %d", resp.StatusCode)
	}
	if len(nets.Networks) != 1 || nets.Networks[0].Network.ID != created.NetworkID {
		t.Fatalf("recovered networks = %+v", nets.Networks)
	}
	if nets.Networks[0].Token == "" || nets.Networks[0].Token == created.Token {
		t.Fatal("recovery must rotate the node token")
	}
}

func TestRegisterDeviceFreshEndpointReturns400(t *testing.T) {
	ts, s := newTestServer(t)
	if err := s.RegisterDevice("collision-http-1", testKey(1), ""); err != nil {
		t.Fatal(err)
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", "",
		protocol.RegisterDeviceReq{DeviceID: "collision-http-1", PublicKey: testKey(2), Fresh: true}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("fresh collision status = %d, want 400", resp.StatusCode)
	}
}
