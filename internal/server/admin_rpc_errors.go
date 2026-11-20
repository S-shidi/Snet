package server

import "errors"

var (
	errInternalAdminDisabled     = errors.New("internal admin RPC is disabled")
	errInternalAdminLoopbackOnly = errors.New("internal admin RPC is loopback-only")
	errInternalAdminUnauthorized = errors.New("internal admin RPC unauthorized")
)
