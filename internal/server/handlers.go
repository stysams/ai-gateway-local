package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"ai-gateway/internal/config"
)

// StatusResponse is the GET /api/v1/status payload (docs/v1-scheme.md §11.1).
type StatusResponse struct {
	Version            string                  `json:"version"`
	PID                int                     `json:"pid"`
	Listen             string                  `json:"listen"`
	ActiveRequests     int64                   `json:"active_requests"`
	LoggingEnabled     bool                    `json:"logging_enabled"`
	LoggingBodyEnabled bool                    `json:"logging_body_enabled"`
	AutostartEnabled   bool                    `json:"autostart_enabled"`
	Clients            map[string]ClientStatus `json:"clients"`
	Routes             map[string]RouteStatus  `json:"routes"`
}

// ClientStatus reports the point state of one first-class client.
type ClientStatus struct {
	PointState string `json:"point_state"`
}

// RouteStatus reports the current route of one first-class client.
type RouteStatus struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	management := http.NewServeMux()
	management.HandleFunc("GET /api/v1/status", s.handleStatus)
	management.HandleFunc("GET /api/v1/metrics", s.handleMetrics)
	management.HandleFunc("GET /api/v1/local-access", s.handleLocalAccess)
	management.HandleFunc("POST /api/v1/shutdown", s.handleShutdown)
	management.HandleFunc("GET /api/v1/doctor", s.handleDoctor)
	management.HandleFunc("GET /api/v1/config", s.handleGetConfig)
	management.HandleFunc("PUT /api/v1/config", s.handlePutConfig)
	management.HandleFunc("GET /api/v1/logs", s.handleLogs)
	management.HandleFunc("GET /api/v1/logs/{request_id}", s.handleLogDetail)
	management.HandleFunc("GET /api/v1/logs/{request_id}/export", s.handleLogExport)
	management.HandleFunc("DELETE /api/v1/logs/{request_id}", s.handleDeleteLog)
	management.HandleFunc("DELETE /api/v1/logs", s.handleClearLogs)
	management.HandleFunc("GET /api/v1/usage", s.handleUsage)
	management.HandleFunc("PUT /api/v1/logging", s.handleLogging)
	management.HandleFunc("PUT /api/v1/autostart", s.handleAutostart)
	management.HandleFunc("GET /api/v1/providers", s.handleListProviders)
	management.HandleFunc("POST /api/v1/providers", s.handleCreateProvider)
	management.HandleFunc("GET /api/v1/providers/{id}", s.handleGetProvider)
	management.HandleFunc("PUT /api/v1/providers/{id}", s.handleUpdateProvider)
	management.HandleFunc("DELETE /api/v1/providers/{id}", s.handleDeleteProvider)
	management.HandleFunc("PUT /api/v1/providers/{id}/availability", s.handleUpdateProviderAvailability)
	management.HandleFunc("POST /api/v1/providers/{id}/probe", s.handleProbeProvider)
	management.HandleFunc("GET /api/v1/providers/{id}/models", s.handleProviderModels)
	management.HandleFunc("POST /api/v1/provider-models/discover", s.handleDiscoverProviderModels)
	management.HandleFunc("PUT /api/v1/routes/{client}", s.handlePutRoute)
	management.HandleFunc("GET /api/v1/clients/{client}", s.handleGetClient)
	management.HandleFunc("POST /api/v1/clients/{client}/point", s.handlePointClient)
	management.HandleFunc("POST /api/v1/clients/{client}/restore", s.handleRestoreClient)
	management.HandleFunc("PUT /api/v1/clients/{client}/remote-compaction", s.handlePutClientRemoteCompaction)
	management.HandleFunc("PUT /api/v1/clients/{client}/helper-models", s.handlePutClientHelperModels)
	mux.Handle("/api/v1/", loopbackManagementOnly(management))
	// Data plane (task package C): OpenAI Chat same-protocol forwarding.
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /c/{client}/v1/chat/completions", s.handleChatCompletionsClient)
	// Data plane (task package D): Responses and Messages endpoints.
	mux.HandleFunc("POST /v1/responses", s.handleResponses)
	mux.HandleFunc("POST /c/{client}/v1/responses", s.handleResponsesClient)
	mux.HandleFunc("POST /v1/responses/compact", s.handleResponsesCompact)
	mux.HandleFunc("POST /c/{client}/v1/responses/compact", s.handleResponsesCompactClient)
	mux.HandleFunc("POST /v1/messages", s.handleMessages)
	mux.HandleFunc("POST /c/{client}/v1/messages", s.handleMessagesClient)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /c/{client}/v1/models", s.handleModelsClient)
	return mux
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.metricsSnapshot())
}

