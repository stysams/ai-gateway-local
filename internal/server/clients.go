package server

import (
	"errors"
	"net/http"

	"ai-gateway/internal/config"
	"ai-gateway/internal/point"
	"ai-gateway/internal/route"
)

func (s *Server) pointContext(w http.ResponseWriter, r *http.Request) (point.Client, string, string, bool) {
	client, err := point.ParseClient(r.PathValue("client"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "client_not_found", err.Error(), nil)
		return "", "", "", false
	}
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return "", "", "", false
	}
	return client, s.ClientBaseURL(cfg), displayModelForClient(cfg, client), true
}

func displayModelForClient(_ *config.Config, _ point.Client) string {
	// Client CLIs must stay provider-neutral. The gateway exposes the complete
	// provider/model catalog through /v1/models and resolves gateway-default via
	// the client route at request time.
	return route.ReservedModel
}

type displayModelSync struct {
	client   point.Client
	oldModel string
	newModel string
}

func displayModelChanges(current, next *config.Config) []displayModelSync {
	clients := []point.Client{point.ClientCodex, point.ClientClaude, point.ClientGrok}
	changes := make([]displayModelSync, 0, len(clients))
	for _, client := range clients {
		oldModel := displayModelForClient(current, client)
		newModel := displayModelForClient(next, client)
		if oldModel != newModel {
			changes = append(changes, displayModelSync{client: client, oldModel: oldModel, newModel: newModel})
		}
	}
	return changes
}

func (s *Server) applyDisplayModelChanges(baseURL string, changes []displayModelSync) ([]displayModelSync, error) {
	applied := make([]displayModelSync, 0, len(changes))
	for _, change := range changes {
		changed, err := s.points.SyncDisplayModel(change.client, baseURL, change.oldModel, change.newModel)
		if err != nil {
			rollbackErr := s.rollbackDisplayModelChanges(baseURL, applied)
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

func (s *Server) rollbackDisplayModelChanges(baseURL string, applied []displayModelSync) error {
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		change := applied[index]
		_, err := s.points.SyncDisplayModel(change.client, baseURL, change.newModel, change.oldModel)
		rollbackErr = errors.Join(rollbackErr, err)
	}
	return rollbackErr
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, displayModel, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.points.Check(client, baseURL, displayModel))
}

func (s *Server) handlePointClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, displayModel, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	result, err := s.points.Point(client, baseURL, displayModel)
	if err != nil {
		s.writePointError(w, "point", result.BackupDir, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRestoreClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, displayModel, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	result, err := s.points.Restore(client, baseURL, displayModel)
	if err != nil {
		s.writePointError(w, "restore", result.BackupDir, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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
