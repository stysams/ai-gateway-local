//go:build !windows

package secret

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnavailableStorePlatforms covers the build-time implementations for
// macOS, Linux and other platforms: every operation must fail with
// ErrUnavailable and nothing may ever be written to disk — ai-gateway never
// falls back to plaintext storage (docs/v1-scheme.md §6.2).
func TestUnavailableStoreAllOperationsFail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := New(dir)

	if err := s.Available(ctx); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Available = %v, want ErrUnavailable", err)
	}
	if err := s.Put(ctx, "provider.x", []byte("sk-plaintext")); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Put = %v, want ErrUnavailable", err)
	}
	if _, err := s.Get(ctx, "provider.x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get = %v, want ErrUnavailable", err)
	}
	if err := s.Delete(ctx, "provider.x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Delete = %v, want ErrUnavailable", err)
	}
	if _, ok := s.(Lister); ok {
		t.Error("unexpected: unavailable store implements Lister")
	}
}

// TestUnavailableStoreNeverWrites verifies the hard rule: a failed Put must
// not leave any file, directory or plaintext behind.
func TestUnavailableStoreNeverWrites(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := New(dir)
	if err := s.Put(ctx, "provider.x", []byte("sk-plaintext")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Put = %v, want ErrUnavailable", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Put left files behind: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(dir, secretsDirName)); !os.IsNotExist(err) {
		t.Error("secrets directory was created by a failed Put")
	}
}

// TestUnavailableStoreErrorCarriesRemediation ensures the error message
// tells the user what to do and never suggests plaintext fallback.
func TestUnavailableStoreErrorCarriesRemediation(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	err := s.Available(ctx)
	if err == nil {
		t.Fatal("Available = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "明文") && !strings.Contains(msg, "plaintext") {
		t.Errorf("error message lacks remediation hint: %q", msg)
	}
}
