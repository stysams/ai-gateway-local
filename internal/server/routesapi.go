package server

import (
	"fmt"
	"net/http"
	"strings"

	"ai-gateway/internal/config"
	"ai-gateway/internal/route"
)

type RouteRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (s *Server) handlePutRoute(w http.ResponseWriter, r *http.Request) {
	client, err := route.ParseClientID(r.PathValue("client"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "client_not_found", err.Error(), nil)
		return
	}
	var req RouteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", "route model must not be empty", map[string]string{"field": "routes." + string(client) + ".model"})
		return
	}

	s.txMu.Lock()
	defer s.txMu.Unlock()
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	if _, ok := cfg.Providers[req.Provider]; !ok {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", fmt.Sprintf("provider %q does not exist", req.Provider), map[string]string{"field": "routes." + string(client) + ".provider"})
		return
	}
	next := config.Route{Provider: req.Provider, Model: req.Model}
	switch client {
	case route.Codex:
		cfg.Routes.Codex = next
	case route.Claude:
		cfg.Routes.Claude = next
	case route.ClaudeDesktop:
		cfg.Routes.ClaudeDesktop = next
	case route.Grok:
		cfg.Routes.Grok = next
	case route.Generic:
		cfg.Routes.Generic = next
	}
	baseURL := s.ClientBaseURL(cfg)
	current := s.cfg.View()
	changes := s.clientSettingsChanges(current, cfg)
	applied, err := s.applyClientSettingsChanges(baseURL, changes)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "client_sync_failed", err.Error(), nil)
		return
	}
	if err := s.cfg.Write(cfg); err != nil {
		if rollbackErr := s.rollbackClientSettingsChanges(baseURL, applied); rollbackErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "partial_failure", fmt.Sprintf("write route config: %v; rollback client settings: %v", err, rollbackErr), nil)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": client, "provider": next.Provider, "model": next.Model})
}
