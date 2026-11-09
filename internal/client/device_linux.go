//go:build linux && !android

package client

import (
	"os"
	"strings"
)

// runningInContainer reports whether this process runs inside a container.
//
// In a container the host's DMI product UUID and /etc/machine-id are shared by
// every deployment (and a base image may bake in a fixed machine-id). Deriving
// the device identity from them would give unrelated clients the same ID, so
// one client could impersonate another. Containers therefore use a random,
// persisted identity instead.
func runningInContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	if b, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := string(b)
		for _, marker := range []string{"docker", "kubepods", "containerd", "libpod", "lxc"} {
			if strings.Contains(s, marker) {
				return true
			}
		}
	}
	return false
}

// hardwareID returns the machine's stable hardware identifier. It prefers the
// DMI product UUID (stable across OS reinstalls) and falls back to
// /etc/machine-id when the DMI data is not exposed (e.g. VMs). Inside a
// container it returns "" so newDeviceID generates a random, persisted ID
// rather than copying the host's shared identifier.
func hardwareID() (string, error) {
	if runningInContainer() {
		return "", nil
	}
	if b, err := os.ReadFile("/sys/class/dmi/id/product_uuid"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	if b, err := os.ReadFile("/etc/machine-id"); err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	return "", nil
}
