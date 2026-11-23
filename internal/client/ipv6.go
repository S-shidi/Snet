package client

import (
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

// advertisedIPv6 lets a platform shell (Android) inject the global IPv6 address
// of the underlying network. Go's interface enumeration can miss it while a
// VPN is active, which disabled the whole IPv6 direct path on Android.
var advertisedIPv6 atomic.Value

var lastV6Dump atomic.Int64

// SetAdvertisedIPv6 overrides the address published as this node's IPv6 direct
// endpoint. Empty clears the override and falls back to interface enumeration.
func SetAdvertisedIPv6(addr string) {
	advertisedIPv6.Store(strings.TrimSpace(addr))
}

func advertisedIPv6Value() string {
	v, _ := advertisedIPv6.Load().(string)
	if v == "" {
		return ""
	}
	ip := net.ParseIP(strings.Trim(v, "[]"))
	if ip == nil || !isPublicIPv6(ip) {
		return ""
	}
	return ip.String()
}

// dumpInterfaces logs the interface/address view once per five minutes when no
// global IPv6 was found, so an exported log shows why the path is unavailable.
func dumpInterfaces() {
	now := time.Now().Unix()
	if now-lastV6Dump.Load() < 300 {
		return
	}
	lastV6Dump.Store(now)
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("ipv6: list interfaces: %v", err)
		return
	}
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		list := make([]string, 0, len(addrs))
		for _, a := range addrs {
			list = append(list, a.String())
		}
		log.Printf("ipv6: iface=%s up=%v addrs=%v", ifc.Name, ifc.Flags&net.FlagUp != 0, list)
	}
}
