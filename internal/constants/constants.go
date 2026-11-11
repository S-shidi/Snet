// Package constants holds build-time invariants shared by the client daemon,
// control API and UI layers.
package constants

// DefaultServer is the official SnetServ coordination server used when the
// user has not configured a custom server address.
const DefaultServer = "https://snet.uizhi.eu.org:8090"

// DefaultServerAddr is kept as a compatibility alias for older callers.
const DefaultServerAddr = DefaultServer
