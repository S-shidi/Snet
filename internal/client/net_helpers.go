package client

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// portFree reports whether a UDP port is available on all local addresses.
func portFree(port int) bool {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// subnetsOverlap reports whether two CIDR networks overlap.
func subnetsOverlap(a, b string) bool {
	_, an, err := net.ParseCIDR(a)
	if err != nil {
		return true // unknown subnet: assume conflict to stay safe
	}
	_, bn, err := net.ParseCIDR(b)
	if err != nil {
		return true
	}
	return an.Contains(bn.IP) || bn.Contains(an.IP)
}

// writeFile0600 persists data with owner-only permissions using a temp file
// and rename, so a crash or power loss cannot leave a truncated identity or
// config file behind.
func writeFile0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create parent dir: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".snet-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
