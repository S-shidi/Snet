//go:build linux

package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIptablesBinFindsBinaryWithoutPathLookup covers the container case: the
// daemon inherits a PATH that does not contain the sbin directories, so the
// binary must be found by probing the well-known locations.
func TestIptablesBinFindsBinaryWithoutPathLookup(t *testing.T) {
	origDirs, origNames := iptablesSearchDirs, iptablesNames
	defer func() { iptablesSearchDirs, iptablesNames = origDirs, origNames }()

	dir := t.TempDir()
	fake := filepath.Join(dir, "iptables")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	iptablesSearchDirs = []string{dir}
	iptablesNames = []string{"iptables"}

	// An empty PATH makes the PATH lookup fail.
	t.Setenv("PATH", "")
	if got := iptablesBin(); got != fake {
		t.Fatalf("iptablesBin = %q, want %q", got, fake)
	}
}

// TestIptablesBinReturnsEmptyWhenAbsent documents the failure mode: the
// gateway setup then reports "iptables not found" instead of failing
// silently.
func TestIptablesBinReturnsEmptyWhenAbsent(t *testing.T) {
	origDirs, origNames := iptablesSearchDirs, iptablesNames
	defer func() { iptablesSearchDirs, iptablesNames = origDirs, origNames }()

	iptablesSearchDirs = []string{t.TempDir()}
	iptablesNames = []string{"iptables"}
	t.Setenv("PATH", "")
	if got := iptablesBin(); got != "" {
		t.Fatalf("iptablesBin = %q, want empty", got)
	}
}
