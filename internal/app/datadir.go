package app

import (
	"os"
	"path/filepath"
)

// dataRootName is the fixed data directory name under the user's home
// (docs/v1-scheme.md §4).
const dataRootName = ".ai-gateway"

// joinDataRoot builds the data root under a home directory, falling back to
// a relative directory when the home is unknown.
func joinDataRoot(home string) string {
	if home == "" {
		return dataRootName
	}
	return filepath.Join(home, dataRootName)
}

// DefaultDataDir returns the data root: %USERPROFILE%\.ai-gateway on
// Windows, ~/.ai-gateway elsewhere. The per-platform resolution lives in
// datadir_windows.go / datadir_unix.go selected by build tags (no runtime
// platform branching, docs/v1-scheme.md §3.1). AI_GATEWAY_DATA_DIR overrides
// it for testing and portable setups.
func DefaultDataDir() string {
	if v := os.Getenv("AI_GATEWAY_DATA_DIR"); v != "" {
		return v
	}
	return platformDefaultDataDir()
}
