package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// requestFixture is a realistic chat/completions body containing unknown
// fields (temperature, user, x-custom) that must survive rewriting.
const requestFixture = `{
  "model": "some-model",
  "messages": [{"role": "user", "content": "hello"}],
  "stream": true,
  "temperature": 0.7,
  "user": "req-user-1",
  "x-custom": {"nested": [1, 2, {"keep": "me"}]}
}`

func TestParse(t *testing.T) {
	req, err := Parse([]byte(requestFixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if req.Model != "some-model" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.Stream == nil || !*req.Stream {
		t.Errorf("Stream = %v, want true", req.Stream)
	}
	if !req.StreamValue() {
		t.Error("StreamValue() = false, want true")
	}
	// Unknown fields must be captured raw.
	if _, ok := req.Fields["temperature"]; !ok {
		t.Error("temperature lost")
	}
	if _, ok := req.Fields["x-custom"]; !ok {
		t.Error("x-custom lost")
	}
}

func TestParseStreamDefaults(t *testing.T) {
	req, err := Parse([]byte(`{"model": "m", "messages": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Stream != nil {
		t.Errorf("Stream = %v, want nil", req.Stream)
	}
	if req.StreamValue() {
		t.Error("StreamValue() = true without stream field")
	}
}

func TestParseErrors(t *testing.T) {
	for _, body := range []string{
		``,
		`not json`,
		`[1, 2, 3]`,
		`"just a string"`,
		`{"model": 42}`,
		`{"stream": "yes"}`,
		`{"model": "m"} trailing`,
	} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", body)
		}
	}
}

func TestParseStreamTypeError(t *testing.T) {
	_, err := Parse([]byte(`{"stream": "true"}`))
	if err == nil || !strings.Contains(err.Error(), "stream") {
		t.Errorf("want stream type error, got %v", err)
	}
}

func TestRewritePreservesUnknownFields(t *testing.T) {
	req, err := Parse([]byte(requestFixture))
	if err != nil {
		t.Fatal(err)
	}
	out, err := req.Rewrite("openrouter/anthropic/claude-sonnet-4", false)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("rewritten body not valid JSON: %v", err)
	}
	// model and stream rewritten.
	var model string
	if err := json.Unmarshal(decoded["model"], &model); err != nil || model != "openrouter/anthropic/claude-sonnet-4" {
		t.Errorf("model = %q, %v", model, err)
	}
	var stream bool
	if err := json.Unmarshal(decoded["stream"], &stream); err != nil || stream {
		t.Errorf("stream = %v, %v; want false", stream, err)
	}
	// Unknown fields preserved with identical values.
	var temp float64
	if err := json.Unmarshal(decoded["temperature"], &temp); err != nil || temp != 0.7 {
		t.Errorf("temperature = %v, %v; want 0.7", temp, err)
	}
	if string(decoded["user"]) != `"req-user-1"` {
		t.Errorf("user = %s", decoded["user"])
	}
	var custom map[string]any
	if err := json.Unmarshal(decoded["x-custom"], &custom); err != nil {
		t.Fatalf("x-custom lost: %v", err)
	}
	nested, ok := custom["nested"].([]any)
	if !ok || len(nested) != 3 {
		t.Fatalf("x-custom.nested = %v", custom["nested"])
	}
	keep, ok := nested[2].(map[string]any)
	if !ok || keep["keep"] != "me" {
		t.Errorf("x-custom.nested[2] = %v", nested[2])
	}
	messages, ok := decoded["messages"]
	if !ok || !strings.Contains(string(messages), "hello") {
		t.Errorf("messages = %s", messages)
	}
}

func TestRewriteIdempotentStreamTrue(t *testing.T) {
	req, err := Parse([]byte(requestFixture))
	if err != nil {
		t.Fatal(err)
	}
	out, err := req.Rewrite("m2", true)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	var stream bool
	if err := json.Unmarshal(decoded["stream"], &stream); err != nil || !stream {
		t.Errorf("stream = %v, want true", stream)
	}
}
