package client

import (
	"errors"
	"strings"
)

// ErrRelayGatewayUnsupported is returned on platforms where the relay
// gateway (IP forwarding + NAT masquerading) cannot be configured. The
// daemon turns it into a reported error instead of advertising a gateway
// that would silently drop forwarded traffic.
var ErrRelayGatewayUnsupported = errors.New("当前系统不支持中继网关模式")

// relayGatewaySubnets returns the VPN subnets that the relay gateway must
// forward and masquerade. Every active network contributes its own subnet:
// packets that arrive from the mesh carry a source address inside that
// subnet, and the LAN cannot answer such an address without NAT.
func relayGatewaySubnets(nets map[string]*NetworkCfg) []string {
	out := make([]string, 0, len(nets))
	seen := map[string]bool{}
	for _, nc := range nets {
		if nc == nil || !nc.Active {
			continue
		}
		sub := strings.TrimSpace(nc.Subnet)
		if sub == "" || seen[sub] {
			continue
		}
		seen[sub] = true
		out = append(out, sub)
	}
	return out
}

// describeRelayGatewayError turns a gateway setup failure into a short,
// user-facing message. It keeps the original cause so the owner can see why
// a delegated route stays pending.
func describeRelayGatewayError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
