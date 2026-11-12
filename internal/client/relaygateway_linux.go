//go:build linux

package client

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// iptablesSearchDirs are the directories probed for the iptables binary when
// it is not found through PATH.
var iptablesSearchDirs = []string{"/usr/sbin", "/sbin", "/usr/local/sbin", "/bin", "/usr/bin", "/usr/local/bin"}

// iptablesNames are the binary names to try, in order of preference.
var iptablesNames = []string{"iptables", "iptables-legacy", "iptables-nft"}

// iptablesBin resolves the iptables binary to use.
//
// The lookup must not rely on PATH alone: the daemon runs under supervisord
// inside a container, and the environment it inherits does not always
// contain the sbin directories even though the binary is installed there
// (a container runtime or the DSM UI may replace the PATH). The well-known
// locations are therefore probed explicitly.
//
// Distributions ship either the legacy backend, the nft backend, or a
// wrapper that picks one, so all common names are tried.
func iptablesBin() string {
	for _, name := range iptablesNames {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, dir := range iptablesSearchDirs {
		for _, name := range iptablesNames {
			p := filepath.Join(dir, name)
			st, err := os.Stat(p)
			if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
				continue
			}
			return p
		}
	}
	return ""
}

// iptablesRuleExists reports whether the given rule is already installed.
// iptables exits with status 1 when the rule is missing and with 2, 3 or 4
// on a real failure, so only a non-1 exit code is treated as an error.
func iptablesRuleExists(table, chain string, args []string) (bool, error) {
	bin := iptablesBin()
	if bin == "" {
		return false, errors.New("未找到 iptables")
	}
	full := append([]string{"-t", table, "-C", chain}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("iptables %s: %w", strings.Join(full, " "), err)
}

// iptablesAdd appends the given rule to the chain.
func iptablesAdd(table, chain string, args []string) error {
	bin := iptablesBin()
	if bin == "" {
		return errors.New("未找到 iptables")
	}
	full := append([]string{"-t", table, "-A", chain}, args...)
	out, err := exec.Command(bin, full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables %s: %w (%s)", strings.Join(full, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureIptablesRule installs the rule once. Re-running it is harmless: the
// rule is checked first, so a daemon restart does not pile up duplicates.
func ensureIptablesRule(table, chain string, args []string) error {
	exists, err := iptablesRuleExists(table, chain, args)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return iptablesAdd(table, chain, args)
}

// currentIPForward reads the effective IPv4 forwarding setting.
func currentIPForward() string {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Test seams for the forwarding setup. They keep the read-only /proc/sys
// case (common on container hosts) coverable without touching the real
// kernel settings.
var (
	ipForwardReadFn    = currentIPForward
	ipForwardWriteFn   = enableIPForwarding
	ipForwardPersistFn = persistSysctl
)

// ensureIPForwarding turns IPv4 forwarding on.
//
// Writing the sysctl fails on hosts that mount /proc/sys read-only (some
// container runtimes, Synology), even when forwarding is already enabled. An
// already-enabled value is therefore accepted as success: the relay gateway
// only needs forwarding to be on, not to have been written by us.
func ensureIPForwarding() error {
	if ipForwardReadFn() == "1" {
		ipForwardPersistFn("net.ipv4.ip_forward", "1")
		return nil
	}
	if err := ipForwardWriteFn(); err != nil {
		// The write may have failed for a reason unrelated to the value
		// (read-only /proc/sys). Re-read: forwarding may already be on.
		if ipForwardReadFn() == "1" {
			return nil
		}
		return err
	}
	return nil
}

// setupRelayGateway prepares this host to forward traffic between the
// WireGuard mesh and the local LAN.
//
// Three things are needed for a delegated subnet to be reachable through this
// node:
//
//  1. IPv4 forwarding, so packets handed over by the kernel are actually
//     routed onwards.
//  2. A FORWARD accept rule for the tunnel subnet in both directions,
//     otherwise the host firewall drops the relayed packets.
//  3. A NAT MASQUERADE rule, because the source address of a relayed packet
//     is a mesh address (e.g. 10.88.1.7) that the LAN cannot route back to.
//
// The rules are idempotent: they are checked before they are added.
func setupRelayGateway(subnets []string) error {
	if err := ensureIPForwarding(); err != nil {
		return fmt.Errorf("开启 IP 转发失败: %w", err)
	}
	for _, raw := range subnets {
		sub := strings.TrimSpace(raw)
		if sub == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(sub); err != nil {
			return fmt.Errorf("网段 %q 无效: %w", sub, err)
		}
		if err := ensureIptablesRule("filter", "FORWARD", []string{"-s", sub, "-j", "ACCEPT"}); err != nil {
			return fmt.Errorf("配置 FORWARD 规则失败 (%s): %w", sub, err)
		}
		if err := ensureIptablesRule("filter", "FORWARD", []string{"-d", sub, "-j", "ACCEPT"}); err != nil {
			return fmt.Errorf("配置 FORWARD 规则失败 (%s): %w", sub, err)
		}
		if err := ensureIptablesRule("nat", "POSTROUTING", []string{"-s", sub, "-j", "MASQUERADE"}); err != nil {
			return fmt.Errorf("配置 MASQUERADE 规则失败 (%s): %w", sub, err)
		}
	}
	return nil
}
