package logstore

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedactValueCoversNestedJSONHeadersAndStreamFrames(t *testing.T) {
	value := map[string]any{
		"headers": http.Header{"X-Api-Key": {"secret-key"}, "X-Trace": {"trace"}},
		"query":   url.Values{"debug": {"api_key=inline-secret"}, "safe": {"keep"}},
		"values":  []string{"access_token=array-secret", "visible"},
		"body":    json.RawMessage(`{"auth":{"access_token":"token-value"},"messages":[{"content":"keep prompt"}]}`),
		"event":   "data: {\"client_secret\":\"hidden\",\"text\":\"visible\"}\n\n",
	}
	encoded, err := json.Marshal(RedactValue(value))
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, forbidden := range []string{"secret-key", "token-value", "hidden", "inline-secret", "array-secret"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("redaction leaked %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{RedactionMarker, "keep prompt", "trace", "visible", "keep"} {
		if !strings.Contains(text, required) {
			t.Fatalf("redaction removed %q: %s", required, text)
		}
	}
}

func TestSessionRedactsTopLevelSensitiveField(t *testing.T) {
	w := New(t.TempDir())
	session, err := w.OpenWithRedaction("logs", "req_top_level", time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Append("request", map[string]any{"api_key": "sk-top-level", "model": "qwen3"}); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := w.Export("logs", "req_top_level", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-top-level") || !strings.Contains(string(data), `"api_key":"[REDACTED]"`) {
		t.Fatalf("top-level sensitive field was not redacted: %s", data)
	}
}

func TestExportRedactsLegacyLogAndManualCleanupPreservesActiveSession(t *testing.T) {
	w := New(t.TempDir())
	legacy, err := w.Open("logs", "legacy", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Append("request", map[string]any{"body": json.RawMessage(`{"api_key":"legacy-secret","content":"prompt"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	active, err := w.Open("logs", "active", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if err := active.Append("request", map[string]any{"body": "active"}); err != nil {
		t.Fatal(err)
	}

	exported, err := w.Export("logs", "legacy", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(exported), "legacy-secret") || !strings.Contains(string(exported), RedactionMarker) {
		t.Fatalf("export was not redacted: %s", exported)
	}
	if err := w.Delete("logs", "active"); !errors.Is(err, ErrLogActive) {
		t.Fatalf("Delete(active) = %v, want ErrLogActive", err)
	}
	removed, err := w.Clear("logs")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("Clear removed %d logs, want 1", removed)
	}
	if _, err := w.Detail("logs", "legacy"); !os.IsNotExist(err) {
		t.Fatalf("legacy log still exists: %v", err)
	}
	if _, err := w.Detail("logs", "active"); err != nil {
		t.Fatalf("active log was removed: %v", err)
	}
}
