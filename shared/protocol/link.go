package protocol

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// inviteServerKey is an application-level key for the server reference carried
// by invite links. It prevents the coordination URL from being displayed as
// plaintext in a copied link. It is not a substitute for server-side trust:
// every client that can join must be able to decrypt it.
var inviteServerKey = sha256.Sum256([]byte("snet invite server envelope v1"))

const inviteServerAAD = "snet-invite-server-v1"

func encryptInviteServer(server string) string {
	block, err := aes.NewCipher(inviteServerKey[:])
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return ""
	}
	// Derive a deterministic nonce from the plaintext. The server URL is the
	// only value that can share a nonce, so AES-GCM nonce reuse here never
	// encrypts two different plaintexts under the same key/nonce.
	n := sha256.Sum256([]byte("snet-invite-server-nonce:" + server))
	nonce := n[:gcm.NonceSize()]
	sealed := gcm.Seal(nil, nonce, []byte(server), []byte(inviteServerAAD))
	return base64.RawURLEncoding.EncodeToString(append(append([]byte(nil), nonce...), sealed...))
}

func decryptInviteServer(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return "", fmt.Errorf("invalid server token: %w", err)
	}
	block, err := aes.NewCipher(inviteServerKey[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("invalid server token: too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(inviteServerAAD))
	if err != nil {
		return "", fmt.Errorf("invalid server token: %w", err)
	}
	return string(plain), nil
}

// NormalizeCode removes grouping dashes and lowercases, returns uppercase.
func NormalizeCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", ""))
}

// BuildLink returns a snet:// link carrying the network id, pairing code and,
// when server is non-empty, the coordination server address the invite belongs
// to. A server-less link is still valid: the joiner targets its bound server.
// When name is non-empty, it is included as a display hint for the joiner.
func BuildLink(nid, code, server string, name string) string {
	link := "snet://join?nid=" + url.QueryEscape(nid) + "&code=" + url.QueryEscape(NormalizeCode(code))
	if name != "" {
		link += "&name=" + url.QueryEscape(name)
	}
	if server != "" {
		if token := encryptInviteServer(server); token != "" {
			link += "&s=" + url.QueryEscape(token)
		}
	}
	return link
}

// ParseLink extracts nid, code, an optional server and an optional name from a
// snet:// link, an http(s) join URL, or a bare "nid:code" string. server and
// name are empty for links that omit them.
func ParseLink(s string) (nid, code, server, name string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", "", "", fmt.Errorf("empty input")
	}
	if u, e := url.Parse(s); e == nil && (u.Scheme == "snet" || u.Scheme == "http" || u.Scheme == "https") {
		q := u.Query()
		nid = q.Get("nid")
		code = NormalizeCode(q.Get("code"))
		if nid == "" || code == "" {
			return "", "", "", "", fmt.Errorf("invalid link: missing nid or code")
		}
		server := q.Get("server")
		if token := q.Get("s"); token != "" {
			var err error
			server, err = decryptInviteServer(token)
			if err != nil {
				return "", "", "", "", err
			}
		}
		return nid, code, server, q.Get("name"), nil
	}
	if i := strings.IndexByte(s, ':'); i > 0 {
		return s[:i], NormalizeCode(s[i+1:]), "", "", nil
	}
	return "", "", "", "", fmt.Errorf("invalid link format %q", s)
}

// ParseInviteLink is the strict variant of ParseLink used by the user-facing
// join path: only a snet://, http:// or https:// URL is accepted. The bare
// "nid:code" shortcut is rejected on purpose, so joining a network always
// goes through an invite link.
func ParseInviteLink(s string) (nid, code, server, name string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", "", "", fmt.Errorf("empty invite link")
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "snet://") && !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", "", "", "", fmt.Errorf("invite link must be a snet:// URL")
	}
	return ParseLink(s)
}
