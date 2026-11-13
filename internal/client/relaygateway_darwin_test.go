//go:build darwin

package client

import (
	"strings"
	"testing"
)

func TestPFGatewayRules(t *testing.T) {
	rules := pfGatewayRules("en0", []string{"10.88.1.0/24", "192.168.50.0/24"})
	for _, want := range []string{
		"pass in quick inet from 10.88.1.0/24 to any keep state",
		"pass out quick inet from 10.88.1.0/24 to any keep state",
		"pass in quick inet from any to 10.88.1.0/24 keep state",
		"pass out quick inet from any to 192.168.50.0/24 keep state",
		"nat on en0 inet from 10.88.1.0/24 to any -> (en0)",
		"nat on en0 inet from 192.168.50.0/24 to any -> (en0)",
	} {
		if !strings.Contains(rules, want) {
			t.Fatalf("rules missing %q:\n%s", want, rules)
		}
	}
	if strings.Index(rules, "nat on") > strings.Index(rules, "pass in") {
		t.Fatalf("PF translation rules must come before filter rules:\n%s", rules)
	}
}
