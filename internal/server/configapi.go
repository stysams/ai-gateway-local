package server

import (
	"fmt"
	"net/http"
	"strings"

	"ai-gateway/internal/config"
)

type ConfigPayload struct {
	Version   int                              `json:"version"`
	Listen    ConfigListenPayload              `json:"listen"`
	Logging   ConfigLoggingPayload             `json:"logging"`
	Limits    *ConfigLimitsPayload             `json:"limits,omitempty"`
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
	Enabled       bool   `json:"enabled"`
	Body          *bool  `json:"body,omitempty"`
	Redact        *bool  `json:"redact,omitempty"`
	Dir           string `json:"dir"`
	RetentionDays int    `json:"retention_days"`
	QuotaBytes    int    `json:"quota_bytes"`
}
type ConfigLimitsPayload struct {
	Global              int `json:"global"`
	PerClient           int `json:"per_client"`
	PerProvider         int `json:"per_provider"`
	StreamIdleSeconds   int `json:"stream_idle_seconds"`
	RequestBodyBytes    int `json:"request_body_bytes"`
	RequestHeaderBytes  int `json:"request_header_bytes"`
	ClientRatePerMinute int `json:"client_rate_per_minute"`
}
type ConfigUIPayload struct {
	Language              string `json:"language"`
	LoggingNoticeAccepted bool   `json:"logging_notice_accepted"`
}
type ConfigAutostartPayload struct {
	Enabled bool `json:"enabled"`
}
type ConfigProviderPayload struct {
	Name           string                 `json:"name"`
	Adapter        string                 `json:"adapter"`
	BaseURL        string                 `json:"base_url"`
	ModelsURL      string                 `json:"models_url,omitempty"`
	ExtraHeaders   map[string]string      `json:"extra_headers,omitempty"`
	DisguiseClient string                 `json:"disguise_client,omitempty"`
	DefaultModel   string                 `json:"default_model"`
	Enabled        *bool                  `json:"enabled,omitempty"`
	Models         []ProviderModelPayload `json:"models"`
	SecretRef      string                 `json:"secret_ref,omitempty"`
	Capabilities   CapabilitiesPayload    `json:"capabilities"`
}
type ConfigRoutesPayload struct {
	Codex         RouteStatus `json:"codex"`
	Claude        RouteStatus `json:"claude"`
	ClaudeDesktop RouteStatus `json:"claude_desktop"`
	Grok          RouteStatus `json:"grok"`
	Generic       RouteStatus `json:"generic"`
}

