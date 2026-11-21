package client

import (
	"strings"
	"testing"

	"snet/internal/protocol"
)

// TestInviteLinkEmbedsCustomServer checks that every invite link carries the
// coordination server reference, encrypted, so the joiner reaches the right
// network server without exposing its URL as plaintext.
func TestInviteLinkEmbedsCustomServer(t *testing.T) {
	custom := &Daemon{cfg: &Config{Networks: map[string]*NetworkCfg{
		"ABCDEFGH": {SnetServURL: "https://custom.example:8090"},
	}}}
	link := custom.inviteLinkLocked("ABCDEFGH", "abcd1234", "office")
	nid, code, server, name, err := protocol.ParseInviteLink(link)
	if err != nil {
		t.Fatalf("ParseInviteLink(%q): %v", link, err)
	}
	if nid != "ABCDEFGH" || code != "ABCD1234" || name != "office" {
		t.Fatalf("link = %q/%q/%q", nid, code, name)
	}
	if server != "https://custom.example:8090" {
		t.Fatalf("link server = %q, want the custom one", server)
	}

	official := &Daemon{cfg: &Config{}}
	link = official.inviteLinkLocked("ABCDEFGH", "abcd1234", "office")
	if strings.Contains(link, "server=") || strings.Contains(link, "https://") {
		t.Fatalf("official link exposes its server: %q", link)
	}
	if !strings.HasPrefix(link, "snet://join?nid=") {
		t.Fatalf("official link = %q", link)
	}
	_, _, officialServer, _, err := protocol.ParseInviteLink(link)
	if err != nil || officialServer != "https://snet.uizhi.eu.org:8090" {
		t.Fatalf("official link server = %q, err=%v", officialServer, err)
	}
}
