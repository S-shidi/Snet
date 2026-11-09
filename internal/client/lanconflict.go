package client

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// physicalSubnetsFn is the source of the machine's real (non-tunnel) IPv4
// subnets. Tests override it so attach can be exercised without depending on
// the host's routing table.
var physicalSubnetsFn = physicalSubnets

// physicalSubnets returns the IPv4 CIDRs of the machine's real interfaces. It
// prefers the OS routing table and falls back to interface addresses when the
// route command is unavailable.
func physicalSubnets() []string {
	var out []string
	switch runtime.GOOS {
	case "linux":
		out = linuxRouteSubnets()
	case "darwin":
		out = darwinRouteSubnets()
	}
	if len(out) == 0 {
		out = interfaceSubnets()
	}
	return out
}

// interfaceSubnets enumerates up, non-loopback, non-point-to-point interfaces
// and returns their IPv4 CIDRs, excluding virtual/tunnel/bridge interfaces.
func interfaceSubnets() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		if isVirtualIface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			cidr := ipNetCIDR(ipNet)
			if cidr == "" {
				continue
			}
			if _, dup := seen[cidr]; dup {
				continue
			}
			seen[cidr] = struct{}{}
			out = append(out, cidr)
		}
	}
	return out
}

// linuxRouteSubnets parses `ip -4 route show` and keeps connected routes on
// real interfaces (it drops default routes and tun/wg/docker/bridge devices).
func linuxRouteSubnets() []string {
	out, err := exec.Command("ip", "-4", "route", "show").CombinedOutput()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var result []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[0] == "default" || !strings.Contains(f[0], "/") {
			continue
		}
		if _, _, err := net.ParseCIDR(f[0]); err != nil {
			continue
		}
		dev := ""
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "dev" {
				dev = f[i+1]
				break
			}
		}
		if dev == "" || isVirtualIface(dev) {
			continue
		}
		if _, dup := seen[f[0]]; dup {
			continue
		}
		seen[f[0]] = struct{}{}
		result = append(result, f[0])
	}
	return result
}

// darwinRouteSubnets parses `netstat -rn -f inet` and keeps destinations on
// real interfaces.
func darwinRouteSubnets() []string {
	out, err := exec.Command("netstat", "-rn", "-f", "inet").CombinedOutput()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var result []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] == "Destination" || f[0] == "default" {
			continue
		}
		if isVirtualIface(f[3]) {
			continue
		}
		cidr := normalizeDarwinDest(f[0])
		if cidr == "" {
			continue
		}
		if _, dup := seen[cidr]; dup {
			continue
		}
		seen[cidr] = struct{}{}
		result = append(result, cidr)
	}
	return result
}

// normalizeDarwinDest turns a netstat destination into a CIDR. macOS prints
// connected networks classfully (e.g. "192.168.1" for a /24), so short forms
// are expanded.
func normalizeDarwinDest(dest string) string {
	if strings.Contains(dest, "/") {
		if _, n, err := net.ParseCIDR(dest); err == nil {
			return n.String()
		}
		return ""
	}
	ip := net.ParseIP(dest)
	if ip == nil || ip.To4() == nil {
		return ""
	}
	switch len(strings.Split(dest, ".")) {
	case 4:
		return ip.String() + "/32"
	case 3:
		return dest + ".0/24"
	case 2:
		return dest + ".0.0/16"
	case 1:
		return dest + ".0.0.0/8"
	}
	return ""
}

// checkLANConflict rejects a virtual subnet that overlaps a real local subnet.
// Advanced users can bypass it with SNET_IGNORE_LAN_CONFLICT=1.
func checkLANConflict(subnet string) error {
	if subnet == "" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SNET_IGNORE_LAN_CONFLICT"))) {
	case "1", "true", "yes":
		return nil
	}
	for _, lan := range physicalSubnetsFn() {
		if subnetsOverlap(subnet, lan) {
			return fmt.Errorf("网段 %s 与本机物理局域网冲突，无法加入", subnet)
		}
	}
	return nil
}
