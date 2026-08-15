//go:build windows

package app

import "os"

// platformDefaultDataDir resolves the data root on Windows:
// %USERPROFILE%\.ai-gateway, falling back to os.UserHomeDir (docs/v1-scheme.md §4).
func platformDefaultDataDir() string {
	return windowsDataDir()
}

// windowsDataDir implements the Windows data-root resolution. It is defined
// on every platform so tests can exercise both resolutions anywhere; on
// non-Windows builds it is never used by production code.
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

// unixDataDir mirrors the Unix resolution (~/.ai-gateway via os.UserHomeDir)
// for cross-platform tests. On Windows it is never used by production code.
func unixDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return joinDataRoot(home)
}
