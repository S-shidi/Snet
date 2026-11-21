package server

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"

	"snet/internal/buildinfo"
	"snet/internal/protocol"
)

const internalAdminPrefix = "/internal/admin"

// NewCoordinationHandler serves the coordination API only. The legacy admin
// routes stay available through NewHandler for cmd/server, but are not mounted
// here.
func NewCoordinationHandler(s *Store, opts Options) http.Handler {
	base := NewHandler(s, opts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/admin") {
			http.NotFound(w, r)
			return
		}
		base.ServeHTTP(w, r)
	})
}

// NewInternalAdminHandler exposes the admin data routes under
// /internal/admin/* for a loopback-only service client. Authentication is a
// dedicated service token, not an administrator browser session.
func NewInternalAdminHandler(s *Store, opts Options, token string) http.Handler {
	if token == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusServiceUnavailable, errInternalAdminDisabled)
		})
	}
	// The wrapped handler keeps its admin routes private; the internal prefix
	// is rewritten to the existing /admin data routes after both checks pass.
	opts.AdminToken = token
	base := NewHandler(s, opts)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackRemote(r.RemoteAddr) {
			writeErr(w, http.StatusForbidden, errInternalAdminLoopbackOnly)
			return
		}
		got := r.Header.Get("X-Internal-Admin-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeErr(w, http.StatusForbidden, errInternalAdminUnauthorized)
			return
		}
		if r.URL.Path == internalAdminPrefix+"/health" {
			if r.Method != http.MethodGet {
				writeErr(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
				return
			}
			snap := s.HealthSnapshot()
			writeJSON(w, http.StatusOK, protocol.SnetServHealth{
				ServerID:    s.ServerID(),
				SnetServURL: s.SnetServURL(),
				Version:     buildinfo.Version,
				APIVersion:  protocol.APIVersion,
				StartedAt:   snap.StartedAt,
				Networks:    snap.Networks,
				Devices:     snap.Devices,
				Nodes:       snap.Nodes,
			})
			return
		}
		path := r.URL.Path
		if path != internalAdminPrefix && !strings.HasPrefix(path, internalAdminPrefix+"/") {
			http.NotFound(w, r)
			return
		}
		suffix := strings.TrimPrefix(path, internalAdminPrefix)
		if suffix == "" || suffix == "/" ||
			suffix == "/login" || suffix == "/logout" ||
			suffix == "/bootstrap" || suffix == "/password" {
			http.NotFound(w, r)
			return
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = "/admin" + suffix
		clone.Header = r.Header.Clone()
		clone.Header.Set("Authorization", "Bearer "+token)
		base.ServeHTTP(w, clone)
	})
}

func loopbackRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
