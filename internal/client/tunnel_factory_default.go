//go:build !android

package client

// createTunnelForPlatform creates a WireGuard tunnel (non-Android).
func createTunnelForPlatform(_ int, privKeyHex, ip string, port, mtu int, networkID string) (*Tunnel, error) {
	return NewTunnel(privKeyHex, ip, port, mtu, networkID)
}
