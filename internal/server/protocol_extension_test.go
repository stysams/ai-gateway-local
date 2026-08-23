package server

import (
	"encoding/json"
	"testing"

	chatin "ai-gateway/internal/inbound/chat"
	messagesin "ai-gateway/internal/inbound/messages"
	responsesin "ai-gateway/internal/inbound/responses"
	"ai-gateway/internal/outbound/anthropic"
	"ai-gateway/internal/outbound/openaichat"
	"ai-gateway/internal/outbound/openairesponses"
)

func TestStructuredOutputAndStrictToolsConvertAcrossProtocols(t *testing.T) {
	t.Run("chat to messages", func(t *testing.T) {
		req, err := chatin.ParseRequest([]byte(`{
			"model":"source","messages":[{"role":"user","content":"extract"}],
			"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}}},
			"tools":[{"type":"function","function":{"name":"lookup","strict":true,"parameters":{"type":"object","properties":{},"additionalProperties":false}}}]
		}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Model = "target"
		body, err := anthropic.GenerateRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONPath(t, body, []string{"output_config", "format", "type"}, "json_schema")
		assertJSONPath(t, body, []string{"tools", "0", "strict"}, true)
	})

	t.Run("messages to responses", func(t *testing.T) {
		req, err := messagesin.ParseRequest([]byte(`{
			"model":"source","max_tokens":128,"messages":[{"role":"user","content":"extract"}],
			"output_config":{"format":{"type":"json_schema","schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}}},
			"tools":[{"name":"lookup","strict":true,"input_schema":{"type":"object","properties":{},"additionalProperties":false}}]
		}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Model = "target"
		body, err := openairesponses.GenerateRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONPath(t, body, []string{"text", "format", "type"}, "json_schema")
		assertJSONPath(t, body, []string{"text", "format", "name"}, "structured_output")
		assertJSONPath(t, body, []string{"tools", "0", "strict"}, true)
	})

	t.Run("responses to chat", func(t *testing.T) {
		req, err := responsesin.ParseRequest([]byte(`{
			"model":"source","input":"extract",
			"text":{"format":{"type":"json_schema","name":"answer","strict":true,"schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}}},
			"tools":[{"type":"function","name":"lookup","strict":true,"parameters":{"type":"object","properties":{},"additionalProperties":false}}]
		}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Model = "target"
		body, err := openaichat.GenerateRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONPath(t, body, []string{"response_format", "json_schema", "name"}, "answer")
		assertJSONPath(t, body, []string{"tools", "0", "function", "strict"}, true)
	})
}

func assertJSONPath(t *testing.T, body []byte, path []string, want any) {
	t.Helper()
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	current := value
	for _, part := range path {
		switch typed := current.(type) {
		case map[string]any:
			current = typed[part]
		case []any:
			if part != "0" || len(typed) == 0 {
				t.Fatalf("path %v is unavailable in %s", path, body)
			}
			current = typed[0]
		default:
			t.Fatalf("path %v is unavailable in %s", path, body)
		}
	}
	if current != want {
		t.Fatalf("path %v = %#v, want %#v; body=%s", path, current, want, body)
	}
}
