// Package secret owns the system key store contract (docs/v1-scheme.md §6):
// a per-ref Put/Get/Delete store whose platform implementations never fall
// back to plaintext persistence. On Windows the store is the current user's
// DPAPI scope with ciphertext files under the data root's secrets/
// directory; macOS and Linux provide explicit build-time implementations
// that fail loudly until Keychain / Secret Service support lands — never
// plaintext YAML, env files or local storage.
package secret

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"
)

// secretsDirName is the fixed subdirectory of the data root that holds the
// platform key store's ciphertext files (docs/v1-scheme.md §4).
const secretsDirName = "secrets"

// ErrNotFound means the referenced secret does not exist. It is distinct
// from store unavailability: callers must be able to tell "no secret yet"
// from "the system key store is broken" (docs/v1-scheme.md §6.1).
var ErrNotFound = errors.New("secret not found")

// ErrUnavailable means the current platform's system key store cannot be
// used. Operations on required secrets must fail with this error instead of
// falling back to plaintext storage (docs/v1-scheme.md §6.2).
var ErrUnavailable = errors.New("system key store unavailable")

// refRe constrains secret_ref values to characters that are safe as file
// names on every supported platform. Refs are used verbatim inside the
// secrets directory, so anything else is rejected at the boundary.
var refRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Store is the system key store contract. Refs come from the config's
// secret_ref fields; Get returns a fresh byte slice that the caller must
// zero as soon as possible. Errors distinguish "not found" from "store
// unavailable".
type Store interface {
	Put(ctx context.Context, ref string, value []byte) error
	Get(ctx context.Context, ref string) ([]byte, error)
	Delete(ctx context.Context, ref string) error
	Available(ctx context.Context) error
}

// Lister is implemented by stores that can enumerate existing refs. The
// doctor path uses it to report orphan secrets (docs/v1-scheme.md §6.3).
type Lister interface {
	List(ctx context.Context) ([]string, error)
}

// Platformer is implemented by stores that can name their backing system.
type Platformer interface {
	Platform() string
}

// ValidRef validates a secret_ref value. Refs are used as file names inside
// the secrets directory, so the charset is deliberately narrow.
func ValidRef(ref string) error {
	if !refRe.MatchString(ref) {
		return fmt.Errorf("invalid secret ref %q: must match %s", ref, refRe.String())
	}
	return nil
}

// Zero clears b in place. Callers that received secret bytes from Get must
// zero them as soon as possible (docs/v1-scheme.md §6.1).
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// MemStore is an in-memory Store used for test injection and as a contract
// reference implementation. It is not a production platform implementation:
// plaintext lives in process memory only, and nothing is ever persisted.
type MemStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{m: map[string][]byte{}}
}

// Platform implements secret.Platformer.
func (s *MemStore) Platform() string { return "memory" }

// Available implements secret.Store.
func (s *MemStore) Available(context.Context) error { return nil }

// Put implements secret.Store. It replaces any existing value for ref.
func (s *MemStore) Put(_ context.Context, ref string, value []byte) error {
	if err := ValidRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[ref] = append([]byte(nil), value...)
	return nil
}

// Get implements secret.Store.
func (s *MemStore) Get(_ context.Context, ref string) ([]byte, error) {
	if err := ValidRef(ref); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[ref]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

// Delete implements secret.Store. Deleting a missing ref is a no-op.
func (s *MemStore) Delete(_ context.Context, ref string) error {
	if err := ValidRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, ref)
	return nil
}

// List implements secret.Lister.
func (s *MemStore) List(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs := make([]string, 0, len(s.m))
	for k := range s.m {
		refs = append(refs, k)
	}
	return refs, nil
}
