// Package logstore owns append-only per-request JSONL files and their query
// and usage aggregation APIs.
package logstore

import (
	"errors"
	"sync"
	"time"
)

// Writer owns request logs below one data root.
type Writer struct {
	root         string
	mu           sync.Mutex
	sessions     map[*Session]struct{}
	cacheMu      sync.RWMutex
	summaryCache map[string]summaryCacheEntry
}

// New returns a log writer rooted at the gateway data directory.
func New(dataRoot string) *Writer {
	return &Writer{root: dataRoot, sessions: make(map[*Session]struct{}), summaryCache: make(map[string]summaryCacheEntry)}
}

// Close closes all request sessions still in progress. It is used during
// graceful shutdown so interrupted logs remain parseable and removable.
func (w *Writer) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	sessions := make([]*Session, 0, len(w.sessions))
	for session := range w.sessions {
		sessions = append(sessions, session)
	}
	w.mu.Unlock()
	var first error
	for _, session := range sessions {
		if err := session.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ActiveSessions reports requests whose terminal result has not closed yet.
func (w *Writer) ActiveSessions() int {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sessions)
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
	defer session.Close()
	return session.Append("warning", map[string]any{
		"code": code, "message": message, "details": details,
	})
}
