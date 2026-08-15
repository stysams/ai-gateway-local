//go:build !windows

package app

import "os"

// platformDefaultDataDir resolves the data root on macOS and Linux:
// ~/.ai-gateway via os.UserHomeDir (docs/v1-scheme.md §4).
func platformDefaultDataDir() string {
	return unixDataDir()
}

// unixDataDir implements the Unix data-root resolution. It is defined on
// every platform so tests can exercise both resolutions anywhere; on Windows
// builds it is never used by production code.
func unixDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return joinDataRoot(home)
}

// windowsDataDir mirrors the Windows resolution (%USERPROFILE%) for
// cross-platform tests. On Unix it is never used by production code.
func windowsDataDir() string {
	if h := os.Getenv("USERPROFILE"); h != "" {
		return joinDataRoot(h)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return joinDataRoot(home)
}
