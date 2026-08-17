// Package upstream holds the shared, safely configured HTTP transport and
// the secret-injection helpers used by every outbound adapter. Adapters
// never call each other; they only share this common plumbing
// (docs/v1-scheme.md §10).
package upstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"ai-gateway/internal/secret"
)

// ApplyExtraHeaders overlays validated provider headers on adapter defaults.
// Authentication and transport-managed headers are rejected by config
// validation and are therefore never expected here.
func ApplyExtraHeaders(target http.Header, extra map[string]string) {
	for name, value := range extra {
		target.Set(name, value)
	}
}

// ErrSecretMissing reports a provider that declares a secret_ref but has no
// readable secret in the key store. The gateway must fail the request
// instead of sending it without authentication (docs/v1-scheme.md §6.2).
var ErrSecretMissing = errors.New("provider secret missing")

// ErrSecretStore reports that the system key store itself failed while
// reading a secret (unavailable or broken). This is an internal/config
// problem, never an upstream problem: callers must not map it to 502.
var ErrSecretStore = errors.New("system key store error")

// DefaultResponseHeaderTimeout bounds how long the gateway waits for the
// upstream's response headers. It never bounds the streaming body:
// long-running reasoning streams must not be cut off (docs/v1-scheme.md
// §9.4). A timeout here maps to 504.
const DefaultResponseHeaderTimeout = 5 * time.Minute

// NewTransport returns a safely configured transport with a shared
// connection pool, a bounded response-header timeout and no overall
// streaming deadline.
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: DefaultResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NoRedirectClient returns an http.Client over tr that never follows
// redirects: the provider's Authorization must not leak to a different
// target, and redirect status plus Location are forwarded to the client
// as-is.
func NoRedirectClient(tr *http.Transport) *http.Client {
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Credential is one secret read from the key store, formatted for a header.
type Credential struct {
	// Header is the header name (Authorization / x-api-key).
	Header string
	// Value is the header value. Callers must call Zero when done.
	Value string
	// raw is the mutable key bytes; Zero clears it.
	raw []byte
}

// Zero clears the underlying key bytes.
func (c *Credential) Zero() {
	if c.raw != nil {
		secret.Zero(c.raw)
		c.raw = nil
	}
}

// Bearer reads the provider secret and formats it as an Authorization:
// Bearer credential. The key bytes are zeroed by Credential.Zero.
func Bearer(ctx context.Context, secrets secret.Store, ref string) (*Credential, error) {
	return read(ctx, secrets, ref, func(key string) (string, string) {
		return "Authorization", "Bearer " + key
	})
}

// XAPIKey reads the provider secret and formats it as an x-api-key
// credential (Anthropic auth, docs/v1-scheme.md §10).
func XAPIKey(ctx context.Context, secrets secret.Store, ref string) (*Credential, error) {
	return read(ctx, secrets, ref, func(key string) (string, string) {
		return "x-api-key", key
	})
}

func read(ctx context.Context, secrets secret.Store, ref string, format func(key string) (string, string)) (*Credential, error) {
	b, err := secrets.Get(ctx, ref)
	if err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			return nil, fmt.Errorf("%w: ref %q has no secret; write it via POST /api/v1/providers", ErrSecretMissing, ref)
		}
		return nil, fmt.Errorf("%w: read secret ref %q: %v", ErrSecretStore, ref, err)
	}
	defer secret.Zero(b)
	name, value := format(string(b))
	return &Credential{Header: name, Value: value, raw: b}, nil
}
