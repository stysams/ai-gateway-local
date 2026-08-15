package server

import (
	"fmt"
	"net/http"

	"ai-gateway/internal/config"
)

type ConfigPayload struct {
	Version   int                              `json:"version"`
	Listen    ConfigListenPayload              `json:"listen"`
	Logging   ConfigLoggingPayload             `json:"logging"`
	UI        ConfigUIPayload                  `json:"ui"`
	Autostart ConfigAutostartPayload           `json:"autostart"`
	Providers map[string]ConfigProviderPayload `json:"providers"`
	Routes    ConfigRoutesPayload              `json:"routes"`
}

type ConfigListenPayload struct {
	Host string `json:"host,omitempty"`
	Port int    `json:"port"`
}
type ConfigLoggingPayload struct {
	Enabled bool   `json:"enabled"`
	Dir     string `json:"dir"`
}
type ConfigUIPayload struct {
	Language              string `json:"language"`
	LoggingNoticeAccepted bool   `json:"logging_notice_accepted"`
}
type ConfigAutostartPayload struct {
	Enabled bool `json:"enabled"`
}
type ConfigProviderPayload struct {
	Name         string                 `json:"name"`
	Adapter      string                 `json:"adapter"`
	BaseURL      string                 `json:"base_url"`
	ModelsURL    string                 `json:"models_url,omitempty"`
	DefaultModel string                 `json:"default_model"`
	Enabled      *bool                  `json:"enabled,omitempty"`
	Models       []ProviderModelPayload `json:"models"`
	SecretRef    string                 `json:"secret_ref,omitempty"`
	Capabilities CapabilitiesPayload    `json:"capabilities"`
}
type ConfigRoutesPayload struct {
	Codex   RouteStatus `json:"codex"`
	Claude  RouteStatus `json:"claude"`
	Grok    RouteStatus `json:"grok"`
	Generic RouteStatus `json:"generic"`
}

func configPayload(cfg *config.Config) ConfigPayload {
	out := ConfigPayload{
		Version:   cfg.Version,
		Listen:    ConfigListenPayload{Port: cfg.Listen.PortValue()},
		Logging:   ConfigLoggingPayload{Enabled: cfg.Logging.EnabledValue(), Dir: cfg.Logging.Dir},
		UI:        ConfigUIPayload{Language: cfg.UI.Language, LoggingNoticeAccepted: cfg.UI.LoggingNoticeAccepted},
		Autostart: ConfigAutostartPayload{Enabled: cfg.Autostart.Enabled},
		Providers: make(map[string]ConfigProviderPayload, len(cfg.Providers)),
		Routes: ConfigRoutesPayload{
			Codex: routeStatus(cfg.Routes.Codex), Claude: routeStatus(cfg.Routes.Claude),
			Grok: routeStatus(cfg.Routes.Grok), Generic: routeStatus(cfg.Routes.Generic),
		},
	}
	for id, p := range cfg.Providers {
		out.Providers[id] = ConfigProviderPayload{
			Name: p.Name, Adapter: p.Adapter, BaseURL: p.BaseURL, ModelsURL: p.ModelsURL,
			DefaultModel: p.DefaultModel, SecretRef: p.SecretRef,
			Enabled:      config.BoolPtr(p.EnabledValue()),
			Models:       providerModelsPayload(p.Models),
			Capabilities: CapabilitiesPayload{ImageInput: p.Capabilities.ImageInput, Reasoning: p.Capabilities.Reasoning},
		}
	}
	out.Listen.Host = cfg.Listen.HostValue()
	return out
}

func routeStatus(r config.Route) RouteStatus {
	return RouteStatus{Provider: r.Provider, Model: r.Model}
}

func (p ConfigPayload) toConfig() *config.Config {
	providers := make(map[string]config.Provider, len(p.Providers))
	for id, provider := range p.Providers {
		providers[id] = config.Provider{
			Name: provider.Name, Adapter: provider.Adapter, BaseURL: provider.BaseURL, ModelsURL: provider.ModelsURL,
			DefaultModel: provider.DefaultModel, SecretRef: provider.SecretRef,
			Enabled:      provider.Enabled,
			Models:       providerModelsFromPayload(provider.Models),
			Capabilities: config.Capabilities{ImageInput: provider.Capabilities.ImageInput, Reasoning: provider.Capabilities.Reasoning},
		}
	}
	return &config.Config{
		Version:   p.Version,
		Listen:    config.Listen{Host: p.Listen.Host, Port: config.IntPtr(p.Listen.Port)},
		Logging:   config.Logging{Enabled: config.BoolPtr(p.Logging.Enabled), Dir: p.Logging.Dir},
		UI:        config.UI{Language: p.UI.Language, LoggingNoticeAccepted: p.UI.LoggingNoticeAccepted},
		Autostart: config.Autostart{Enabled: p.Autostart.Enabled},
		Providers: providers,
		Routes: config.Routes{
			Codex:   config.Route{Provider: p.Routes.Codex.Provider, Model: p.Routes.Codex.Model},
			Claude:  config.Route{Provider: p.Routes.Claude.Provider, Model: p.Routes.Claude.Model},
			Grok:    config.Route{Provider: p.Routes.Grok.Provider, Model: p.Routes.Grok.Model},
			Generic: config.Route{Provider: p.Routes.Generic.Provider, Model: p.Routes.Generic.Model},
		},
	}
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	writeJSON(w, http.StatusOK, configPayload(cfg))
}

func (s *Server) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var payload ConfigPayload
	if !decodeJSON(w, r, &payload) {
		return
	}

	s.txMu.Lock()
	defer s.txMu.Unlock()
	current := s.cfg.Snapshot()
	if current == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	if payload.Autostart.Enabled != current.Autostart.Enabled {
		writeAPIError(w, http.StatusConflict, "autostart_requires_endpoint", "change autostart through PUT /api/v1/autostart so operating-system registration stays consistent", nil)
		return
	}
	next := payload.toConfig()
	next.Extra = current.Extra
	if err := next.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", err.Error(), map[string]string{"field": validationField(err)})
		return
	}
	baseURL := s.ClientBaseURL(current)
	applied, err := s.applyDisplayModelChanges(baseURL, displayModelChanges(current, next))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "client_sync_failed", err.Error(), nil)
		return
	}
	if err := s.cfg.Write(next); err != nil {
		if rollbackErr := s.rollbackDisplayModelChanges(baseURL, applied); rollbackErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "partial_failure", fmt.Sprintf("write config: %v; rollback client display models: %v", err, rollbackErr), nil)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
		return
	}
	s.invalidateModels("")
	writeJSON(w, http.StatusOK, configPayload(next))
}
