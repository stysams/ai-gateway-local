package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/outbound/anthropic"
	"ai-gateway/internal/outbound/openaichat"
	"ai-gateway/internal/outbound/openairesponses"
	"ai-gateway/internal/secret"
)

const (
	providerProbeTimeout = 15 * time.Second
	maxModelsBody        = 16 << 20
	probePrompt          = "表明你的身份"
)

var providerProbeClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: providerProbeTimeout,
	},
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}

type ProviderModel struct {
	ID              string `json:"id"`
	ProviderID      string `json:"provider_id"`
	RawID           string `json:"raw_id"`
	DisplayName     string `json:"display_name,omitempty"`
	OwnedBy         string `json:"owned_by,omitempty"`
	ContextWindow   int    `json:"context_window,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

type DiscoverProviderModelsRequest struct {
	ProviderID   string            `json:"provider_id"`
	Adapter      string            `json:"adapter"`
	BaseURL      string            `json:"base_url"`
	ModelsURL    string            `json:"models_url,omitempty"`
	ExtraHeaders map[string]string `json:"extra_headers"`
	APIKey       *string           `json:"api_key"`
}

type ProviderModelsResponse struct {
	Object   string          `json:"object"`
	Provider string          `json:"provider"`
	Data     []ProviderModel `json:"data"`
}

type ProbeResponse struct {
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Models    int    `json:"models,omitempty"`
	Error     string `json:"error,omitempty"`
	Response  string `json:"response,omitempty"`
}

func (s *Server) handleProbeProvider(w http.ResponseWriter, r *http.Request) {
	id, p, ok := s.lookupProvider(w, r.PathValue("id"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), providerProbeTimeout)
	defer cancel()
	started := time.Now()
	status, err, responseBody := s.probeProvider(ctx, id, p)
	modelCount := len(p.Models)
	if modelCount == 0 && strings.TrimSpace(p.DefaultModel) != "" {
		modelCount = 1
	}
	result := ProbeResponse{OK: err == nil, Status: status, LatencyMS: time.Since(started).Milliseconds(), Models: modelCount, Response: responseBody}
	if err != nil {
		result.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, result)
}

// probeProvider performs a minimal real completion request. Model discovery is
// intentionally kept in fetchProviderModels: probing must validate that the
// configured model can answer a user message, rather than only proving that a
// provider exposes a /models endpoint.
func (s *Server) probeProvider(ctx context.Context, id string, p config.Provider) (int, error, string) {
	var payload any
	var endpoint string
	adapter := p.ModelAdapter(p.DefaultModel)
	switch adapter {
	case "openai-chat":
		endpoint = openaichat.CompletionURL(p.BaseURL)
		payload = map[string]any{
			"model":    p.DefaultModel,
			"messages": []map[string]string{{"role": "user", "content": probePrompt}},
			"stream":   false,
		}
	case "openai-responses":
		endpoint = openairesponses.CompletionURL(p.BaseURL)
		payload = map[string]any{"model": p.DefaultModel, "input": probePrompt, "stream": false}
	case "anthropic":
		endpoint = anthropic.CompletionURL(p.BaseURL)
		payload = map[string]any{
			"model":      p.DefaultModel,
			"max_tokens": 256,
			"messages":   []map[string]string{{"role": "user", "content": probePrompt}},
			"stream":     false,
		}
	default:
		return 0, fmt.Errorf("provider %q model %q uses unsupported adapter %q", id, p.DefaultModel, adapter), ""
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encode probe request: %w", err), ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build probe request: %w", err), ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ai-gateway/provider-probe")
	applyExtraHeaders(req.Header, p.ExtraHeaders)
	key, err := s.providerProbeKey(ctx, p)
	if err != nil {
		return 0, err, ""
	}
	if key != nil {
		defer secret.Zero(key)
		if adapter == "anthropic" {
			req.Header.Set("x-api-key", string(key))
			req.Header.Set("anthropic-version", anthropic.APIVersion)
		} else {
			req.Header.Set("Authorization", "Bearer "+string(key))
		}
	} else if adapter == "anthropic" {
		req.Header.Set("anthropic-version", anthropic.APIVersion)
	}
	resp, err := providerProbeClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("probe request failed: %w", err), ""
	}
	defer resp.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxProbeResponse+1))
	if readErr != nil {
		return resp.StatusCode, fmt.Errorf("read probe response: %w", readErr), ""
	}
	if len(responseBody) > maxProbeResponse {
		responseBody = responseBody[:maxProbeResponse]
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("upstream returned %s", resp.Status), ""
	}
	return resp.StatusCode, nil, formatProbeResponse(responseBody)
}

func (s *Server) providerProbeKey(ctx context.Context, p config.Provider) ([]byte, error) {
	if p.SecretRef == "" {
		return nil, nil
	}
	if s.secrets == nil {
		return nil, errors.New("provider secret store is unavailable")
	}
	key, err := s.secrets.Get(ctx, p.SecretRef)
	if err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			return nil, fmt.Errorf("provider secret %q is missing", p.SecretRef)
		}
		return nil, fmt.Errorf("read provider secret: %w", err)
	}
	return key, nil
}

func formatProbeResponse(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, trimmed, "", "  "); err == nil {
		return formatted.String()
	}
	return string(trimmed)
}

func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	id, p, ok := s.lookupProvider(w, r.PathValue("id"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), providerProbeTimeout)
	defer cancel()
	models, status, err := s.fetchProviderModels(ctx, id, p)
	if err != nil {
		details := map[string]string{}
		if status != 0 {
			details["upstream_status"] = fmt.Sprint(status)
		}
		writeAPIError(w, http.StatusBadGateway, "provider_models_failed", err.Error(), details)
		return
	}
	writeJSON(w, http.StatusOK, ProviderModelsResponse{Object: "list", Provider: id, Data: models})
}

func (s *Server) handleDiscoverProviderModels(w http.ResponseWriter, r *http.Request) {
	var request DiscoverProviderModelsRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	id := strings.TrimSpace(request.ProviderID)
	p := config.Provider{Name: "model discovery", Adapter: strings.TrimSpace(request.Adapter), BaseURL: strings.TrimSpace(request.BaseURL), ModelsURL: strings.TrimSpace(request.ModelsURL), ExtraHeaders: cloneStringMap(request.ExtraHeaders), DefaultModel: "discovery-placeholder"}
	if existing := s.cfg.Snapshot(); existing != nil {
		if saved, ok := existing.Providers[id]; ok {
			p.SecretRef = saved.SecretRef
		}
	}
	if err := config.ValidateProvider(id, p); err != nil {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", err.Error(), map[string]string{"field": validationField(err)})
		return
	}
	var key []byte
	if request.APIKey != nil && *request.APIKey != "" {
		key = []byte(*request.APIKey)
		defer secret.Zero(key)
	}
	ctx, cancel := context.WithTimeout(r.Context(), providerProbeTimeout)
	defer cancel()
	models, status, err := s.fetchProviderModelsWithKey(ctx, id, p, key, false)
	if err != nil {
		details := map[string]string{}
		if status != 0 {
			details["upstream_status"] = fmt.Sprint(status)
		}
		writeAPIError(w, http.StatusBadGateway, "provider_models_failed", err.Error(), details)
		return
	}
	writeJSON(w, http.StatusOK, ProviderModelsResponse{Object: "list", Provider: id, Data: models})
}

func (s *Server) lookupProvider(w http.ResponseWriter, id string) (string, config.Provider, bool) {
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return "", config.Provider{}, false
	}
	p, ok := cfg.Providers[id]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "provider_not_found", fmt.Sprintf("provider %q does not exist", id), nil)
		return "", config.Provider{}, false
	}
	return id, p, true
}

func (s *Server) fetchProviderModels(ctx context.Context, id string, p config.Provider) ([]ProviderModel, int, error) {
	models, status, err, _ := s.fetchProviderModelsDetailed(ctx, id, p, true)
	return models, status, err
}

func (s *Server) fetchProviderModelsWithKey(ctx context.Context, id string, p config.Provider, suppliedKey []byte, cache bool) ([]ProviderModel, int, error) {
	models, status, err, _ := s.fetchProviderModelsDetailedWithKey(ctx, id, p, suppliedKey, cache)
	return models, status, err
}

func (s *Server) fetchProviderModelsDetailed(ctx context.Context, id string, p config.Provider, cache bool) ([]ProviderModel, int, error, string) {
	return s.fetchProviderModelsDetailedWithKey(ctx, id, p, nil, cache)
}

func (s *Server) fetchProviderModelsDetailedWithKey(ctx context.Context, id string, p config.Provider, suppliedKey []byte, cache bool) ([]ProviderModel, int, error, string) {
	endpoint, err := providerModelsURL(p)
	if err != nil {
		return nil, 0, err, ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build models request: %w", err), ""
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ai-gateway/provider-probe")
	applyExtraHeaders(req.Header, p.ExtraHeaders)
	key := suppliedKey
	if len(key) == 0 && p.SecretRef != "" {
		key, err = s.secrets.Get(ctx, p.SecretRef)
		if err != nil {
			if errors.Is(err, secret.ErrNotFound) {
				return nil, 0, fmt.Errorf("provider %q secret is missing", id), ""
			}
			return nil, 0, fmt.Errorf("read provider %q secret: %w", id, err), ""
		}
		defer secret.Zero(key)
	}
	if p.Adapter == "anthropic" {
		if len(key) > 0 {
			req.Header.Set("x-api-key", string(key))
		}
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if len(key) > 0 {
		req.Header.Set("Authorization", "Bearer "+string(key))
	}
	resp, err := providerProbeClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("models request failed: %w", err), ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxModelsBody))
		return nil, resp.StatusCode, fmt.Errorf("upstream returned %s", resp.Status), sanitizeProbeBody(body, false)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBody+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read models response: %w", err), ""
	}
	if len(body) > maxModelsBody {
		return nil, resp.StatusCode, fmt.Errorf("models response exceeds %d bytes", maxModelsBody), ""
	}
	var doc struct {
		Data []struct {
			ID                  string `json:"id"`
			Name                string `json:"name"`
			DisplayName         string `json:"display_name"`
			OwnedBy             string `json:"owned_by"`
			ContextLength       int    `json:"context_length"`
			ContextWindow       int    `json:"context_window"`
			MaxCompletionTokens int    `json:"max_completion_tokens"`
			MaxOutputTokens     int    `json:"max_output_tokens"`
			TopProvider         struct {
				ContextLength       int `json:"context_length"`
				MaxCompletionTokens int `json:"max_completion_tokens"`
			} `json:"top_provider"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode models response: %w", err), sanitizeProbeBody(body, true)
	}
	seen := map[string]bool{}
	models := make([]ProviderModel, 0, len(doc.Data))
	for _, model := range doc.Data {
		rawID := strings.TrimSpace(model.ID)
		if rawID == "" || seen[rawID] {
			continue
		}
		seen[rawID] = true
		displayName := strings.TrimSpace(model.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSpace(model.Name)
		}
		contextWindow := model.ContextLength
		if model.TopProvider.ContextLength > 0 {
			contextWindow = model.TopProvider.ContextLength
		} else if contextWindow == 0 {
			contextWindow = model.ContextWindow
		}
		if contextWindow < 0 {
			contextWindow = 0
		}
		maxOutputTokens := model.TopProvider.MaxCompletionTokens
		if maxOutputTokens == 0 {
			maxOutputTokens = model.MaxCompletionTokens
		}
		if maxOutputTokens == 0 {
			maxOutputTokens = model.MaxOutputTokens
		}
		if maxOutputTokens < 0 {
			maxOutputTokens = 0
		}
		models = append(models, ProviderModel{ID: id + "/" + rawID, ProviderID: id, RawID: rawID, DisplayName: displayName, OwnedBy: model.OwnedBy, ContextWindow: contextWindow, MaxOutputTokens: maxOutputTokens})
	}
	if cache {
		sort.Slice(models, func(i, j int) bool { return models[i].RawID < models[j].RawID })
		s.cacheModels(id, models)
	}
	return models, resp.StatusCode, nil, sanitizeProbeBody(body, true)
}

