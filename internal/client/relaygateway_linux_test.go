//go:build linux

package client

import (
	"errors"
	"testing"
)

func TestEnsureIPForwardingAcceptsAlreadyEnabled(t *testing.T) {
	origRead, origWrite, origPersist := ipForwardReadFn, ipForwardWriteFn, ipForwardPersistFn
	defer func() {
		ipForwardReadFn, ipForwardWriteFn, ipForwardPersistFn = origRead, origWrite, origPersist
	}()

	ipForwardReadFn = func() string { return "1" }
	wrote := false
	ipForwardWriteFn = func() error { wrote = true; return nil }
	ipForwardPersistFn = func(string, string) {}

	if err := ensureIPForwarding(); err != nil {
		t.Fatalf("forwarding already on: %v", err)
	}
	if wrote {
		t.Fatal("the sysctl must not be written when forwarding is already enabled")
	}
}

func TestEnsureIPForwardingToleratesReadOnlyProcSys(t *testing.T) {
	origRead, origWrite, origPersist := ipForwardReadFn, ipForwardWriteFn, ipForwardPersistFn
	defer func() {
		ipForwardReadFn, ipForwardWriteFn, ipForwardPersistFn = origRead, origWrite, origPersist
	}()

	// The write fails (read-only /proc/sys) but the value is already 1.
	ipForwardReadFn = func() string { return "1" }
	ipForwardWriteFn = func() error { return errors.New("exit status 1 (Read-only file system)") }
	ipForwardPersistFn = func(string, string) {}

	// The read says "1", so the write is never attempted and no error is
	// reported: the gateway can forward.
	if err := ensureIPForwarding(); err != nil {
		t.Fatalf("read-only proc/sys with forwarding on must succeed: %v", err)
	}
}

func TestEnsureIPForwardingFailsWhenDisabled(t *testing.T) {
	origRead, origWrite, origPersist := ipForwardReadFn, ipForwardWriteFn, ipForwardPersistFn
	defer func() {
		ipForwardReadFn, ipForwardWriteFn, ipForwardPersistFn = origRead, origWrite, origPersist
	}()

	ipForwardReadFn = func() string { return "0" }
	ipForwardWriteFn = func() error { return errors.New("sysctl failed") }
	ipForwardPersistFn = func(string, string) {}

	if err := ensureIPForwarding(); err == nil {
		t.Fatal("forwarding disabled and not settable must be reported as an error")
	}
}

func TestSetupRelayGatewayRejectsInvalidSubnet(t *testing.T) {
	origRead, origWrite, origPersist := ipForwardReadFn, ipForwardWriteFn, ipForwardPersistFn
	defer func() {
		ipForwardReadFn, ipForwardWriteFn, ipForwardPersistFn = origRead, origWrite, origPersist
	}()
	ipForwardReadFn = func() string { return "1" }
	ipForwardPersistFn = func(string, string) {}

	if err := setupRelayGateway([]string{"not-a-cidr"}); err == nil {
		t.Fatal("an invalid subnet must be rejected")
	}
}
