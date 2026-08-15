package secret

import (
	"context"
	"fmt"
)

// unavailableStore is the build-time implementation for platforms whose
// system key store is not implemented yet (macOS Keychain, Linux Secret
// Service). It exists so a build is always complete, but every operation
// fails with ErrUnavailable plus a remediation hint: ai-gateway never falls
// back to plaintext YAML, env files or local storage (docs/v1-scheme.md
// §6.2).
type unavailableStore struct {
	platform string
	hint     string
}

// Platform implements secret.Platformer.
func (s *unavailableStore) Platform() string { return s.platform }

// Available implements secret.Store.
func (s *unavailableStore) Available(context.Context) error {
	return fmt.Errorf("%w: %s: %s", ErrUnavailable, s.platform, s.hint)
}

// Put implements secret.Store and always fails: no key may be written.
func (s *unavailableStore) Put(ctx context.Context, _ string, _ []byte) error {
	return s.Available(ctx)
}

// Get implements secret.Store and always fails: no key may exist.
func (s *unavailableStore) Get(ctx context.Context, _ string) ([]byte, error) {
	return nil, s.Available(ctx)
}

// Delete implements secret.Store and always fails: there is nothing to
// delete, and the store must stay explicit about being unavailable.
func (s *unavailableStore) Delete(ctx context.Context, _ string) error {
	return s.Available(ctx)
}

// unavailableStore deliberately does not implement secret.Lister: it can
// never hold secrets, so there is nothing to enumerate.
