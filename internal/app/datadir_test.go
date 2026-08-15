package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultDataDirEnvOverride(t *testing.T) {
	t.Setenv("AI_GATEWAY_DATA_DIR", filepath.Join("C:", "custom", "gateway"))
	if got := DefaultDataDir(); got != filepath.Join("C:", "custom", "gateway") {
		t.Errorf("DefaultDataDir() = %q, want override path", got)
	}
}

func TestDefaultDataDirWithoutOverride(t *testing.T) {
	t.Setenv("AI_GATEWAY_DATA_DIR", "")
	got := DefaultDataDir()
	if got == "" || !strings.HasSuffix(got, dataRootName) {
		t.Errorf("DefaultDataDir() = %q, want a path ending in %q", got, dataRootName)
	}
	if want := platformDefaultDataDir(); got != want {
		t.Errorf("DefaultDataDir() = %q, platformDefaultDataDir() = %q", got, want)
	}
}

func TestWindowsDataDirUsesUserProfile(t *testing.T) {
	t.Setenv("USERPROFILE", filepath.Join("C:", "Users", "tester"))
	want := filepath.Join("C:", "Users", "tester", dataRootName)
	if got := windowsDataDir(); got != want {
		t.Errorf("windowsDataDir() = %q, want %q", got, want)
	}
}

func TestWindowsDataDirWithoutUserProfile(t *testing.T) {
	t.Setenv("USERPROFILE", "")
	got := windowsDataDir()
	if got == "" || !strings.HasSuffix(got, dataRootName) {
		t.Errorf("windowsDataDir() = %q, want a path ending in %q", got, dataRootName)
	}
}

func TestUnixDataDirUsesHome(t *testing.T) {
	got := unixDataDir()
	if got == "" || !strings.HasSuffix(got, dataRootName) {
		t.Errorf("unixDataDir() = %q, want a path ending in %q", got, dataRootName)
	}
}
