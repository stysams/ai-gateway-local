package server

import (
	"errors"
	"net/http"
	"strings"

	"ai-gateway/internal/config"
	"ai-gateway/internal/point"
	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/route"
)

func (s *Server) pointContext(w http.ResponseWriter, r *http.Request) (point.Client, string, point.Settings, bool) {
	client, err := point.ParseClient(r.PathValue("client"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "client_not_found", err.Error(), nil)
		return "", "", point.Settings{}, false
	}
	cfg := s.cfg.View()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return "", "", point.Settings{}, false
	}
	return client, s.ClientBaseURL(cfg), s.clientSettings(cfg, client), true
}

// clientSettings is what a pointed client configuration must express.
//
// The preferred startup model stays the provider-neutral reserved name so that
// switching a route never rewrites a pointed client's file (§1.2, §7.3): the
// gateway resolves gateway-default against the current route per request. The
// catalog carries every enabled `<provider-id>/<model-id>` so a user can pick
// any of them inside the agent. Grok writes it into config.toml; Codex writes
// it into ai-gateway-catalog.json; Claude writes cache/gateway-models.json
// and still enables gateway discovery.
func (s *Server) clientSettings(cfg *config.Config, client point.Client) point.Settings {
	items := s.modelCatalog(cfg)
	catalog := make([]clientcatalog.Entry, 0, len(items))
	for _, item := range items {
		if item.ID == route.ReservedModel {
			continue
		}
		catalog = append(catalog, clientcatalog.Entry{ID: item.ID, DisplayName: item.DisplayName})
	}
	settings := point.Settings{PreferredModel: route.ReservedModel, Catalog: catalog}
	if client == point.ClientCodex {
		settings.RemoteCompaction = cfg.Clients.Codex.RemoteCompactionValue()
	} else if client == point.ClientClaude {
		settings.SubagentModel = configuredHelperModel(cfg.Clients.Claude.SubagentModel, cfg)
		settings.TitleModel = configuredHelperModel(cfg.Clients.Claude.TitleModel, cfg)
	}
	return settings
}

type clientSettingsSync struct {
	client point.Client
	before point.Settings
	after  point.Settings
}

func (s *Server) clientSettingsChanges(current, next *config.Config) []clientSettingsSync {
	clients := []point.Client{point.ClientCodex, point.ClientClaude, point.ClientGrok}
	changes := make([]clientSettingsSync, 0, len(clients))
	for _, client := range clients {
		before := s.clientSettings(current, client)
		after := s.clientSettings(next, client)
		if !before.Equal(after) {
			changes = append(changes, clientSettingsSync{client: client, before: before, after: after})
		}
	}
	return changes
}

func (s *Server) applyClientSettingsChanges(baseURL string, changes []clientSettingsSync) ([]clientSettingsSync, error) {
	applied := make([]clientSettingsSync, 0, len(changes))
	for _, change := range changes {
		changed, err := s.points.SyncSettings(change.client, baseURL, change.after)
		if err != nil {
			rollbackErr := s.rollbackClientSettingsChanges(baseURL, applied)
			if rollbackErr != nil {
				return nil, errors.Join(err, rollbackErr)
			}
			return nil, err
		}
		if changed {
			applied = append(applied, change)
		}
	}
	return applied, nil
}

func (s *Server) syncClientsThenWrite(current, next *config.Config) error {
	if current == nil || next == nil {
		return errors.New("config not loaded")
	}
	baseURL := s.ClientBaseURL(current)
	applied, err := s.applyClientSettingsChanges(baseURL, s.clientSettingsChanges(current, next))
	if err != nil {
		return err
	}
	if err := s.cfg.Write(next); err != nil {
		if rollbackErr := s.rollbackClientSettingsChanges(baseURL, applied); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	return nil
}

func (s *Server) rollbackClientSettingsChanges(baseURL string, applied []clientSettingsSync) error {
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		change := applied[index]
		_, err := s.points.SyncSettings(change.client, baseURL, change.before)
		rollbackErr = errors.Join(rollbackErr, err)
	}
	return rollbackErr
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, settings, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.withClientOptions(s.points.Check(client, baseURL, settings)))
}

func (s *Server) handlePointClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, settings, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	result, err := s.points.Point(client, baseURL, settings)
	if err != nil {
		s.writePointError(w, "point", result.BackupDir, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withClientResult(result))
}

func (s *Server) handleRestoreClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, settings, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	result, err := s.points.Restore(client, baseURL, settings)
	if err != nil {
		s.writePointError(w, "restore", result.BackupDir, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withClientResult(result))
}

type remoteCompactionRequest struct {
	Enabled *bool `json:"enabled"`
}

type helperModelsRequest struct {
	SubagentModel *string `json:"subagent_model"`
	TitleModel    *string `json:"title_model"`
}

