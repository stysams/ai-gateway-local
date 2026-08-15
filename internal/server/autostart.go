package server

import (
	"errors"
	"net/http"

	"ai-gateway/internal/autostart"
)

type AutostartRequest struct {
	Enabled bool `json:"enabled"`
}

type AutostartResponse struct {
	Enabled    bool   `json:"enabled"`
	Valid      bool   `json:"valid"`
	Executable string `json:"executable,omitempty"`
}

func (s *Server) handleAutostart(w http.ResponseWriter, r *http.Request) {
	var request AutostartRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	s.txMu.Lock()
	defer s.txMu.Unlock()
	registration, err := autostart.Apply(s.autostart, s.cfg, request.Enabled)
	if err != nil {
		var applyErr *autostart.ApplyError
		if errors.As(err, &applyErr) && applyErr.Partial() {
			writeAPIError(w, http.StatusInternalServerError, "partial_failure", err.Error(), map[string]string{"repair": "run ai-gateway doctor, then retry autostart on or off"})
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "autostart_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, AutostartResponse{Enabled: registration.Enabled, Valid: registration.Valid, Executable: registration.Executable})
}
