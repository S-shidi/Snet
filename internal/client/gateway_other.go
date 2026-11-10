//go:build !linux && !android && !darwin

package client

// defaultGatewayIP is best-effort on other platforms; an empty result simply
// skips NAT-PMP/PCP. UPnP discovery does not need the gateway address, so
// mapping still works anywhere the router answers SSDP.
func defaultGatewayIP() string {
	return ""
}