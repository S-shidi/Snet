//go:build darwin

package client

import (
	"os/exec"
	"strings"
)

// defaultGatewayIP returns the IPv4 default gateway from the BSD routing
// table as reported by netstat. An empty result simply skips NAT-PMP/PCP.
func defaultGatewayIP() string {
	out, err := exec.Command("/usr/sbin/netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "default" {
			if strings.Contains(f[1], ":") {
				continue
			}
			return f[1]
		}
	}
	return ""
}
