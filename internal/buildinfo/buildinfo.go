// Package buildinfo carries release metadata injected at build time.
package buildinfo

import "fmt"

// Version is the product release version. Keep it in sync with VERSION;
// scripts/check-version.sh enforces this in CI.
var Version = "1.0.8"

// Commit and BuildDate are overridden with -ldflags for release builds.
var (
	Commit    = "unknown"
	BuildDate = "unknown"
)

// String returns a human-readable build identity.
func String() string {
	if Commit == "" || Commit == "unknown" {
		return Version
	}
	if BuildDate == "" || BuildDate == "unknown" {
		return fmt.Sprintf("%s (%s)", Version, Commit)
	}
	return fmt.Sprintf("%s (%s, %s)", Version, Commit, BuildDate)
}
