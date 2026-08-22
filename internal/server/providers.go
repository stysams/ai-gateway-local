package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

// maxAdminBody bounds management API request bodies (docs/v1-scheme.md §9.3).
const maxAdminBody = 128 << 20

// providerRefPrefix is the canonical secret_ref namespace for providers
// managed through the management API: "provider.<id>" (docs/v1-scheme.md
// §5.1 uses exactly this shape).
const providerRefPrefix = "provider."

// Sentinel errors for the provider CRUD paths; handlers map them onto the
// unified API error shape.
var (
	errProviderNotFound = errors.New("provider not found")
	errProviderInUse    = errors.New("provider is referenced by routes")
)

// refFor returns the canonical secret ref for a provider id.
func refFor(id string) string { return providerRefPrefix + id }

// ProviderRequest is the create/update payload. api_key, when present, is
// written to the system key store and discarded immediately; it is never
// stored in config or returned by any read endpoint (docs/v1-scheme.md
// §11.3).
type ProviderRequest struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Adapter        string                 `json:"adapter"`
	BaseURL        string                 `json:"base_url"`
	ModelsURL      string                 `json:"models_url"`
	ExtraHeaders   map[string]string      `json:"extra_headers"`
	DisguiseClient string                 `json:"disguise_client"`
	DefaultModel   string                 `json:"default_model"`
	Enabled        *bool                  `json:"enabled"`
	Models         []ProviderModelPayload `json:"models"`
	APIKey         *string                `json:"api_key"`
	Capabilities   *CapabilitiesPayload   `json:"capabilities"`
}

// ProviderModelPayload is the editable, persisted model metadata for one
// provider. Zero token limits mean the upstream did not publish a value.
type ProviderModelPayload struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	Adapter         string `json:"adapter,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	ContextWindow   int    `json:"context_window"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	Enabled         *bool  `json:"enabled,omitempty"`
}

// CapabilitiesPayload is the wire form of provider capability flags.
type CapabilitiesPayload struct {
	ImageInput        bool `json:"image_input"`
	Reasoning         bool `json:"reasoning"`
	ContextManagement bool `json:"context_management"`
}

// ProviderResponse is the provider payload returned by the management API.
// It never carries secret material (docs/v1-scheme.md §11.3).
type ProviderResponse struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Adapter        string                 `json:"adapter"`
	BaseURL        string                 `json:"base_url"`
	ModelsURL      string                 `json:"models_url,omitempty"`
	ExtraHeaders   map[string]string      `json:"extra_headers"`
	DisguiseClient string                 `json:"disguise_client,omitempty"`
	DefaultModel   string                 `json:"default_model"`
	Enabled        bool                   `json:"enabled"`
	Models         []ProviderModelPayload `json:"models"`
	HasSecret      bool                   `json:"has_secret"`
	Capabilities   CapabilitiesPayload    `json:"capabilities"`
}

type ProviderAvailabilityRequest struct {
	Enabled *bool           `json:"enabled"`
	Models  map[string]bool `json:"models"`
}

// PartialFailureError reports a transaction whose config write failed AND
// whose key restoration also failed: the gateway cannot tell which key is
// live. It must be surfaced as an explicit partial-failure error and doctor
// must be able to report the resulting key store state
// (docs/v1-scheme.md §6.3).
type PartialFailureError struct {
	Ref        string
	ConfigErr  error
	RestoreErr error
}

func (e *PartialFailureError) Error() string {
	return fmt.Sprintf("partial failure: config write failed (%v) and restoring the previous key %q failed (%v); run doctor to inspect the key store",
		e.ConfigErr, e.Ref, e.RestoreErr)
}

func (e *PartialFailureError) Unwrap() error { return e.ConfigErr }

// decodeJSON parses a management API request body, enforcing the JSON-only
// and size limits (docs/v1-scheme.md §9.3): exactly one JSON document, no
// unknown fields (a misspelled api_key must fail loudly instead of silently
// losing the key), body at most 128 MiB. On failure it writes the error
// response and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large",
				fmt.Sprintf("request body exceeds %d bytes", maxAdminBody), nil)
			return false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return false
	}
	// Reject trailing data: a second JSON document after the first is a
	// malformed request, not a feature.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json",
			"request body must contain exactly one JSON document", nil)
		return false
	}
	return true
}

