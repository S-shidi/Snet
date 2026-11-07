package relay

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestTicketSignVerify(t *testing.T) {
	s, err := NewTicketSigner(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := NewClaims("net-1", "node-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := s.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Verify(ticket, time.Now())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.NodeID != claims.NodeID || got.NetworkID != claims.NetworkID || got.SessionID != claims.SessionID {
		t.Fatalf("claims mismatch: %+v vs %+v", got, claims)
	}
}

func TestTicketRejectsTamperExpiryAndRotation(t *testing.T) {
	s, _ := NewTicketSigner(testSecret)
	claims, _ := NewClaims("net-1", "node-1", time.Hour)
	ticket, _ := s.Sign(claims)

	body, sig, _ := strings.Cut(ticket, ".")
	tampered := body + "x." + sig
	if _, err := s.Verify(tampered, time.Now()); !errors.Is(err, ErrTicketMalformed) && !errors.Is(err, ErrTicketSignature) {
		t.Fatalf("tampered err = %v", err)
	}

	expired := claims
	expired.ExpiresAt = time.Now().Add(-time.Second).Unix()
	expiredTicket, _ := s.Sign(expired)
	if _, err := s.Verify(expiredTicket, time.Now()); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired err = %v", err)
	}

	oldSigner, _ := NewTicketSigner("abcdef0123456789abcdef0123456789")
	oldTicket, _ := oldSigner.Sign(claims)
	if _, err := s.Verify(oldTicket, time.Now()); !errors.Is(err, ErrTicketSignature) {
		t.Fatalf("foreign secret err = %v", err)
	}
	rotated, err := NewTicketSigner("fedcba9876543210fedcba9876543210", testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.Verify(ticket, time.Now()); err != nil {
		t.Fatalf("rotated signer must accept old ticket: %v", err)
	}
}

func TestTicketRejectsShortSecret(t *testing.T) {
	if _, err := NewTicketSigner("short"); err == nil {
		t.Fatal("short secret accepted")
	}
}
