//go:build windows

package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// secretFileSuffix marks DPAPI blob files inside the secrets directory.
const secretFileSuffix = ".bin"

// dpapiStore persists each secret as one DPAPI-encrypted file at
// <dataRoot>/secrets/<ref>.bin, encrypted in the current user's scope
// (docs/v1-scheme.md §6.2). Plaintext only ever exists transiently in
// process memory; files are written atomically with the strictest file mode
// the platform offers.
type dpapiStore struct {
	dir string
}

// New returns the Windows DPAPI key store rooted at dataDir.
func New(dataDir string) Store {
	return &dpapiStore{dir: filepath.Join(dataDir, secretsDirName)}
}

// Platform implements secret.Platformer.
func (s *dpapiStore) Platform() string { return "windows-dpapi" }

// path returns the ciphertext file for a validated ref.
func (s *dpapiStore) path(ref string) string {
	return filepath.Join(s.dir, ref+secretFileSuffix)
}

// Available probes the current user's DPAPI scope with an encrypt/decrypt
// roundtrip. It never touches the secrets directory.
func (s *dpapiStore) Available(ctx context.Context) error {
	probe := []byte("ai-gateway dpapi availability probe")
	defer Zero(probe)
	blob, err := protect(probe)
	if err != nil {
		return fmt.Errorf("%w: DPAPI protect failed: %v", ErrUnavailable, err)
	}
	out, err := unprotect(blob)
	if err != nil {
		return fmt.Errorf("%w: DPAPI unprotect failed: %v", ErrUnavailable, err)
	}
	defer Zero(out)
	if !bytes.Equal(out, probe) {
		return fmt.Errorf("%w: DPAPI roundtrip mismatch", ErrUnavailable)
	}
	return nil
}

// Put encrypts value with the current user's DPAPI scope and atomically
// replaces the ciphertext file for ref.
func (s *dpapiStore) Put(_ context.Context, ref string, value []byte) error {
	if err := ValidRef(ref); err != nil {
		return err
	}
	blob, err := protect(value)
	if err != nil {
		return fmt.Errorf("protect secret %q: %w", ref, err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create secrets directory %s: %w", s.dir, err)
	}
	path := s.path(ref)

	tmp, err := os.CreateTemp(s.dir, ".secret-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp secret file in %s: %w", s.dir, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp secret %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp secret %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp secret %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp secret %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace secret %s: %w", path, err)
	}
	tmpName = "" // renamed; defer must not remove the temp name

	syncDir(s.dir)
	return nil
}

// Get reads and decrypts the ciphertext file for ref. The returned slice is
// fresh and must be zeroed by the caller as soon as possible.
func (s *dpapiStore) Get(_ context.Context, ref string) ([]byte, error) {
	if err := ValidRef(ref); err != nil {
		return nil, err
	}
	blob, err := os.ReadFile(s.path(ref))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read secret %q: %w", ref, err)
	}
	defer Zero(blob)
	out, err := unprotect(blob)
	if err != nil {
		return nil, fmt.Errorf("unprotect secret %q: %w", ref, err)
	}
	return out, nil
}

// Delete removes the ciphertext file for ref. Deleting a missing ref is a
// no-op.
func (s *dpapiStore) Delete(_ context.Context, ref string) error {
	if err := ValidRef(ref); err != nil {
		return err
	}
	err := os.Remove(s.path(ref))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete secret %q: %w", ref, err)
	}
	return nil
}

// List enumerates the refs that currently have ciphertext files. A missing
// secrets directory means no secrets exist.
func (s *dpapiStore) List(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list secrets directory %s: %w", s.dir, err)
	}
	var refs []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, secretFileSuffix) {
			continue
		}
		refs = append(refs, strings.TrimSuffix(name, secretFileSuffix))
	}
	return refs, nil
}

// protect encrypts data in the current user's DPAPI scope without any UI.
func protect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("cannot protect empty data")
	}
	var in, out windows.DataBlob
	in.Size = uint32(len(data))
	in.Data = &data[0]
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	result := make([]byte, int(out.Size))
	copy(result, unsafe.Slice(out.Data, int(out.Size)))
	return result, nil
}

// unprotect decrypts a DPAPI blob produced by protect.
func unprotect(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, errors.New("cannot unprotect empty blob")
	}
	var in, out windows.DataBlob
	in.Size = uint32(len(blob))
	in.Data = &blob[0]
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	result := make([]byte, int(out.Size))
	copy(result, unsafe.Slice(out.Data, int(out.Size)))
	return result, nil
}

// syncDir flushes directory metadata best-effort after a rename.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}