func (s *Server) handlePutClientHelperModels(w http.ResponseWriter, r *http.Request) {
	client, err := point.ParseClient(r.PathValue("client"))
	if err != nil || (client != point.ClientCodex && client != point.ClientClaude) {
		writeAPIError(w, http.StatusNotFound, "client_not_found", "helper model settings are supported only for Codex and Claude Code", nil)
		return
	}
	var req helperModelsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.SubagentModel == nil || req.TitleModel == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "subagent_model and title_model are required", map[string]string{"subagent_model": "required", "title_model": "required"})
		return
	}

	s.txMu.Lock()
	defer s.txMu.Unlock()
	current := s.cfg.View()
	if current == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	subagent := normalizeHelperModel(*req.SubagentModel)
	title := normalizeHelperModel(*req.TitleModel)
	invalid := map[string]string{}
	if !s.helperModelSelectable(current, subagent) {
		invalid["subagent_model"] = "must be an enabled provider/model ID or empty"
	}
	if !s.helperModelSelectable(current, title) {
		invalid["title_model"] = "must be an enabled provider/model ID or empty"
	}
	if len(invalid) > 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "helper model selection is invalid", invalid)
		return
	}

	next := s.cfg.Snapshot()
	if client == point.ClientCodex {
		next.Clients.Codex.SubagentModel = subagent
		next.Clients.Codex.TitleModel = title
	} else {
		next.Clients.Claude.SubagentModel = subagent
		next.Clients.Claude.TitleModel = title
	}
	if err := s.syncClientsThenWrite(current, next); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "client_sync_failed", err.Error(), nil)
		return
	}
	_, baseURL, settings, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.withClientOptions(s.points.Check(client, baseURL, settings)))
}

func normalizeHelperModel(value string) string {
	value = strings.TrimSpace(value)
	if value == route.ReservedModel {
		return ""
	}
	return value
}

func (s *Server) helperModelSelectable(cfg *config.Config, model string) bool {
	if model == "" {
		return true
	}
	for _, item := range s.modelCatalog(cfg) {
		if item.ID == model && item.ID != route.ReservedModel {
			return true
		}
	}
	return false
}

func (s *Server) handlePutClientRemoteCompaction(w http.ResponseWriter, r *http.Request) {
	client, err := point.ParseClient(r.PathValue("client"))
	if err != nil || client != point.ClientCodex {
		writeAPIError(w, http.StatusNotFound, "client_not_found", "remote compaction is a Codex-only setting", nil)
		return
	}
	var req remoteCompactionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "enabled is required", map[string]string{"enabled": "required"})
		return
	}
	s.txMu.Lock()
	defer s.txMu.Unlock()
	current := s.cfg.View()
	if current == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return
	}
	next := s.cfg.Snapshot()
	next.Clients.Codex.RemoteCompaction = config.BoolPtr(*req.Enabled)
	if err := s.syncClientsThenWrite(current, next); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "client_sync_failed", err.Error(), nil)
		return
	}
	_, baseURL, settings, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.withClientOptions(s.points.Check(client, baseURL, settings)))
}

func (s *Server) withClientOptions(status point.Status) point.Status {
	if cfg := s.cfg.View(); cfg != nil {
		switch status.Client {
		case point.ClientCodex:
			enabled := cfg.Clients.Codex.RemoteCompactionValue()
			status.RemoteCompaction = &enabled
			status.SubagentModel = strings.TrimSpace(cfg.Clients.Codex.SubagentModel)
			status.TitleModel = strings.TrimSpace(cfg.Clients.Codex.TitleModel)
		case point.ClientClaude:
			status.SubagentModel = strings.TrimSpace(cfg.Clients.Claude.SubagentModel)
			status.TitleModel = strings.TrimSpace(cfg.Clients.Claude.TitleModel)
		}
	}
	return status
}

func (s *Server) withClientResult(result point.Result) point.Result {
	result.Status = s.withClientOptions(result.Status)
	return result
}

func (s *Server) writePointError(w http.ResponseWriter, operation, backupDir string, err error) {
	details := map[string]string{}
	if backupDir != "" {
		details["backup_dir"] = backupDir
	}
	var partial *point.PartialFailureError
	switch {
	case errors.Is(err, point.ErrClientNotInstalled):
		writeAPIError(w, http.StatusConflict, "client_not_installed", err.Error(), details)
	case errors.Is(err, point.ErrNoRestore):
		writeAPIError(w, http.StatusConflict, "no_restore_available", err.Error(), details)
	case errors.As(err, &partial):
		writeAPIError(w, http.StatusInternalServerError, "partial_failure", err.Error(), details)
	default:
		writeAPIError(w, http.StatusInternalServerError, operation+"_failed", err.Error(), details)
	}
}
