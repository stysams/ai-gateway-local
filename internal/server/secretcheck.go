package server

import (
	"context"
	"errors"
	"fmt"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

// HasRequiredSecrets reports whether any provider carries a secret_ref. When
// false, the system key store is irrelevant for serving traffic: a missing
// or unavailable store must not block a gateway whose providers are all
// keyless (docs/v1-scheme.md §6.2).
func HasRequiredSecrets(cfg *config.Config) bool {
	for _, p := range cfg.Providers {
		if p.SecretRef != "" {
			return true
		}
	}
	return false
}

// CheckSecretStore returns nil when the store is available.
func CheckSecretStore(ctx context.Context, store secret.Store) error {
	return store.Available(ctx)
}

// CheckRequiredSecrets verifies that every provider with a secret_ref has a
// readable secret in the store. It returns one human-readable problem per
// provider, and nil when every required secret exists. Read bytes are
// zeroed immediately; plaintext never leaves this function.
func CheckRequiredSecrets(ctx context.Context, store secret.Store, cfg *config.Config) []string {
	var errs []string
	for id, p := range cfg.Providers {
		if p.SecretRef == "" {
			continue
		}
		b, err := store.Get(ctx, p.SecretRef)
		if b != nil {
			secret.Zero(b)
		}
		switch {
		case err == nil:
		case errors.Is(err, secret.ErrNotFound):
			errs = append(errs, fmt.Sprintf("secret: provider %q has no secret for ref %q; write it via POST /api/v1/providers (api_key field)", id, p.SecretRef))
		default:
			errs = append(errs, fmt.Sprintf("secret: provider %q ref %q: %v", id, p.SecretRef, err))
		}
	}
	return errs
}