// providerFromRequest validates a request payload into a config.Provider
// (docs/v1-scheme.md §6.3 step 1: validate before touching the key store).
func providerFromRequest(id string, req ProviderRequest) (config.Provider, error) {
	p := config.Provider{
		Name:           req.Name,
		Adapter:        req.Adapter,
		BaseURL:        req.BaseURL,
		ModelsURL:      req.ModelsURL,
		ExtraHeaders:   cloneStringMap(req.ExtraHeaders),
		DisguiseClient: strings.TrimSpace(req.DisguiseClient),
		DefaultModel:   req.DefaultModel,
		Models:         providerModelsFromPayload(req.Models),
		Enabled:        req.Enabled,
	}
	if req.Capabilities != nil {
		p.Capabilities = config.Capabilities{
			ImageInput:        req.Capabilities.ImageInput,
			Reasoning:         req.Capabilities.Reasoning,
			ContextManagement: req.Capabilities.ContextManagement,
		}
	}
	if err := config.ValidateProvider(id, p); err != nil {
		return p, err
	}
	return p, nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func providerModelsFromPayload(models []ProviderModelPayload) []config.ProviderModel {
	out := make([]config.ProviderModel, 0, len(models))
	for _, model := range models {
		out = append(out, config.ProviderModel{
			ID: strings.TrimSpace(model.ID), Name: strings.TrimSpace(model.Name),
			Adapter: strings.TrimSpace(model.Adapter), Endpoint: strings.TrimSpace(model.Endpoint),
			ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens, Enabled: model.Enabled,
		})
	}
	return out
}

func providerModelsPayload(models []config.ProviderModel) []ProviderModelPayload {
	out := make([]ProviderModelPayload, 0, len(models))
	for _, model := range models {
		out = append(out, ProviderModelPayload{
			ID: model.ID, Name: model.Name, Adapter: model.Adapter, Endpoint: model.Endpoint,
			ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
			Enabled: config.BoolPtr(model.EnabledValue()),
		})
	}
	return out
}

// upsertProvider runs the provider write transaction
// (docs/v1-scheme.md §6.3):
//
//  1. fields are validated by the caller,
//  2. the new key is written to the system key store when newKey is non-nil,
//  3. the config is written atomically,
//  4. on config write failure the previous key is restored; when
//     restoration fails, a PartialFailureError is returned so the caller can
//     report an explicit partial failure.
//
// newKey is a fresh byte slice (from the request body) and oldKey is the
// slice returned by Store.Get; both are zeroed on every return path via a
// single deferred cleanup. Callers must hold s.txMu for the whole
// transaction (snapshot → key store → config write) so concurrent provider
// writes can never race on a stale snapshot.
func (s *Server) upsertProvider(ctx context.Context, id string, p config.Provider, newKey []byte) (*ProviderResponse, error) {
	ref := refFor(id)
	var oldKey []byte
	haveOld := false
	if newKey != nil {
		old, err := s.secrets.Get(ctx, ref)
		switch {
		case err == nil:
			haveOld = true
			oldKey = old
		case errors.Is(err, secret.ErrNotFound):
			// Fresh key, nothing to restore on failure.
		case errors.Is(err, secret.ErrUnavailable):
			return nil, fmt.Errorf("%w: cannot write key for provider %q", err, id)
		default:
			return nil, fmt.Errorf("read previous key for provider %q: %w", id, err)
		}
	}
	// Zero both key buffers on every return path: success, key-write
	// failure, config-write failure, partial failure. restoreKey below
	// already zeroes oldKey after a successful restore; re-zeroing is
	// idempotent and keeps the invariant unconditional.
	defer func() {
		if oldKey != nil {
			secret.Zero(oldKey)
		}
		if newKey != nil {
			secret.Zero(newKey)
		}
	}()

	if newKey != nil {
		if err := s.secrets.Put(ctx, ref, newKey); err != nil {
			return nil, fmt.Errorf("write secret for provider %q: %w", id, err)
		}
	}

	current := s.cfg.View()
	if current == nil {
		// The config was never loaded; the key store must not end up ahead
		// of the config. Restore, then fail loudly.
		if restoreErr := s.restoreKey(ctx, ref, haveOld, oldKey); restoreErr != nil {
			return nil, &PartialFailureError{Ref: ref, ConfigErr: errors.New("config not loaded"), RestoreErr: restoreErr}
		}
		return nil, errors.New("config not loaded")
	}
	next := s.cfg.Snapshot()
	next.Providers[id] = p
	if err := s.syncClientsThenWrite(current, next); err != nil {
		if restoreErr := s.restoreKey(ctx, ref, haveOld, oldKey); restoreErr != nil {
			return nil, &PartialFailureError{Ref: ref, ConfigErr: err, RestoreErr: restoreErr}
		}
		return nil, fmt.Errorf("write config: %w", err)
	}
	s.invalidateModels(id)

	resp := s.providerResponse(ctx, id, next.Providers[id])
	return &resp, nil
}

// restoreKey puts the previous key back after a failed config write; with no
// previous key it removes the freshly written one. Both directions are valid
// outcomes, so "not found" is never reported as an error. oldKey is zeroed
// after use.
func (s *Server) restoreKey(ctx context.Context, ref string, haveOld bool, oldKey []byte) error {
	var err error
	if haveOld {
		err = s.secrets.Put(ctx, ref, oldKey)
	} else {
		err = s.secrets.Delete(ctx, ref)
	}
	if oldKey != nil {
		secret.Zero(oldKey)
	}
	return err
}

// hasSecret reports whether a readable secret exists for ref. Read bytes are
// zeroed immediately.
func (s *Server) hasSecret(ctx context.Context, ref string) bool {
	if ref == "" {
		return false
	}
	b, err := s.secrets.Get(ctx, ref)
	if b != nil {
		secret.Zero(b)
	}
	return err == nil
}

// providerResponse renders a provider without any secret material.
func (s *Server) providerResponse(ctx context.Context, id string, p config.Provider) ProviderResponse {
	return ProviderResponse{
		ID:             id,
		Name:           p.Name,
		Adapter:        p.Adapter,
		BaseURL:        p.BaseURL,
		ModelsURL:      p.ModelsURL,
		ExtraHeaders:   cloneStringMap(p.ExtraHeaders),
		DisguiseClient: p.DisguiseClient,
		DefaultModel:   p.DefaultModel,
		Enabled:        p.EnabledValue(),
		Models:         providerModelsPayload(p.Models),
		HasSecret:      s.hasSecret(ctx, p.SecretRef),
		Capabilities: CapabilitiesPayload{
			ImageInput:        p.Capabilities.ImageInput,
			Reasoning:         p.Capabilities.Reasoning,
			ContextManagement: p.Capabilities.ContextManagement,
		},
	}
}

// routesReference reports whether any of the four fixed routes points at id.
// Deleting a referenced provider must fail instead of cascading into the
// routes (docs/v1-scheme.md §5.2).
func routesReference(cfg *config.Config, id string) bool {
	for _, r := range []config.Route{cfg.Routes.Codex, cfg.Routes.Claude, cfg.Routes.Grok, cfg.Routes.Generic} {
		if r.Provider == id {
			return true
		}
	}
	return false
}

// validationField extracts the first locatable field from a config
// ValidationError for the error details (docs/v1-scheme.md §9.5).
func validationField(err error) string {
	var ve *config.ValidationError
	if errors.As(err, &ve) && len(ve.Errors) > 0 {
		return ve.Errors[0].Field
	}
	return ""
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.View()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	ids := make([]string, 0, len(cfg.Providers))
	for id := range cfg.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]ProviderResponse, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.providerResponse(r.Context(), id, cfg.Providers[id]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cfg := s.cfg.View()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	p, ok := cfg.Providers[id]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", id), nil)
		return
	}
	writeJSON(w, http.StatusOK, s.providerResponse(r.Context(), id, p))
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var req ProviderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := strings.TrimSpace(req.ID)
	p, err := providerFromRequest(id, req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", err.Error(), map[string]string{"field": validationField(err)})
		return
	}

	// The whole snapshot → key store → config write sequence must be
	// serialized: two writers based on the same stale snapshot would
	// silently drop each other's update (docs/v1-scheme.md §6.3).
	s.txMu.Lock()
	defer s.txMu.Unlock()

	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	if _, exists := cfg.Providers[id]; exists {
		writeAPIError(w, http.StatusConflict, "provider_exists", fmt.Sprintf("provider %q already exists; use PUT to update it", id), nil)
		return
	}
	if p.Enabled == nil {
		p.Enabled = config.BoolPtr(true)
	}

	if req.APIKey != nil && *req.APIKey != "" {
		p.SecretRef = refFor(id)
	}
	resp, err := s.upsertProvider(r.Context(), id, p, keyBytes(req.APIKey))
	if err != nil {
		s.writeTxError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req ProviderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := providerFromRequest(id, req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", err.Error(), map[string]string{"field": validationField(err)})
		return
	}

	// Serialized like every other provider write transaction (see
	// handleCreateProvider).
	s.txMu.Lock()
	defer s.txMu.Unlock()

	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	old, exists := cfg.Providers[id]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", id), nil)
		return
	}

	if req.APIKey != nil && *req.APIKey != "" {
		// A new key normalizes the ref to the canonical namespace.
		p.SecretRef = refFor(id)
	} else {
		// No new key: keep the previous ref so the existing secret survives.
		p.SecretRef = old.SecretRef
	}
	if req.Enabled == nil {
		p.Enabled = old.Enabled
	}
	resp, err := s.upsertProvider(r.Context(), id, p, keyBytes(req.APIKey))
	if err != nil {
		s.writeTxError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// deleteProvider removes a provider from the config first and its secret
// afterwards (docs/v1-scheme.md §6.3). A secret deletion failure must not
// restore the provider but is returned as a warning for doctor to surface as
// an orphan secret. Callers must hold s.txMu.
func (s *Server) deleteProvider(ctx context.Context, id string) (warning string, err error) {
	cfg := s.cfg.View()
	if cfg == nil {
		return "", errors.New("config not loaded")
	}
	p, exists := cfg.Providers[id]
	if !exists {
		return "", fmt.Errorf("%w: provider %q does not exist", errProviderNotFound, id)
	}
	if routesReference(cfg, id) {
		return "", fmt.Errorf("%w: provider %q is referenced by routes; remove the references first", errProviderInUse, id)
	}

	current := cfg
	next := s.cfg.Snapshot()
	if next == nil {
		return "", errors.New("config not loaded")
	}
	delete(next.Providers, id)
	if err := s.syncClientsThenWrite(current, next); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	s.invalidateModels(id)
	if p.SecretRef != "" {
		if err := s.secrets.Delete(ctx, p.SecretRef); err != nil {
			return fmt.Sprintf("provider deleted, but deleting its secret ref %q failed: %v; run doctor to inspect orphan secrets", p.SecretRef, err), nil
		}
	}
	return "", nil
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Serialized like every other provider write transaction (see
	// handleCreateProvider): delete must never interleave with a concurrent
	// create/update on the same provider.
	s.txMu.Lock()
	defer s.txMu.Unlock()

	warning, err := s.deleteProvider(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, errProviderNotFound):
			writeAPIError(w, http.StatusNotFound, "provider_not_found", err.Error(), nil)
		case errors.Is(err, errProviderInUse):
			writeAPIError(w, http.StatusConflict, "provider_in_use", err.Error(), nil)
		default:
			writeAPIError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
		}
		return
	}
	body := map[string]any{"id": id, "deleted": true}
	if warning != "" {
		body["warning"] = warning
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleUpdateProviderAvailability(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req ProviderAvailabilityRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	s.txMu.Lock()
	defer s.txMu.Unlock()
	current := s.cfg.View()
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	p, ok := cfg.Providers[id]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", id), nil)
		return
	}
	if req.Enabled != nil {
		p.Enabled = req.Enabled
	}
	for i := range p.Models {
		if enabled, exists := req.Models[p.Models[i].ID]; exists {
			p.Models[i].Enabled = config.BoolPtr(enabled)
		}
	}
	for modelID, enabled := range req.Models {
		found := false
		for _, model := range p.Models {
			if model.ID == modelID {
				found = true
				break
			}
		}
		if !found && strings.TrimSpace(modelID) != "" {
			p.Models = append(p.Models, config.ProviderModel{ID: strings.TrimSpace(modelID), Enabled: config.BoolPtr(enabled)})
		}
	}
	cfg.Providers[id] = p
	if err := cfg.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", err.Error(), map[string]string{"field": validationField(err)})
		return
	}
	if err := s.syncClientsThenWrite(current, cfg); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "client_sync_failed", err.Error(), nil)
		return
	}
	s.invalidateModels(id)
	writeJSON(w, http.StatusOK, s.providerResponse(r.Context(), id, p))
}

// keyBytes converts an optional api_key payload into a writable byte slice,
// or nil when the request carries no key. An explicitly empty string counts
// as "no key" (writing an empty key would be meaningless).
func keyBytes(apiKey *string) []byte {
	if apiKey == nil || *apiKey == "" {
		return nil
	}
	return []byte(*apiKey)
}

// writeTxError maps transaction errors onto the unified management API error
// shape, keeping secret material out of every message.
func (s *Server) writeTxError(w http.ResponseWriter, err error) {
	var pe *PartialFailureError
	switch {
	case errors.Is(err, secret.ErrUnavailable):
		writeAPIError(w, http.StatusInternalServerError, "secret_store_unavailable", err.Error(), nil)
	case errors.As(err, &pe):
		writeAPIError(w, http.StatusInternalServerError, "partial_failure", pe.Error(), map[string]string{"ref": pe.Ref})
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error(), nil)
	}
}
