package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"ai-gateway/internal/config"
)

var (
	errKeyGroupNotFound = errors.New("key group not found")
	errKeyGroupInUse    = errors.New("key group is referenced by routes")
	errModelInUse       = errors.New("model is referenced by routes")
)

func keyGroupFromPayload(payload KeyGroupPayload, existing *config.KeyGroup) config.KeyGroup {
	group := config.KeyGroup{
		Name: payload.Name, Enabled: payload.Enabled, Endpoint: strings.TrimSpace(payload.Endpoint),
		Adapter: strings.TrimSpace(payload.Adapter), DefaultModel: strings.TrimSpace(payload.DefaultModel),
		Models: providerModelsFromPayload(payload.Models),
	}
	if existing != nil && payload.APIKey == nil {
		group.APIKey = existing.APIKey
	} else if payload.APIKey != nil {
		group.APIKey = *payload.APIKey
	}
	return group
}

func keyGroupPayload(keyID string, group config.KeyGroup) KeyGroupPayload {
	apiKey := group.APIKey
	return KeyGroupPayload{
		KeyID: keyID, Name: group.Name, Enabled: config.BoolPtr(group.EnabledValue()), APIKey: &apiKey,
		Endpoint: group.Endpoint, Adapter: group.Adapter, DefaultModel: group.DefaultModel,
		Models: providerModelsPayload(group.Models),
	}
}

func duplicateKeyGroups(provider config.Provider, keyID string) []string {
	key := provider.KeyGroups[keyID].APIKey
	if key == "" {
		return nil
	}
	ids := make([]string, 0)
	for id, group := range provider.KeyGroups {
		if id != keyID && group.APIKey == key && group.APIKey != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func keyGroupSummaries(provider config.Provider) []KeyGroupSummary {
	ids := make([]string, 0, len(provider.KeyGroups))
	for id := range provider.KeyGroups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	groups := make([]KeyGroupSummary, 0, len(ids))
	for _, id := range ids {
		group := provider.KeyGroups[id]
		groups = append(groups, KeyGroupSummary{
			KeyID: id, Name: group.Name, Enabled: group.EnabledValue(), HasAPIKey: group.APIKey != "",
			ModelCount: len(group.Models), DefaultModel: group.DefaultModel, Endpoint: group.Endpoint, Adapter: group.Adapter,
			DuplicateCount: len(duplicateKeyGroups(provider, id)),
			Models:         providerModelsPayload(group.Models),
		})
	}
	return groups
}

func keyGroupResponse(providerID, keyID string, provider config.Provider, group config.KeyGroup) KeyGroupResponse {
	models := make([]ProviderModelPayload, 0, len(group.Models))
	for _, model := range providerModelsPayload(group.Models) {
		model.EffectiveEndpoint = group.EffectiveEndpoint(model.ID)
		model.EffectiveProtocol = group.ModelAdapter(model.ID)
		models = append(models, model)
	}
	return KeyGroupResponse{
		ProviderID: providerID, KeyID: keyID, Name: group.Name, Enabled: group.EnabledValue(),
		APIKey: group.APIKey, HasAPIKey: group.APIKey != "", DuplicateKeyGroups: duplicateKeyGroups(provider, keyID),
		Endpoint: group.Endpoint, Adapter: group.Adapter, DefaultModel: group.DefaultModel, Models: models,
	}
}

// KeyGroupResponse is intentionally only returned by explicit key-group
// endpoints. The API key is omitted from provider list/detail responses.
type KeyGroupResponse struct {
	ProviderID         string                 `json:"provider_id"`
	KeyID              string                 `json:"key_id"`
	Name               string                 `json:"name"`
	Enabled            bool                   `json:"enabled"`
	APIKey             string                 `json:"api_key"`
	HasAPIKey          bool                   `json:"has_api_key"`
	DuplicateKeyGroups []string               `json:"duplicate_key_groups,omitempty"`
	Endpoint           string                 `json:"endpoint,omitempty"`
	Adapter            string                 `json:"adapter,omitempty"`
	DefaultModel       string                 `json:"default_model"`
	Models             []ProviderModelPayload `json:"models"`
}

func (s *Server) lookupKeyGroup(w http.ResponseWriter, providerID, keyID string) (config.Provider, config.KeyGroup, bool) {
	cfg := s.cfg.View()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return config.Provider{}, config.KeyGroup{}, false
	}
	provider, ok := cfg.Providers[providerID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", providerID), nil)
		return config.Provider{}, config.KeyGroup{}, false
	}
	group, ok := provider.KeyGroups[keyID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "key_group_not_found", fmt.Sprintf("key group %q does not exist for provider %q", keyID, providerID), nil)
		return config.Provider{}, config.KeyGroup{}, false
	}
	return provider, group, true
}

