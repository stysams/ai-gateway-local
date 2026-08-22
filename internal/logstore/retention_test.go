package logstore

import (
	"testing"
	"time"
)

func writeCompletedLog(t *testing.T, w *Writer, requestID string, at time.Time) {
	t.Helper()
	session, err := w.Open("logs", requestID, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Append("request", map[string]any{"client": "generic", "protocol": "chat"}); err != nil {
		t.Fatal(err)
	}
	if err := session.Append("result", map[string]any{"status": "success", "status_code": 200}); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRetainRemovesOnlyCompletedLogs(t *testing.T) {
	w := New(t.TempDir())
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.Local)
	writeCompletedLog(t, w, "old", now.AddDate(0, 0, -4))
	writeCompletedLog(t, w, "new", now.AddDate(0, 0, -1))
	active, err := w.Open("logs", "active", now.AddDate(0, 0, -5))
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if err := active.Append("request", map[string]any{"client": "generic", "protocol": "chat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Retain("logs", 2, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Detail("logs", "old"); err == nil {
		t.Fatal("old completed log was not removed")
	}
	if _, err := w.Detail("logs", "new"); err != nil {
		t.Fatalf("new log removed: %v", err)
	}
	if _, err := w.Detail("logs", "active"); err != nil {
		t.Fatalf("active log removed: %v", err)
	}
}
