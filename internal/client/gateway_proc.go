//go:build linux || android

package client

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// defaultGatewayIP returns the IPv4 default gateway from the Linux routing
// table (also present on Android). An empty result simply skips NAT-PMP/PCP.
func defaultGatewayIP() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		if len(f[2]) < 8 {
			return ""
		}
		raw, err := hex.DecodeString(f[2][:8])
		if err != nil {
			return ""
		}
		return fmt.Sprintf("%d.%d.%d.%d", raw[3], raw[2], raw[1], raw[0])
	}
	return ""
}