//go:build linux

package client

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// iptablesBin resolves the iptables binary to use. Distributions ship
// either the legacy backend, the nft backend, or a wrapper that picks one,
// so all common names are tried.
func iptablesBin() string {
	for _, name := range []string{"iptables", "iptables-legacy", "iptables-nft"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
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
	if err := enableIPForwarding(); err != nil {
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
