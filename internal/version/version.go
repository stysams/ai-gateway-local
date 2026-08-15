// Package version carries build-time version metadata for the ai-gateway
// binaries. Values may be overridden at build time via -ldflags.
package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the semantic version of the gateway. "0.1.0-dev" is the
	// default during development; release builds inject a concrete value.
	Version = "0.1.0-dev"
	// Commit is the VCS commit the binary was built from.
	Commit = "unknown"
	// BuildTime is the UTC build timestamp.
	BuildTime = "unknown"
)

// String returns the human-readable multi-line version report used by the
// `version` CLI command.
func String() string {
	return fmt.Sprintf("ai-gateway %s\ncommit: %s\nbuilt: %s\ngo: %s\nplatform: %s/%s",
		Version, Commit, BuildTime, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