func configPayload(cfg *config.Config) ConfigPayload {
	out := ConfigPayload{
		Version:   cfg.Version,
		Listen:    ConfigListenPayload{Port: cfg.Listen.PortValue()},
		Logging:   ConfigLoggingPayload{Enabled: cfg.Logging.EnabledValue(), Body: config.BoolPtr(cfg.Logging.BodyValue()), Redact: config.BoolPtr(cfg.Logging.RedactValue()), Dir: cfg.Logging.Dir, RetentionDays: cfg.Logging.RetentionDays, QuotaBytes: cfg.Logging.QuotaBytes},
		Limits:    &ConfigLimitsPayload{Global: cfg.Limits.Global, PerClient: cfg.Limits.PerClient, PerProvider: cfg.Limits.PerProvider, StreamIdleSeconds: cfg.Limits.StreamIdleSeconds, RequestBodyBytes: cfg.Limits.RequestBodyBytes, RequestHeaderBytes: cfg.Limits.RequestHeaderBytes, ClientRatePerMinute: cfg.Limits.ClientRatePerMinute},
		UI:        ConfigUIPayload{Language: cfg.UI.Language, LoggingNoticeAccepted: cfg.UI.LoggingNoticeAccepted},
		Autostart: ConfigAutostartPayload{Enabled: cfg.Autostart.Enabled},
		Providers: make(map[string]ConfigProviderPayload, len(cfg.Providers)),
		Routes: ConfigRoutesPayload{
			Codex: routeStatus(cfg.Routes.Codex), Claude: routeStatus(cfg.Routes.Claude),
			ClaudeDesktop: routeStatus(cfg.Routes.ClaudeDesktop),
			Grok:          routeStatus(cfg.Routes.Grok), Generic: routeStatus(cfg.Routes.Generic),
		},
	}
	for id, p := range cfg.Providers {
		out.Providers[id] = ConfigProviderPayload{
			Name: p.Name, Adapter: p.Adapter, BaseURL: p.BaseURL, ModelsURL: p.ModelsURL,
			ExtraHeaders: cloneStringMap(p.ExtraHeaders), DisguiseClient: p.DisguiseClient,
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
	limits := config.Limits{}
	if p.Limits != nil {
		limits = config.Limits{
			Global: p.Limits.Global, PerClient: p.Limits.PerClient, PerProvider: p.Limits.PerProvider,
			StreamIdleSeconds: p.Limits.StreamIdleSeconds, RequestBodyBytes: p.Limits.RequestBodyBytes,
			RequestHeaderBytes: p.Limits.RequestHeaderBytes, ClientRatePerMinute: p.Limits.ClientRatePerMinute,
		}
	}
	providers := make(map[string]config.Provider, len(p.Providers))
	for id, provider := range p.Providers {
		providers[id] = config.Provider{
			Name: provider.Name, Adapter: provider.Adapter, BaseURL: provider.BaseURL, ModelsURL: provider.ModelsURL,
			ExtraHeaders: cloneStringMap(provider.ExtraHeaders), DisguiseClient: strings.TrimSpace(provider.DisguiseClient),
			DefaultModel: provider.DefaultModel, SecretRef: provider.SecretRef,
			Enabled:      provider.Enabled,
			Models:       providerModelsFromPayload(provider.Models),
			Capabilities: config.Capabilities{ImageInput: provider.Capabilities.ImageInput, Reasoning: provider.Capabilities.Reasoning},
		}
	}
	return &config.Config{
		Version:   p.Version,
		Listen:    config.Listen{Host: p.Listen.Host, Port: config.IntPtr(p.Listen.Port)},
		Logging:   config.Logging{Enabled: config.BoolPtr(p.Logging.Enabled), Body: p.Logging.Body, Redact: p.Logging.Redact, Dir: p.Logging.Dir, RetentionDays: p.Logging.RetentionDays, QuotaBytes: p.Logging.QuotaBytes},
		Limits:    limits,
		UI:        config.UI{Language: p.UI.Language, LoggingNoticeAccepted: p.UI.LoggingNoticeAccepted},
		Autostart: config.Autostart{Enabled: p.Autostart.Enabled},
		Providers: providers,
		Routes: config.Routes{
			Codex:         config.Route{Provider: p.Routes.Codex.Provider, Model: p.Routes.Codex.Model},
			Claude:        config.Route{Provider: p.Routes.Claude.Provider, Model: p.Routes.Claude.Model},
			ClaudeDesktop: config.Route{Provider: p.Routes.ClaudeDesktop.Provider, Model: p.Routes.ClaudeDesktop.Model},
			Grok:          config.Route{Provider: p.Routes.Grok.Provider, Model: p.Routes.Grok.Model},
			Generic:       config.Route{Provider: p.Routes.Generic.Provider, Model: p.Routes.Generic.Model},
		},
	}
}

func (s *Server) handleGetConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.cfg.View()
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
	if payload.Limits == nil {
		next.Limits = current.Limits
	}
	if payload.Logging.Body == nil && current.Logging.Body != nil {
		body := *current.Logging.Body
		next.Logging.Body = &body
	}
	if payload.Logging.Redact == nil && current.Logging.Redact != nil {
		redact := *current.Logging.Redact
		next.Logging.Redact = &redact
	}
	next.Extra = current.Extra
	// Client preferences have dedicated endpoints so a settings save cannot
	// silently drop them (same reason autostart cannot change here).
	next.Clients = current.Clients
	if current.Clients.Codex.RemoteCompaction != nil {
		enabled := *current.Clients.Codex.RemoteCompaction
		next.Clients.Codex.RemoteCompaction = &enabled
	}
	if err := next.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, "config_invalid", err.Error(), map[string]string{"field": validationField(err)})
		return
	}
	baseURL := s.ClientBaseURL(current)
	applied, err := s.applyClientSettingsChanges(baseURL, s.clientSettingsChanges(current, next))
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "client_sync_failed", err.Error(), nil)
		return
	}
	if err := s.cfg.Write(next); err != nil {
		if rollbackErr := s.rollbackClientSettingsChanges(baseURL, applied); rollbackErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "partial_failure", fmt.Sprintf("write config: %v; rollback client settings: %v", err, rollbackErr), nil)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
		return
	}
	s.invalidateModels("")
	writeJSON(w, http.StatusOK, configPayload(next))
}
