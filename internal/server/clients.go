package server

import (
	"errors"
	"net/http"

	"ai-gateway/internal/point"
)

func (s *Server) pointContext(w http.ResponseWriter, r *http.Request) (point.Client, string, bool) {
	client, err := point.ParseClient(r.PathValue("client"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "client_not_found", err.Error(), nil)
		return "", "", false
	}
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_not_loaded", "config not loaded", nil)
		return "", "", false
	}
	return client, "http://" + s.ListenString(cfg), true
}

func (s *Server) handleGetClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.points.Check(client, baseURL))
}

func (s *Server) handlePointClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	result, err := s.points.Point(client, baseURL)
	if err != nil {
		s.writePointError(w, "point", result.BackupDir, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRestoreClient(w http.ResponseWriter, r *http.Request) {
	client, baseURL, ok := s.pointContext(w, r)
	if !ok {
		return
	}
	result, err := s.points.Restore(client, baseURL)
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
