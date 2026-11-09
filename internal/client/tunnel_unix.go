//go:build !windows && !android && !linux

package client

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

// tunDeviceName returns the TUN adapter name used by tun.CreateTUN. macOS
// treats "utun" as a template prefix and allocates a fresh interface; linux
// creates the literal name.
func tunDeviceName() string { return "utun" }

// configureInterface assigns the private /32 address, MTU and up state on a
// freshly created TUN interface (BSD ifconfig / iproute2 syntax).
func configureInterface(iface, ip string, mtu int) error {
	if err := runCmd("ifconfig", iface, "inet", ip, "255.255.255.255", "up"); err != nil {
		return fmt.Errorf("ifconfig addr: %w", err)
	}
	if err := runCmd("ifconfig", iface, "mtu", fmt.Sprint(mtu)); err != nil {
		return fmt.Errorf("ifconfig mtu: %w", err)
	}
	return nil
}

// addHostRoute installs a /32 host route to a peer through the TUN interface.
func addHostRoute(iface, ip string) error {
	return ensureDarwinRoute(iface, ip, true)
}

// removeHostRoute removes a /32 host route.
func removeHostRoute(iface, ip string) error {
	return runCmd("route", "delete", "-host", ip, "-interface", iface)
}

// addSubnetRoute installs a subnet route through the TUN interface.
func addSubnetRoute(iface, cidr string) error {
	return ensureDarwinRoute(iface, cidr, false)
}

// ensureDarwinRoute is idempotent: an exact route already pointing at iface is
// success, an exact route on another interface is a conflict, otherwise the
// route is added.
func ensureDarwinRoute(iface, target string, host bool) error {
	if dev, ok := darwinRouteDevice(target, host); ok {
		if dev == iface {
			return nil
		}
		return fmt.Errorf("route %s already exists on %s", target, dev)
	}
	if host {
		return runCmd("route", "add", "-host", target, "-interface", iface)
	}
	return runCmd("route", "add", "-net", target, "-interface", iface)
}

// darwinRouteDevice looks up the interface for an exact route in netstat.
func darwinRouteDevice(target string, host bool) (string, bool) {
	want := target
	if host {
		want = target + "/32"
	} else if _, n, err := net.ParseCIDR(target); err == nil {
		want = n.String()
	} else {
		return "", false
	}
	out, err := exec.Command("netstat", "-rn", "-f", "inet").CombinedOutput()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		if normalizeDarwinDest(f[0]) == want {
			return f[3], true
		}
	}
	return "", false
}

// removeSubnetRoute removes a subnet route.
func removeSubnetRoute(iface, cidr string) error {
	return runCmd("route", "delete", "-net", cidr, "-interface", iface)
}

// enableIPForwarding enables IP forwarding on macOS/Linux (requires root)
// and persists the setting so it survives reboots.
func enableIPForwarding() error {
	if err := runCmd("sysctl", "-w", sysctlForwardKey+"=1"); err != nil {
		return err
	}
	persistSysctl(sysctlForwardKey, "1")
	return nil
}

// sysctlForwardKey is the OS-specific sysctl key for IP forwarding.
const sysctlForwardKey = "net.inet.ip.forwarding"

// persistSysctl appends a key=value line to /etc/sysctl.conf if not already
// present, making the setting survive reboots. Errors are silently ignored
// since this is best-effort (the live sysctl already took effect).
func persistSysctl(key, value string) {
	const path = "/etc/sysctl.conf"
	line := key + "=" + value
	data, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(data), line) {
		return // already persisted
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n%s\n", line)
}