func (s *Server) handleListKeyGroups(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("id")
	cfg := s.cfg.View()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	provider, ok := cfg.Providers[providerID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", providerID), nil)
		return
	}
	ids := make([]string, 0, len(provider.KeyGroups))
	for id := range provider.KeyGroups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]KeyGroupResponse, 0, len(ids))
	for _, id := range ids {
		out = append(out, keyGroupResponse(providerID, id, provider, provider.KeyGroups[id]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetKeyGroup(w http.ResponseWriter, r *http.Request) {
	providerID, keyID := r.PathValue("id"), r.PathValue("key_id")
	provider, group, ok := s.lookupKeyGroup(w, providerID, keyID)
	if ok {
		writeJSON(w, http.StatusOK, keyGroupResponse(providerID, keyID, provider, group))
	}
}

func (s *Server) commitKeyGroup(ctx context.Context, providerID, keyID string, group config.KeyGroup) error {
	current := s.cfg.View()
	next := s.cfg.Snapshot()
	if current == nil || next == nil {
		return errors.New("config not loaded")
	}
	provider, ok := next.Providers[providerID]
	if !ok {
		return fmt.Errorf("%w: provider %q", errProviderNotFound, providerID)
	}
	if provider.KeyGroups == nil {
		provider.KeyGroups = make(map[string]config.KeyGroup)
	}
	provider.KeyGroups[keyID] = group
	next.Providers[providerID] = provider
	if err := next.Validate(); err != nil {
		return err
	}
	if err := s.syncClientsThenWrite(current, next); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	s.invalidateModels(modelCacheKey(providerID, keyID))
	return nil
}

func (s *Server) handleCreateKeyGroup(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("id")
	var payload KeyGroupPayload
	if !decodeJSON(w, r, &payload) {
		return
	}
	keyID := strings.TrimSpace(payload.KeyID)
	if keyID == "" {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", "key_id must not be empty", map[string]string{"field": "key_id"})
		return
	}
	s.txMu.Lock()
	defer s.txMu.Unlock()
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	provider, ok := cfg.Providers[providerID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", providerID), nil)
		return
	}
	if _, exists := provider.KeyGroups[keyID]; exists {
		writeAPIError(w, http.StatusConflict, "key_group_exists", fmt.Sprintf("key group %q already exists", keyID), nil)
		return
	}
	group := keyGroupFromPayload(payload, nil)
	if err := s.commitKeyGroup(r.Context(), providerID, keyID, group); err != nil {
		writeKeyGroupError(w, err)
		return
	}
	provider.KeyGroups[keyID] = group
	writeJSON(w, http.StatusCreated, keyGroupResponse(providerID, keyID, provider, group))
}

func (s *Server) handleUpdateKeyGroup(w http.ResponseWriter, r *http.Request) {
	providerID, keyID := r.PathValue("id"), r.PathValue("key_id")
	var payload KeyGroupPayload
	if !decodeJSON(w, r, &payload) {
		return
	}
	s.txMu.Lock()
	defer s.txMu.Unlock()
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	provider, exists := cfg.Providers[providerID]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", providerID), nil)
		return
	}
	old, exists := provider.KeyGroups[keyID]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "key_group_not_found", fmt.Sprintf("key group %q does not exist for provider %q", keyID, providerID), nil)
		return
	}
	if removedRouteModel(cfg, providerID, keyID, old, keyGroupFromPayload(payload, &old)) {
		writeAPIError(w, http.StatusConflict, "model_in_use", "a route still references a model that would be removed or disabled", map[string]string{"provider": providerID, "key_id": keyID})
		return
	}
	group := keyGroupFromPayload(payload, &old)
	if err := s.commitKeyGroup(r.Context(), providerID, keyID, group); err != nil {
		writeKeyGroupError(w, err)
		return
	}
	provider.KeyGroups[keyID] = group
	writeJSON(w, http.StatusOK, keyGroupResponse(providerID, keyID, provider, group))
}

