//go:build android

package client

// setupRelayGateway reports that the relay gateway mode is unavailable on
// Android. A phone can join networks and use delegated routes, but it is not a
// stable public forwarding gateway.
func setupRelayGateway(subnets []string) error {
	return ErrRelayGatewayUnsupported
}

func relayGatewaySupported() bool { return false }
