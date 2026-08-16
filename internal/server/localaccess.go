package server

import (
	"net/http"

	"ai-gateway/internal/config"
	"ai-gateway/internal/route"
)

const localAccessAPIKeyPlaceholder = "ai-gateway"

// LocalAccessEndpoints lists the stable protocol URLs local applications can
// configure without needing to understand the management API.
type LocalAccessEndpoints struct {
	Models          string `json:"models"`
	ChatCompletions string `json:"chat_completions"`
	Responses       string `json:"responses"`
	Messages        string `json:"messages"`
}

// LocalAccessResponse describes the loopback data plane and its current model
// catalog. APIKey is a non-secret placeholder for clients that require a
// non-empty OpenAI API key field; inbound credentials are never forwarded.
type LocalAccessResponse struct {
	BaseURL      string               `json:"base_url"`
	APIKey       string               `json:"api_key"`
	AuthRequired bool                 `json:"auth_required"`
	DefaultModel string               `json:"default_model"`
	DefaultRoute RouteStatus          `json:"default_route"`
	Endpoints    LocalAccessEndpoints `json:"endpoints"`
	Models       []modelItem          `json:"models"`
}

func (s *Server) handleLocalAccess(w http.ResponseWriter, _ *http.Request) {
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	writeJSON(w, http.StatusOK, s.localAccessResponse(cfg))
}

func (s *Server) localAccessResponse(cfg *config.Config) LocalAccessResponse {
	root := s.ClientBaseURL(cfg)
	baseURL := root + "/v1"
	return LocalAccessResponse{
		BaseURL:      baseURL,
		APIKey:       localAccessAPIKeyPlaceholder,
		AuthRequired: false,
		DefaultModel: route.ReservedModel,
		DefaultRoute: RouteStatus{Provider: cfg.Routes.Generic.Provider, Model: cfg.Routes.Generic.Model},
		Endpoints: LocalAccessEndpoints{
			Models:          baseURL + "/models",
			ChatCompletions: baseURL + "/chat/completions",
			Responses:       baseURL + "/responses",
			Messages:        baseURL + "/messages",
		},
		Models: s.modelCatalog(cfg),
	}
}