func applyExtraHeaders(target http.Header, extra map[string]string) {
	for name, value := range extra {
		target.Set(name, value)
	}
}

const maxProbeResponse = 256 << 10

func sanitizeProbeBody(body []byte, success bool) string {
	if !success {
		return ""
	}
	if len(body) > maxProbeResponse {
		body = body[:maxProbeResponse]
	}
	return string(body)
}

func (s *Server) cacheModels(id string, models []ProviderModel) {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	s.modelCache[id] = append([]ProviderModel(nil), models...)
}

func (s *Server) invalidateModels(id string) {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	if id == "" {
		s.modelCache = make(map[string][]ProviderModel)
		return
	}
	delete(s.modelCache, id)
}

func (s *Server) cachedModels() map[string][]ProviderModel {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	out := make(map[string][]ProviderModel, len(s.modelCache))
	for id, models := range s.modelCache {
		out[id] = append([]ProviderModel(nil), models...)
	}
	return out
}

func providerModelsURL(p config.Provider) (string, error) {
	base, err := url.Parse(strings.TrimRight(p.BaseURL, "/"))
	if err != nil {
		return "", fmt.Errorf("parse provider base URL: %w", err)
	}
	if strings.TrimSpace(p.ModelsURL) != "" {
		custom, err := url.Parse(strings.TrimSpace(p.ModelsURL))
		if err != nil || custom.Scheme == "" || custom.Host == "" {
			return "", fmt.Errorf("parse provider models URL: %w", err)
		}
		return custom.String(), nil
	}
	path := strings.TrimRight(base.Path, "/")
	if p.Adapter == "anthropic" && !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}
	base.Path = path + "/models"
	if p.Adapter == "anthropic" {
		q := base.Query()
		q.Set("limit", "1000")
		base.RawQuery = q.Encode()
	}
	return base.String(), nil
}
