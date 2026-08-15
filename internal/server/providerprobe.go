package server

import (
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
	"ai-gateway/internal/secret"
)

const (
	providerProbeTimeout = 15 * time.Second
	maxModelsBody        = 16 << 20
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
	ID          string `json:"id"`
	ProviderID  string `json:"provider_id"`
	RawID       string `json:"raw_id"`
	DisplayName string `json:"display_name,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
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
}

func (s *Server) handleProbeProvider(w http.ResponseWriter, r *http.Request) {
	id, p, ok := s.lookupProvider(w, r.PathValue("id"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), providerProbeTimeout)
	defer cancel()
	started := time.Now()
	models, status, err := s.fetchProviderModels(ctx, id, p)
	result := ProbeResponse{OK: err == nil, Status: status, LatencyMS: time.Since(started).Milliseconds(), Models: len(models)}
	if err != nil {
		result.Error = err.Error()
	}
	writeJSON(w, http.StatusOK, result)
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
	endpoint, err := providerModelsURL(p)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ai-gateway/provider-probe")
	var key []byte
	if p.SecretRef != "" {
		key, err = s.secrets.Get(ctx, p.SecretRef)
		if err != nil {
			if errors.Is(err, secret.ErrNotFound) {
				return nil, 0, fmt.Errorf("provider %q secret is missing", id)
			}
			return nil, 0, fmt.Errorf("read provider %q secret: %w", id, err)
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
		return nil, 0, fmt.Errorf("models request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxModelsBody))
		return nil, resp.StatusCode, fmt.Errorf("upstream returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelsBody+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read models response: %w", err)
	}
	if len(body) > maxModelsBody {
		return nil, resp.StatusCode, fmt.Errorf("models response exceeds %d bytes", maxModelsBody)
	}
	var doc struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			OwnedBy     string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode models response: %w", err)
	}
	seen := map[string]bool{}
	models := make([]ProviderModel, 0, len(doc.Data))
	for _, model := range doc.Data {
		rawID := strings.TrimSpace(model.ID)
		if rawID == "" || seen[rawID] {
			continue
		}
		seen[rawID] = true
		models = append(models, ProviderModel{ID: id + "/" + rawID, ProviderID: id, RawID: rawID, DisplayName: model.DisplayName, OwnedBy: model.OwnedBy})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].RawID < models[j].RawID })
	s.cacheModels(id, models)
	return models, resp.StatusCode, nil
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
