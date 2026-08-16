package server

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/logstore"
)

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_unavailable", "config not loaded", nil)
		return
	}
	q, ok := parseLogQuery(w, r, true)
	if !ok {
		return
	}
	page, err := s.warnings.List(cfg.Logging.Dir, q)
	if errors.Is(err, logstore.ErrInvalidCursor) {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "log_query_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleLogDetail(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_unavailable", "config not loaded", nil)
		return
	}
	requestID := r.PathValue("request_id")
	events, err := s.warnings.Detail(cfg.Logging.Dir, requestID)
	if errors.Is(err, os.ErrNotExist) {
		writeAPIError(w, http.StatusNotFound, "log_not_found", "request log not found", nil)
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "log_read_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request_id": requestID, "events": events})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_unavailable", "config not loaded", nil)
		return
	}
	q, ok := parseLogQuery(w, r, false)
	if !ok {
		return
	}
	report, err := s.warnings.Usage(cfg.Logging.Dir, q)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "usage_query_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

type loggingRequest struct {
	Enabled *bool `json:"enabled"`
	Body    *bool `json:"body"`
}

func (s *Server) handleLogging(w http.ResponseWriter, r *http.Request) {
	var req loggingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil && req.Body == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "enabled or body is required", map[string]string{"enabled": "required"})
		return
	}
	s.txMu.Lock()
	defer s.txMu.Unlock()
	cfg := s.cfg.Snapshot()
	if cfg == nil {
		writeAPIError(w, http.StatusInternalServerError, "config_unavailable", "config not loaded", nil)
		return
	}
	if req.Enabled != nil {
		cfg.Logging.Enabled = config.BoolPtr(*req.Enabled)
	}
	if req.Body != nil {
		cfg.Logging.Body = config.BoolPtr(*req.Body)
	}
	if err := s.cfg.Write(cfg); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "config_write_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": cfg.Logging.EnabledValue(), "body": cfg.Logging.BodyValue()})
}

func parseLogQuery(w http.ResponseWriter, r *http.Request, withPage bool) (logstore.Query, bool) {
	values := r.URL.Query()
	q := logstore.Query{Client: values.Get("client"), Provider: values.Get("provider"), Status: values.Get("status"), Cursor: values.Get("cursor")}
	for raw, dst := range map[string]**time.Time{"from": &q.From, "to": &q.To} {
		if value := values.Get(raw); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_query", raw+" must be an RFC 3339 timestamp", nil)
				return q, false
			}
			*dst = &parsed
		}
	}
	if q.From != nil && q.To != nil && q.From.After(*q.To) {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", "from must not be after to", nil)
		return q, false
	}
	if q.Status != "" && q.Status != "success" && q.Status != "failed" && q.Status != "cancelled" && q.Status != "interrupted" {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", "status must be success, failed, cancelled, or interrupted", nil)
		return q, false
	}
	if withPage {
		limit, err := logstore.ParseLimit(values.Get("limit"))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", err.Error(), nil)
			return q, false
		}
		q.Limit = limit
	} else if values.Get("limit") != "" || values.Get("cursor") != "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_query", "usage does not accept limit or cursor", nil)
		return q, false
	}
	for key := range values {
		if !strings.Contains(" from to client provider status limit cursor ", " "+key+" ") {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "unknown query parameter: "+key, nil)
			return q, false
		}
	}
	return q, true
}
