// Package logstore owns append-only per-request JSONL files and their query
// and usage aggregation APIs.
package logstore

import (
	"errors"
	"time"
)

// Writer owns request logs below one data root.
type Writer struct {
	root string
}

// New returns a log writer rooted at the gateway data directory.
func New(dataRoot string) *Writer {
	return &Writer{root: dataRoot}
}

// Warning is the task package E warning event shape.
type Warning struct {
	Timestamp time.Time      `json:"timestamp"`
	RequestID string         `json:"request_id"`
	Type      string         `json:"type"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
}

// AppendWarning appends a standalone warning. New data-plane code uses a
// Session so warnings share the request's existing JSONL file; this method is
// retained for callers that do not yet own a request session.
func (w *Writer) AppendWarning(logDir, requestID, code, message string, details map[string]any) error {
	if w == nil {
		return errors.New("warning writer is nil")
	}
	session, err := w.Open(logDir, requestID, time.Now())
	if err != nil {
		return err
	}
	return session.Append("warning", map[string]any{
		"code": code, "message": message, "details": details,
	})
}
