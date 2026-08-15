package messages

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"ai-gateway/internal/ir"
)

func TestParseRequestBlocks(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"system": "Be brief.",
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hi"}]},
			{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "f", "input": {"a": 1}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "ok"}]}
		],
		"tools": [{"name": "f", "description": "d", "input_schema": {"type": "object"}}]
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "m" || len(req.System) != 1 || req.System[0].Text != "Be brief." {
		t.Errorf("req = %+v", req)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %+v", req.Messages)
	}
	assistant := req.Messages[1]
	if assistant.Content[0].ToolCall == nil || assistant.Content[0].ToolCall.ID != "toolu_1" {
		t.Errorf("tool_use = %+v", assistant.Content)
	}
	if string(assistant.Content[0].ToolCall.Arguments) != `{"a": 1}` {
		t.Errorf("tool input = %s", assistant.Content[0].ToolCall.Arguments)
	}
	toolResult := req.Messages[2].Content[0].ToolResult
	if toolResult == nil || toolResult.ID != "toolu_1" || toolResult.Content != "ok" {
		t.Errorf("tool_result = %+v", req.Messages[2].Content)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "f" {
		t.Errorf("tools = %+v", req.Tools)
	}
}

func TestParseRequestMergesClaudeCodeSystemRoleMessages(t *testing.T) {
	body := []byte(`{
		"model": "gateway-default",
		"stream": true,
		"system": [{"type":"text","text":"Base instructions","cache_control":{"type":"ephemeral"}}],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"hello"}]},
			{"role":"system","content":[{"type":"text","text":"Deferred ToolSearch guidance","cache_control":{"type":"ephemeral"}}]}
		]
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Stream {
		t.Fatal("stream = false, want true")
	}
	if len(req.System) != 2 || req.System[0].Text != "Base instructions" || req.System[1].Text != "Deferred ToolSearch guidance" {
		t.Fatalf("system = %+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != ir.RoleUser || req.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("messages = %+v", req.Messages)
	}
}

func TestParseRequestRejectsNonTextSystemRoleContent(t *testing.T) {
	_, err := ParseRequest([]byte(`{"model":"m","messages":[{"role":"system","content":[{"type":"tool_use","id":"x","name":"f","input":{}}]}]}`))
	if !errors.Is(err, ir.ErrUnsupportedContent) {
		t.Fatalf("err = %v, want ErrUnsupportedContent", err)
	}
}

func TestParseRequestImageAndThinking(t *testing.T) {
	body := []byte(`{"model":"m","thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	image := req.Messages[0].Content[0].Image
	if image == nil || image.Base64 != "AAAA" || image.MediaType != "image/png" {
		t.Fatalf("image = %+v", image)
	}
	if req.Reasoning.Type != "enabled" || req.Reasoning.BudgetTokens != 2048 || req.Reasoning.Source != ir.ProtocolMessages {
		t.Fatalf("reasoning = %+v", req.Reasoning)
	}
	if features := InspectFeatures(body); !features.Image || !features.Reasoning {
		t.Fatalf("features = %+v", features)
	}
}

func TestToolChoiceNormalization(t *testing.T) {
	req, err := ParseRequest([]byte(`{"model":"m","messages":[],"tool_choice":{"type":"tool","name":"f"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(req.ToolChoice) != `{"name":"f","type":"function"}` {
		t.Errorf("tool_choice = %s", req.ToolChoice)
	}
}

func TestFieldsRewritePreservesUnknown(t *testing.T) {
	req, err := Parse([]byte(`{"model":"m","max_tokens":10,"messages":[],"x-extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := req.Rewrite("new-model", true)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "new-model" || doc["stream"] != true || doc["x-extra"] == nil {
		t.Errorf("doc = %v", doc)
	}
}

func eventSource(events []ir.Event) func() (ir.Event, error) {
	i := 0
	return func() (ir.Event, error) {
		if i >= len(events) {
			return ir.Event{}, io.EOF
		}
		ev := events[i]
		i++
		return ev, nil
	}
}

func TestEncodeStreamText(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventTextDelta, Text: "Hel"},
		{Type: ir.EventTextDelta, Text: "lo"},
		{Type: ir.EventCompleted, StopReason: "end_turn"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		`"type":"text"`,
		"event: content_block_delta",
		`"type":"text_delta"`,
		`"text":"Hel"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		"event: message_stop",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, `"text":"Hel"`) > strings.Index(out, `"text":"lo"`) {
		t.Error("delta order wrong")
	}
}

func TestEncodeStreamToolCall(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventToolCallStarted, ToolCallID: "toolu_1", ToolName: "f"},
		{Type: ir.EventToolCallArgumentsDlt, ToolCallID: "toolu_1", ArgumentsDelta: `{"a":`},
		{Type: ir.EventToolCallArgumentsDlt, ToolCallID: "toolu_1", ArgumentsDelta: `1}`},
		{Type: ir.EventToolCallCompleted, ToolCallID: "toolu_1", ToolName: "f"},
		{Type: ir.EventCompleted, StopReason: "tool_use"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`"type":"tool_use"`,
		`"id":"toolu_1"`,
		`"type":"input_json_delta"`,
		`"partial_json":"{\"a\":"`,
		`"stop_reason":"tool_use"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
}

func TestEncodeStreamErrorNoStop(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventError, Error: &ir.ErrorInfo{Message: "boom"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "event: message_stop") {
		t.Errorf("error stream must not end with message_stop: %s", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("error stream should carry the error: %s", out)
	}
}

func TestEncodeNonStream(t *testing.T) {
	var buf bytes.Buffer
	resp := &ir.Response{Text: "hello", ToolCalls: []ir.ToolCall{{ID: "toolu_1", Name: "f", Arguments: json.RawMessage(`{}`)}}}
	if err := EncodeNonStream(&buf, "m", resp); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["type"] != "message" || doc["role"] != "assistant" {
		t.Errorf("doc = %v", doc)
	}
	content := doc["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content = %v", content)
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Errorf("content[0] = %v", content[0])
	}
	tu := content[1].(map[string]any)
	if tu["type"] != "tool_use" || tu["id"] != "toolu_1" {
		t.Errorf("content[1] = %v", tu)
	}
}
