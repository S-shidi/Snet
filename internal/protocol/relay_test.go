package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestRelayFrameRoundTrip(t *testing.T) {
	f := RelayFrame{
		Type:        RelayTypeData,
		Flags:       3,
		SessionID:   [16]byte{1, 2, 3, 4},
		SenderID:    0x1122334455667788,
		RecipientID: 0x8877665544332211,
		Payload:     []byte("wireguard-ciphertext"),
	}
	b, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) != RelayHeaderLen+len(f.Payload) {
		t.Fatalf("wire length = %d", len(b))
	}
	got, err := ParseRelayFrame(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Type != f.Type || got.Flags != f.Flags || got.SessionID != f.SessionID ||
		got.SenderID != f.SenderID || got.RecipientID != f.RecipientID ||
		!bytes.Equal(got.Payload, f.Payload) {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestRelayFrameRejectsBadInput(t *testing.T) {
	good, _ := (RelayFrame{Type: RelayTypePing}).MarshalBinary()

	cases := []struct {
		name string
		mut  func([]byte) []byte
		err  error
	}{
		{"short", func(b []byte) []byte { return b[:RelayHeaderLen-1] }, ErrRelayShortFrame},
		{"magic", func(b []byte) []byte { b[0] = 'X'; return b }, ErrRelayBadMagic},
		{"version", func(b []byte) []byte { b[4] = 99; return b }, ErrRelayBadVersion},
		{"length", func(b []byte) []byte { b[40] = 1; b[41] = 0; return b }, ErrRelayBadLength},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseRelayFrame(tc.mut(append([]byte(nil), good...))); !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
		})
	}
	if _, err := (RelayFrame{Type: RelayTypeData, Payload: make([]byte, RelayMaxPayload+1)}).MarshalBinary(); !errors.Is(err, ErrRelayPayloadLarge) {
		t.Fatalf("oversized payload err = %v", err)
	}
}

func TestRelayNodeKeyStable(t *testing.T) {
	a := RelayNodeKey("node-123")
	if a != RelayNodeKey("node-123") {
		t.Fatal("node key is not deterministic")
	}
	if a == RelayNodeKey("node-124") {
		t.Fatal("distinct node IDs produced the same relay key")
	}
	if len(RelayNodeKeyHex("node-123")) != 16 {
		t.Fatal("node key hex must be 16 characters")
	}
}
