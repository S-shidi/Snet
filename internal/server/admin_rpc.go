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

// NewCoordinationHandler serves the coordination API only. Admin routes stay
// private on NewHandler and are reached through NewInternalAdminHandler; they
// are not mounted on the public coordination listener.
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
		if !internalAdminRemoteAllowed(r.RemoteAddr, opts.InternalAdminAllowCIDRs) {
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
	return internalAdminRemoteAllowed(remoteAddr, nil)
}

// internalAdminRemoteAllowed accepts loopback unconditionally, plus any peer
// inside one of the configured private CIDRs. The service token is still
// required by the caller, so this only widens the transport boundary to a
// trusted private network (e.g. a Compose network).
func internalAdminRemoteAllowed(remoteAddr string, allowCIDRs []string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, raw := range allowCIDRs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
