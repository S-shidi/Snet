//go:build darwin

package client

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"
)

// The macOS gateway uses PF instead of iptables. Rules are kept in a private
// anchor below com.apple so the system's default /etc/pf.conf (which already
// contains the wildcard anchors) evaluates them without replacing the user's
// firewall configuration. Loading the anchor is idempotent: every call
// replaces exactly this anchor.
const relayPFAnchor = "com.apple/snet"

var pfInterfaceRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// defaultRouteInterface returns the interface used by the IPv4 default route.
func defaultRouteInterface() string {
	out, err := exec.Command("/sbin/route", "-n", "get", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "interface:" && pfInterfaceRe.MatchString(f[1]) {
			return f[1]
		}
	}
	return ""
}

// pfGatewayRules builds the PF rules for the delegated subnets. Keeping this
// pure makes the rule set testable without touching a real PF instance.
func pfGatewayRules(iface string, subnets []string) string {
	var b strings.Builder
	// PF requires translation (nat) rules before filtering (pass) rules.
	for _, raw := range subnets {
		sub := strings.TrimSpace(raw)
		if sub == "" {
			continue
		}
		fmt.Fprintf(&b, "nat on %s inet from %s to any -> (%s)\n", iface, sub, iface)
	}
	for _, raw := range subnets {
		sub := strings.TrimSpace(raw)
		if sub == "" {
			continue
		}
		fmt.Fprintf(&b, "pass in quick inet from %s to any keep state\n", sub)
		fmt.Fprintf(&b, "pass out quick inet from %s to any keep state\n", sub)
		fmt.Fprintf(&b, "pass in quick inet from any to %s keep state\n", sub)
		fmt.Fprintf(&b, "pass out quick inet from any to %s keep state\n", sub)
	}
	return b.String()
}

func ensureIPForwardingDarwin() error {
	out, err := exec.Command("/usr/sbin/sysctl", "-w", "net.inet.ip.forwarding=1").CombinedOutput()
	if err != nil {
		return fmt.Errorf("开启 IP 转发失败: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func cleanPFOutput(out string) string {
	var keep []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.Contains(trimmed, "Use of -f option"):
			continue
		case strings.Contains(trimmed, "present in the main ruleset"):
			continue
		case strings.HasPrefix(trimmed, "See /etc/pf.conf"):
			continue
		case strings.HasPrefix(trimmed, "No ALTQ support"):
			continue
		case strings.HasPrefix(trimmed, "ALTQ related functions"):
			continue
		}
		keep = append(keep, trimmed)
	}
	return strings.Join(keep, " ")
}

func ensurePFEnabled() error {
	out, err := exec.Command("/sbin/pfctl", "-E").CombinedOutput()
	if err == nil {
		return nil
	}
	// -E fails when PF is already enabled on some macOS versions. Verify the
	// current state before treating it as an error.
	state, _ := exec.Command("/sbin/pfctl", "-s", "info").CombinedOutput()
	if strings.Contains(string(state), "Status: Enabled") {
		return nil
	}
	return fmt.Errorf("启用 PF 失败: %w (%s)", err, strings.TrimSpace(string(out)))
}

// setupRelayGateway prepares macOS to forward and NAT traffic for delegated
// subnets. Unlike Linux there is no per-rule insertion API: the private PF
// anchor is replaced as a whole, which also keeps repeated calls idempotent.
func setupRelayGateway(subnets []string) error {
	valid := make([]string, 0, len(subnets))
	for _, raw := range subnets {
		sub := strings.TrimSpace(raw)
		if sub == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(sub); err != nil {
			return fmt.Errorf("网段 %q 无效: %w", sub, err)
		}
		valid = append(valid, sub)
	}
	if len(valid) == 0 {
		return nil
	}
	if err := ensureIPForwardingDarwin(); err != nil {
		return err
	}
	if err := ensurePFEnabled(); err != nil {
		return err
	}
	iface := defaultRouteInterface()
	if iface == "" {
		return errors.New("未找到 IPv4 默认路由接口，无法配置 PF NAT")
	}
	rules := pfGatewayRules(iface, valid)
	cmd := exec.Command("/sbin/pfctl", "-a", relayPFAnchor, "-f", "-")
	cmd.Stdin = strings.NewReader(rules)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("加载 PF 规则失败 (%s): %w (%s)", iface, err, cleanPFOutput(string(out)))
	}
	return nil
}

func relayGatewaySupported() bool { return true }