// handleHealthz answers whether the process and HTTP loop are alive. It never
// touches upstreams (docs/v1-scheme.md §9.2).
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz checks the things the gateway needs before serving traffic
// (docs/v1-scheme.md §9.2): a valid config, a usable system key store and a
// readable secret for every provider that declares a secret_ref. A gateway
// whose providers are all keyless does not depend on the key store.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	var errs []string
	cfg := s.cfg.View()
	if cfg == nil {
		errs = append(errs, "config: not loaded")
	} else if err := cfg.Validate(); err != nil {
		errs = append(errs, "config: "+err.Error())
	}
	if cfg != nil && HasRequiredSecrets(cfg) {
		if err := CheckSecretStore(r.Context(), s.secrets); err != nil {
			errs = append(errs, "secret store: "+err.Error())
		}
		errs = append(errs, CheckRequiredSecrets(r.Context(), s.secrets, cfg)...)
	}
	if len(errs) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "not_ready",
			"errors": errs,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStatus reports version, pid, listener, config-derived flags and the
// fixed four client routes and live point state.
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	cfg := s.cfg.View()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	baseURL := s.ClientBaseURL(cfg)
	codexState := s.points.Check("codex", baseURL, s.clientSettings(cfg, "codex"))
	claudeState := s.points.Check("claude", baseURL, s.clientSettings(cfg, "claude"))
	grokState := s.points.Check("grok", baseURL, s.clientSettings(cfg, "grok"))
	st := StatusResponse{
		Version:            s.version,
		PID:                s.pid,
		Listen:             s.ListenString(cfg),
		ActiveRequests:     s.limiter.activeCount(),
		LoggingEnabled:     cfg.Logging.EnabledValue(),
		LoggingBodyEnabled: cfg.Logging.BodyValue(),
		AutostartEnabled:   cfg.Autostart.Enabled,
		Clients: map[string]ClientStatus{
			"codex":   {PointState: string(codexState.PointState)},
			"claude":  {PointState: string(claudeState.PointState)},
			"grok":    {PointState: string(grokState.PointState)},
			"generic": {PointState: "unknown"},
		},
		Routes: map[string]RouteStatus{
			"codex":   {Provider: cfg.Routes.Codex.Provider, Model: cfg.Routes.Codex.Model},
			"claude":  {Provider: cfg.Routes.Claude.Provider, Model: cfg.Routes.Claude.Model},
			"grok":    {Provider: cfg.Routes.Grok.Provider, Model: cfg.Routes.Grok.Model},
			"generic": {Provider: cfg.Routes.Generic.Provider, Model: cfg.Routes.Generic.Model},
		},
	}
	writeJSON(w, http.StatusOK, st)
}

// ListenString reports the actual bound listener address when serving,
// falling back to the configured loopback address otherwise.
func (s *Server) ListenString(cfg *config.Config) string {
	if addr := s.Addr(); addr != "" {
		return addr
	}
	return fmt.Sprintf("%s:%d", cfg.Listen.HostValue(), cfg.Listen.PortValue())
}

// ClientBaseURL is the loopback URL written into local client configuration.
// A wildcard listener is useful for other devices, but 0.0.0.0 is not a
// valid destination that local clients should persist.
func (s *Server) ClientBaseURL(cfg *config.Config) string {
	port := cfg.Listen.PortValue()
	if addr := s.Addr(); addr != "" {
		if _, rawPort, err := net.SplitHostPort(addr); err == nil {
			addr = rawPort
			if addr != "" {
				return "http://127.0.0.1:" + addr
			}
		}
	}
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// handleShutdown answers 202 Accepted, then triggers graceful shutdown. The
// actual http.Server.Shutdown runs outside this handler so the response is
// fully written before the listener closes (docs/v1-scheme.md §11.1).
func (s *Server) handleShutdown(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting_down"})
	s.RequestShutdown()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