func (s *Server) handleDeleteKeyGroup(w http.ResponseWriter, r *http.Request) {
	providerID, keyID := r.PathValue("id"), r.PathValue("key_id")
	s.txMu.Lock()
	defer s.txMu.Unlock()
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	provider, exists := cfg.Providers[providerID]
	if !exists {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", providerID), nil)
		return
	}
	if _, exists := provider.KeyGroups[keyID]; !exists {
		writeAPIError(w, http.StatusNotFound, "key_group_not_found", fmt.Sprintf("key group %q does not exist for provider %q", keyID, providerID), nil)
		return
	}
	if len(provider.KeyGroups) == 1 {
		writeAPIError(w, http.StatusConflict, "last_key_group", "cannot delete the last key group; delete the provider instead", nil)
		return
	}
	if routesReferenceKeyGroup(cfg, providerID, keyID) {
		writeAPIError(w, http.StatusConflict, "key_group_in_use", fmt.Sprintf("key group %q is referenced by routes", keyID), nil)
		return
	}
	next := s.cfg.Snapshot()
	delete(next.Providers[providerID].KeyGroups, keyID)
	if err := s.syncClientsThenWrite(cfg, next); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
		return
	}
	s.invalidateModels(modelCacheKey(providerID, keyID))
	writeJSON(w, http.StatusOK, map[string]any{"provider_id": providerID, "key_id": keyID, "deleted": true})
}

func (s *Server) handleProbeKeyGroup(w http.ResponseWriter, r *http.Request) {
	providerID, keyID := r.PathValue("id"), r.PathValue("key_id")
	provider, group, ok := s.lookupKeyGroup(w, providerID, keyID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), providerProbeTimeout)
	defer cancel()
	started := timeNow()
	status, err, response := s.probeKeyGroup(ctx, providerID, keyID, provider, group)
	result := ProbeResponse{OK: err == nil, Status: status, LatencyMS: timeNow().Sub(started).Milliseconds(), Models: len(group.Models), Response: response}
	if err != nil {
		result.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, result)
}

func routesReferenceKeyGroup(cfg *config.Config, providerID, keyID string) bool {
	for _, route := range []config.Route{cfg.Routes.Codex, cfg.Routes.Claude, cfg.Routes.ClaudeDesktop, cfg.Routes.Grok, cfg.Routes.Generic} {
		if route.Provider == providerID && route.KeyID == keyID {
			return true
		}
	}
	return false
}

func removedRouteModel(cfg *config.Config, providerID, keyID string, before, after config.KeyGroup) bool {
	for _, route := range []config.Route{cfg.Routes.Codex, cfg.Routes.Claude, cfg.Routes.ClaudeDesktop, cfg.Routes.Grok, cfg.Routes.Generic} {
		if route.Provider == providerID && route.KeyID == keyID && before.ModelEnabled(route.Model) && !after.ModelEnabled(route.Model) {
			return true
		}
	}
	return false
}

func writeKeyGroupError(w http.ResponseWriter, err error) {
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", err.Error(), map[string]string{"field": validationField(err)})
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
}

// timeNow is a small seam for tests that only need to assert non-negative
// probe latency without coupling the handler to a clock implementation.
var timeNow = func() time.Time { return time.Now() }
