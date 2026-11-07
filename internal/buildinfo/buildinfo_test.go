package buildinfo

import "testing"

func TestString(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := Version, Commit, BuildDate
	t.Cleanup(func() {
		Version, Commit, BuildDate = oldVersion, oldCommit, oldBuildDate
	})

	Version, Commit, BuildDate = "1.2.3", "unknown", "unknown"
	if got := String(); got != "1.2.3" {
		t.Fatalf("String() = %q, want version only", got)
	}

	Commit = "abc123"
	if got := String(); got != "1.2.3 (abc123)" {
		t.Fatalf("String() = %q, want version + commit", got)
	}

	BuildDate = "2026-10-26T00:00:00Z"
	if got := String(); got != "1.2.3 (abc123, 2026-10-26T00:00:00Z)" {
		t.Fatalf("String() = %q, want full build identity", got)
	}
}
