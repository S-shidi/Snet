package relay

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"snet/internal/protocol"
)

const (
	// TicketVersion is the only relay ticket version this build accepts.
	TicketVersion = 1
	// TicketTTL is how long a relay ticket remains valid. Clients refresh
	// before this deadline and the relay still enforces the absolute expiry.
	TicketTTL = time.Hour
	// MinSecretLen is the minimum accepted HMAC secret length. 32 bytes is
	// recommended; 16 is accepted for small self-hosted deployments.
	MinSecretLen = 16
)

var (
	ErrTicketMalformed = errors.New("relay ticket malformed")
	ErrTicketSignature = errors.New("relay ticket signature invalid")
	ErrTicketExpired   = errors.New("relay ticket expired")
	ErrTicketVersion   = errors.New("relay ticket version unsupported")
)

// TicketClaims is the signed payload carried in a relay ticket. SessionID is
// the 16-byte relay session identifier in hex. The relay uses it as the key
// for the client's current source address; NodeID/NetworkID bind the session
// to the coordination identity that requested it.
type TicketClaims struct {
	Version   int    `json:"v"`
	SessionID string `json:"sid"`
	NetworkID string `json:"nid"`
	NodeID    string `json:"node"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

// TicketSigner issues and verifies HMAC-SHA256 relay tickets. Verification
// accepts the primary secret and any rotated-out secrets, so a deployment can
// roll SNET_RELAY_SECRET without dropping live sessions.
type TicketSigner struct {
	keys [][]byte
}

// NewTicketSigner builds a signer from the primary secret and optional old
// secrets (for rotation). Empty/duplicate old secrets are ignored.
func NewTicketSigner(secret string, oldSecrets ...string) (*TicketSigner, error) {
	if len(secret) < MinSecretLen {
		return nil, fmt.Errorf("relay secret must be at least %d bytes", MinSecretLen)
	}
	s := &TicketSigner{keys: [][]byte{[]byte(secret)}}
	for _, old := range oldSecrets {
		if len(old) < MinSecretLen || string(s.keys[0]) == old {
			continue
		}
		dup := false
		for _, k := range s.keys[1:] {
			if string(k) == old {
				dup = true
				break
			}
		}
		if !dup {
			s.keys = append(s.keys, []byte(old))
		}
	}
	return s, nil
}

// Sign serializes and signs claims. It does not validate the claims beyond
// requiring a version; callers construct them through NewClaims.
func (s *TicketSigner) Sign(claims TicketClaims) (string, error) {
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.keys[0])
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks the signature and expiry, then returns the claims. It tries
// every configured secret so tickets signed before a rotation keep working.
func (s *TicketSigner) Verify(ticket string, now time.Time) (TicketClaims, error) {
	var claims TicketClaims
	bodyPart, sigPart, ok := strings.Cut(ticket, ".")
	if !ok || bodyPart == "" || sigPart == "" {
		return claims, ErrTicketMalformed
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyPart)
	if err != nil {
		return claims, ErrTicketMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil || len(sig) != sha256.Size {
		return claims, ErrTicketMalformed
	}
	valid := false
	for _, key := range s.keys {
		mac := hmac.New(sha256.New, key)
		mac.Write(body)
		if subtle.ConstantTimeCompare(mac.Sum(nil), sig) == 1 {
			valid = true
			break
		}
	}
	if !valid {
		return claims, ErrTicketSignature
	}
	if err := json.Unmarshal(body, &claims); err != nil {
		return claims, ErrTicketMalformed
	}
	if claims.Version != TicketVersion {
		return claims, ErrTicketVersion
	}
	if claims.SessionID == "" || claims.NetworkID == "" || claims.NodeID == "" {
		return claims, ErrTicketMalformed
	}
	if sid, err := hex.DecodeString(claims.SessionID); err != nil || len(sid) != 16 {
		return claims, ErrTicketMalformed
	}
	if claims.ExpiresAt <= now.Unix() {
		return claims, ErrTicketExpired
	}
	return claims, nil
}

// NewClaims builds a fresh ticket for a network/node with a random session
// ID and an absolute expiry.
func NewClaims(networkID, nodeID string, ttl time.Duration) (TicketClaims, error) {
	if networkID == "" || nodeID == "" {
		return TicketClaims{}, errors.New("relay ticket requires network and node IDs")
	}
	if ttl <= 0 {
		ttl = TicketTTL
	}
	sid, err := NewSessionID()
	if err != nil {
		return TicketClaims{}, err
	}
	now := time.Now()
	return TicketClaims{
		Version:   TicketVersion,
		SessionID: hex.EncodeToString(sid[:]),
		NetworkID: networkID,
		NodeID:    nodeID,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
	}, nil
}

// NewSessionID returns 16 cryptographically random bytes.
func NewSessionID() ([16]byte, error) {
	var sid [16]byte
	if _, err := rand.Read(sid[:]); err != nil {
		return sid, err
	}
	return sid, nil
}

// NodeKey returns the compact relay routing key for a node ID.
func NodeKey(nodeID string) uint64 {
	return protocol.RelayNodeKey(nodeID)
}
