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

// TestIptablesUsableReportsBrokenBackend covers the NAS case: the nf_tables
// binary exists but the kernel cannot use it, so the probe must reject it and
// the legacy backend must be picked instead.
func TestIptablesUsableReportsBrokenBackend(t *testing.T) {
	origDirs, origNames := iptablesSearchDirs, iptablesNames
	defer func() { iptablesSearchDirs, iptablesNames = origDirs, origNames }()

	dir := t.TempDir()
	broken := filepath.Join(dir, "iptables")
	// Exits with 4 and prints the classic "table does not exist" diagnostic.
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho \"iptables: can't initialize iptables table 'filter': Table does not exist (do you need to insmod?)\" >&2\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "iptables-legacy")
	if err := os.WriteFile(legacy, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	iptablesSearchDirs = []string{dir}
	iptablesNames = []string{"iptables", "iptables-legacy"}
	t.Setenv("PATH", "")

	if iptablesUsable(broken) {
		t.Fatal("the broken nf_tables backend must not be selected")
	}
	if !iptablesUsable(legacy) {
		t.Fatal("the legacy backend must be usable")
	}
	if got := iptablesBin(); got != legacy {
		t.Fatalf("iptablesBin = %q, want the legacy backend %q", got, legacy)
	}
}
