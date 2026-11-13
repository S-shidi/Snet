package protocol

import "testing"

func TestParseInviteLinkAcceptsURLs(t *testing.T) {
	nid, code, server, name, err := ParseInviteLink("snet://join?nid=ABCDEFGH&code=abcd-efgh-ijkl&server=https://s.example:8090&name=office")
	if err != nil {
		t.Fatalf("ParseInviteLink: %v", err)
	}
	if nid != "ABCDEFGH" {
		t.Fatalf("nid = %q", nid)
	}
	if code != "ABCDEFGHIJKL" {
		t.Fatalf("code = %q", code)
	}
	if server != "https://s.example:8090" {
		t.Fatalf("server = %q", server)
	}
	if name != "office" {
		t.Fatalf("name = %q", name)
	}
}

func TestParseInviteLinkRejectsNonURL(t *testing.T) {
	// The bare "nid:code" shortcut is not an invite link: joining a network
	// always goes through a URL.
	if _, _, _, _, err := ParseInviteLink("ABCDEFGH:ABCDEFGHIJKL"); err == nil {
		t.Fatal("bare nid:code accepted, want error")
	}
	if _, _, _, _, err := ParseInviteLink(""); err == nil {
		t.Fatal("empty link accepted, want error")
	}
	if _, _, _, _, err := ParseInviteLink("snet://join?nid=ABCDEFGH"); err == nil {
		t.Fatal("link without code accepted, want error")
	}
}
