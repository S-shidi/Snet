package client

import (
	"strings"
	"testing"

	"snet/internal/protocol"
)

// TestInviteLinkEmbedsCustomServer checks that an invite link points at the
// coordination server the joiner has to reach, but only when that server is
// not the official one (an official link stays short).
func TestInviteLinkEmbedsCustomServer(t *testing.T) {
	custom := &Daemon{cfg: &Config{ServerAddr: "https://custom.example:8090"}}
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
	if strings.Contains(link, "server=") {
		t.Fatalf("official link carries a server: %q", link)
	}
	if !strings.HasPrefix(link, "snet://join?nid=") {
		t.Fatalf("official link = %q", link)
	}
}
