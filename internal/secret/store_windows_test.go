//go:build windows

package secret

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newDPAPIStore builds a store rooted at a fresh temp dir.
func newDPAPIStore(t *testing.T) *dpapiStore {
	t.Helper()
	return &dpapiStore{dir: filepath.Join(t.TempDir(), secretsDirName)}
}

func TestDPAPIStoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	s := newDPAPIStore(t)
	if err := s.Available(ctx); err != nil {
		t.Fatalf("Available: %v", err)
	}
	if err := s.Put(ctx, "provider.openrouter", []byte("sk-dpapi-test")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "provider.openrouter")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer Zero(got)
	if string(got) != "sk-dpapi-test" {
		t.Errorf("Get = %q, want %q", got, "sk-dpapi-test")
	}

	// Put replaces the previous ciphertext.
	if err := s.Put(ctx, "provider.openrouter", []byte("sk-dpapi-new")); err != nil {
		t.Fatalf("Put replace: %v", err)
	}
	got2, err := s.Get(ctx, "provider.openrouter")
	if err != nil {
		t.Fatalf("Get after replace: %v", err)
	}
	defer Zero(got2)
	if string(got2) != "sk-dpapi-new" {
		t.Errorf("Get after replace = %q, want %q", got2, "sk-dpapi-new")
	}
}

func TestDPAPIStoreFileLayoutAndNoPlaintext(t *testing.T) {
	ctx := context.Background()
	s := newDPAPIStore(t)
	key := "sk-very-secret-42"
	if err := s.Put(ctx, "provider.openrouter", []byte(key)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	path := s.path("provider.openrouter")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ciphertext file missing at %s: %v", path, err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), key) {
		t.Error("ciphertext file contains the plaintext key")
	}
	// The blob must be a real DPAPI blob, not a plaintext copy.
	if strings.Contains(string(blob), "sk-") {
		t.Error("ciphertext file looks like plaintext")
	}
	// No stray temp files after an atomic write.
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestDPAPIStoreGetMissing(t *testing.T) {
	ctx := context.Background()
	s := newDPAPIStore(t)
	if _, err := s.Get(ctx, "provider.never"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
}

func TestDPAPIStoreDelete(t *testing.T) {
	ctx := context.Background()
	s := newDPAPIStore(t)
	if err := s.Put(ctx, "provider.x", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "provider.x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "provider.x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, "provider.x"); err != nil {
		t.Errorf("Delete missing = %v, want nil (idempotent)", err)
	}
}

func TestDPAPIStoreList(t *testing.T) {
	ctx := context.Background()
	s := newDPAPIStore(t)
	// A store with no secrets directory lists nothing.
	refs, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("List on empty store = %v, want []", refs)
	}
	for _, ref := range []string{"provider.a", "provider.b"} {
		if err := s.Put(ctx, ref, []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	refs, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Errorf("List = %v, want 2 refs", refs)
	}
	if err := s.Delete(ctx, "provider.a"); err != nil {
		t.Fatal(err)
	}
	refs, err = s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "provider.b" {
		t.Errorf("List after delete = %v, want [provider.b]", refs)
	}
}

func TestDPAPIStoreRejectsInvalidRef(t *testing.T) {
	ctx := context.Background()
	s := newDPAPIStore(t)
	if err := s.Put(ctx, "bad/ref", []byte("v")); err == nil {
		t.Error("Put accepted invalid ref")
	}
	if _, err := s.Get(ctx, "bad/ref"); err == nil {
		t.Error("Get accepted invalid ref")
	}
	if err := s.Delete(ctx, "bad/ref"); err == nil {
		t.Error("Delete accepted invalid ref")
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("invalid refs left files behind: %v", entries)
	}
}

func TestDPAPIStoreCorruptFile(t *testing.T) {
	ctx := context.Background()
	s := newDPAPIStore(t)
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path("provider.x"), []byte("not a dpapi blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "provider.x"); err == nil {
		t.Error("Get of a corrupt blob succeeded, want error")
	}
}

func TestDPAPIStorePlatform(t *testing.T) {
	s := newDPAPIStore(t)
	if s.Platform() != "windows-dpapi" {
		t.Errorf("Platform() = %q, want windows-dpapi", s.Platform())
	}
}
