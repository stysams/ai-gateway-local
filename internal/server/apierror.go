package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// APIError is the unified management API error payload
// (docs/v1-scheme.md §9.5).
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Details carries locatable field info, e.g. {"field":
	// "providers.openrouter.base_url"}.
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

// errorBody wraps the error object like the contract example:
// {"error": {"code": ..., "message": ...}}.
type errorBody struct {
	Error APIError `json:"error"`
}

// writeAPIError writes the unified management API error shape. Management
// request ids are generated for response correlation;正文日志仅覆盖数据面请求。
func writeAPIError(w http.ResponseWriter, status int, code, message string, details map[string]string) {
	var reqID [8]byte
	if _, err := rand.Read(reqID[:]); err == nil {
		// Best-effort; a missing id is better than a broken response.
	}
	writeJSON(w, status, errorBody{
		Error: APIError{
			Code:      code,
			Message:   message,
			Details:   details,
			RequestID: "req_" + hex.EncodeToString(reqID[:]),
		},
	})
}
