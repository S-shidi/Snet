//go:build linux && !android

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
//
// The nf_tables backend comes first because it is the default on modern
// kernels. The legacy xtables backend is the fallback for kernels without
// nf_tables support: on such a kernel the nf_tables binary exists but every
// command fails with "Table does not exist" and exit status 4, which is
// exactly what an older NAS kernel does.
var iptablesNames = []string{"iptables", "iptables-nft", "iptables-legacy"}

// iptablesProbeErr keeps the diagnostic of the last failed backend probe so
// the error message can explain why no usable backend was found.
var iptablesProbeErr string

// iptablesBin resolves a usable iptables binary.
//
// Three things matter:
//
//   - The binary must not be found through PATH alone: the daemon runs under
//     supervisord inside a container, and the environment it inherits does
//     not always contain the sbin directories even though the binary is
//     installed there (a container runtime or the DSM UI may replace the
//     PATH). The well-known locations are therefore probed explicitly.
//   - The first existing binary is not necessarily usable: the nf_tables
//     backend needs kernel support that older NAS kernels lack, and it then
//     fails on every command. Each candidate is probed against the kernel
//     and the first working one wins.
//   - The chosen backend must be the one the host firewall uses, otherwise
//     the ACCEPT rule lands in a rule set the kernel never consults for the
//     forwarded packets.
func iptablesBin() string {
	iptablesProbeErr = ""
	for _, cand := range iptablesCandidates() {
		if iptablesUsable(cand) {
			return cand
		}
	}
	return ""
}

// iptablesCandidates lists the binaries to probe, without duplicates.
func iptablesCandidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, name := range iptablesNames {
		if p, err := exec.LookPath(name); err == nil {
			add(p)
		}
	}
	for _, dir := range iptablesSearchDirs {
		for _, name := range iptablesNames {
			p := filepath.Join(dir, name)
			st, err := os.Stat(p)
			if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
				continue
			}
			add(p)
		}
	}
	return out
}

// iptablesUsable reports whether the binary can talk to the kernel. Listing
// a chain is a harmless way to find out: it fails with exit status 4 when
// the backend is not supported by the kernel.
func iptablesUsable(bin string) bool {
	out, err := exec.Command(bin, "-t", "filter", "-S").CombinedOutput()
	if err == nil {
		return true
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	iptablesProbeErr = bin + ": " + msg
	return false
}

// iptablesRuleExists reports whether the given rule is already installed.
// iptables exits with status 1 when the rule is missing and with 2, 3 or 4
// on a real failure, so only a non-1 exit code is treated as an error.
func iptablesRuleExists(table, chain string, args []string) (bool, error) {
	bin := iptablesBin()
	if bin == "" {
		if iptablesProbeErr != "" {
			return false, fmt.Errorf("未找到可用的 iptables（%s）", iptablesProbeErr)
		}
		return false, errors.New("未找到可用的 iptables（nf_tables 与 legacy 后端均不可用）")
	}
	full := append([]string{"-t", table, "-C", chain}, args...)
	// The output is kept: iptables explains the failure on stderr, and that
	// text is the only useful diagnostic when the backend cannot be used.
	out, err := exec.Command(bin, full...).CombinedOutput()
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		// Exit status 1 means the rule is simply not installed yet.
		return false, nil
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	}
	return false, fmt.Errorf("iptables %s: %w (%s)", strings.Join(full, " "), err, msg)
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

func relayGatewaySupported() bool { return true }
