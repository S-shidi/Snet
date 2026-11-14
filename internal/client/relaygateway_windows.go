//go:build windows

package client

// Windows does not currently provide a reliable NAT/IP-forwarding gateway
// implementation for the Snet delegated-subnet model. Keep the capability
// explicitly disabled so the UI hides the switch and the server never gets a
// misleading allow_relay=true report.
func setupRelayGateway(subnets []string) error {
	return ErrRelayGatewayUnsupported
}

func relayGatewaySupported() bool { return false }
