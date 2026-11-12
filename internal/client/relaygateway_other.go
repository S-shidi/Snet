//go:build !linux

package client

// setupRelayGateway reports that the relay gateway mode is unavailable on
// this platform. The daemon reports the failure to the coordination server,
// so the routes delegated to this node stay pending instead of silently
// blackholing traffic.
func setupRelayGateway(subnets []string) error {
	return ErrRelayGatewayUnsupported
}
