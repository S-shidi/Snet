package protocol

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// Relay v2 is a stateless-ticket, session-oriented UDP relay. A client sends a
// HELLO containing a control-plane-signed ticket; the relay remembers the
// source address under the ticket's session ID and forwards DATA frames only
// to the recipient named in the frame. The relay never parses WireGuard and
// never holds coordination state.
const (
	RelayMagic      = "SNR1"
	RelayVersion    = 1
	RelayHeaderLen  = 42
	RelayMaxPayload = 2048

	RelayTypeHello    = 1
	RelayTypeHelloAck = 2
	RelayTypeData     = 3
	RelayTypePing     = 4
	RelayTypePong     = 5
	RelayTypeError    = 6
	RelayTypeBye      = 7

	// Direct-path control frames. Registration and lookup travel between the
	// client and relay; probe/data/keepalive frames travel directly between
	// peers after the relay has introduced their public endpoints.
	RelayTypeDirectRegister    = 8
	RelayTypeDirectRegisterAck = 9
	RelayTypeDirectLookup      = 10
	RelayTypeDirectPeer        = 11
	RelayTypeDirectProbe       = 12
	RelayTypeDirectProbeAck    = 13
	RelayTypeDirectPing        = 14
	RelayTypeDirectPong        = 15
)

var (
	ErrRelayShortFrame   = errors.New("relay frame too short")
	ErrRelayBadMagic     = errors.New("relay frame bad magic")
	ErrRelayBadVersion   = errors.New("relay frame bad version")
	ErrRelayBadLength    = errors.New("relay frame bad payload length")
	ErrRelayPayloadLarge = errors.New("relay frame payload too large")
)

// RelayFrame is the wire representation of one Relay v2 packet.
type RelayFrame struct {
	Type        uint8
	Flags       uint8
	SessionID   [16]byte
	SenderID    uint64
	RecipientID uint64
	Payload     []byte
}

// MarshalBinary encodes the frame exactly as it appears on the wire.
func (f RelayFrame) MarshalBinary() ([]byte, error) {
	if len(f.Payload) > RelayMaxPayload {
		return nil, ErrRelayPayloadLarge
	}
	b := make([]byte, RelayHeaderLen+len(f.Payload))
	copy(b[0:4], RelayMagic)
	b[4] = RelayVersion
	b[5] = f.Type
	b[6] = f.Flags
	b[7] = 0
	copy(b[8:24], f.SessionID[:])
	binary.BigEndian.PutUint64(b[24:32], f.SenderID)
	binary.BigEndian.PutUint64(b[32:40], f.RecipientID)
	binary.BigEndian.PutUint16(b[40:42], uint16(len(f.Payload)))
	copy(b[42:], f.Payload)
	return b, nil
}

// ParseRelayFrame decodes and validates a Relay v2 packet. The returned
// payload aliases b; callers that retain it must copy it.
func ParseRelayFrame(b []byte) (RelayFrame, error) {
	if len(b) < RelayHeaderLen {
		return RelayFrame{}, ErrRelayShortFrame
	}
	if string(b[0:4]) != RelayMagic {
		return RelayFrame{}, ErrRelayBadMagic
	}
	if b[4] != RelayVersion {
		return RelayFrame{}, ErrRelayBadVersion
	}
	n := int(binary.BigEndian.Uint16(b[40:42]))
	if n > RelayMaxPayload {
		return RelayFrame{}, ErrRelayPayloadLarge
	}
	if len(b) != RelayHeaderLen+n {
		return RelayFrame{}, fmt.Errorf("%w: header says %d, got %d", ErrRelayBadLength, n, len(b)-RelayHeaderLen)
	}
	var f RelayFrame
	f.Type = b[5]
	f.Flags = b[6]
	copy(f.SessionID[:], b[8:24])
	f.SenderID = binary.BigEndian.Uint64(b[24:32])
	f.RecipientID = binary.BigEndian.Uint64(b[32:40])
	if n > 0 {
		f.Payload = b[RelayHeaderLen:]
	}
	return f, nil
}

// RelayNodeKey derives the compact 64-bit node identifier used in relay
// frames from the coordination node ID. It is not a secret; the ticket HMAC
// authenticates the session, while this key only routes frames.
func RelayNodeKey(nodeID string) uint64 {
	sum := sha256.Sum256([]byte(nodeID))
	return binary.BigEndian.Uint64(sum[:8])
}

// RelayNodeKeyHex formats a node key for JSON/control-plane messages.
func RelayNodeKeyHex(nodeID string) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], RelayNodeKey(nodeID))
	return hex.EncodeToString(b[:])
}
