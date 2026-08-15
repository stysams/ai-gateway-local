package logstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendWarning(t *testing.T) {
	root := t.TempDir()
	w := New(root)
	if err := w.AppendWarning("logs", "req_test", "reasoning_dropped", "removed", map[string]any{"provider": "local"}); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, "logs", "*", "req_test.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, err = %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var event Warning
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "warning" || event.Code != "reasoning_dropped" || event.RequestID != "req_test" {
		t.Fatalf("event = %+v", event)
	}
}

func TestAppendWarningRejectsEscapingDirectory(t *testing.T) {
	if err := New(t.TempDir()).AppendWarning("../outside", "req_test", "x", "x", nil); err == nil {
		t.Fatal("escaping log directory accepted")
	}
}
