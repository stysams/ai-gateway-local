package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

// HasRequiredSecrets reports whether any provider carries a secret_ref. When
// false, the system key store is irrelevant for serving traffic: a missing
// or unavailable store must not block a gateway whose providers are all
// keyless (docs/v1-scheme.md §6.2).
func HasRequiredSecrets(cfg *config.Config) bool {
	for _, target := range routeTargets(cfg) {
		if target.group != nil && strings.TrimSpace(target.group.APIKey) != "" {
			return true
		}
	}
	for _, p := range cfg.Providers {
		if p.SecretRef != "" {
			return true
		}
	}
	return false
}

type routeTarget struct {
	provider config.Provider
	group    *config.KeyGroup
	model    string
}

func routeTargets(cfg *config.Config) []routeTarget {
	if cfg == nil {
		return nil
	}
	routes := []config.Route{cfg.Routes.Codex, cfg.Routes.Claude, cfg.Routes.ClaudeDesktop, cfg.Routes.Grok, cfg.Routes.Generic}
	out := make([]routeTarget, 0, len(routes))
	seen := map[string]bool{}
	for _, route := range routes {
		provider, ok := cfg.Providers[route.Provider]
		if !ok {
			continue
		}
		key := route.Provider + "\x00" + route.KeyID + "\x00" + route.Model
		if seen[key] {
			continue
		}
		seen[key] = true
		target := routeTarget{provider: provider, model: route.Model}
		if len(provider.KeyGroups) > 0 {
			group, ok := provider.KeyGroups[route.KeyID]
			if !ok {
				continue
			}
			target.group = &group
		}
		out = append(out, target)
	}
	return out
}

// CheckRequiredKeyGroups validates only credential groups used by routes.
// Unused optional groups do not prevent readiness.
func CheckRequiredKeyGroups(cfg *config.Config) []string {
	var errs []string
	for _, target := range routeTargets(cfg) {
		if !target.provider.EnabledValue() {
			errs = append(errs, fmt.Sprintf("provider: %q is disabled", target.provider.Name))
			continue
		}
		if target.group == nil {
			continue
		}
		if !target.group.EnabledValue() {
			errs = append(errs, fmt.Sprintf("key group: %q is disabled", target.group.Name))
			continue
		}
		if strings.TrimSpace(target.group.APIKey) == "" {
			errs = append(errs, fmt.Sprintf("key group: %q has no api_key", target.group.Name))
		}
		if !target.group.ModelEnabled(target.model) {
			errs = append(errs, fmt.Sprintf("key group: %q does not expose enabled model %q", target.group.Name, target.model))
		}
		if strings.TrimSpace(target.group.EffectiveEndpoint(target.model)) == "" {
			errs = append(errs, fmt.Sprintf("key group: %q model %q has no endpoint", target.group.Name, target.model))
		}
	}
	return errs
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
