package chat

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"ai-gateway/internal/ir"
)

func TestParseRequestTextAndSystem(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "hi there"}
		],
		"temperature": 0.5
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "m" || req.Stream {
		t.Errorf("req = %+v", req)
	}
	if len(req.System) != 1 || req.System[0].Text != "You are helpful." {
		t.Errorf("system = %+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != ir.RoleUser || req.Messages[0].Content[0].Text != "hi there" {
		t.Errorf("messages = %+v", req.Messages)
	}
	// 未建模字段保留在 Extensions。
	if _, ok := req.Extensions["temperature"]; !ok {
		t.Error("temperature lost")
	}
}

func TestParseRequestToolsAndToolResults(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"tools": [{"type": "function", "function": {"name": "get_weather", "description": "w", "parameters": {"type": "object"}}}],
		"tool_choice": {"type": "function", "function": {"name": "get_weather"}},
		"messages": [
			{"role": "assistant", "content": null, "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Berlin\"}"}}]},
			{"role": "tool", "tool_call_id": "call_1", "content": "sunny"}
		]
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	tc := string(req.ToolChoice)
	if !strings.Contains(tc, "get_weather") || !strings.Contains(tc, "function") {
		t.Errorf("tool_choice = %s", tc)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %+v", req.Messages)
	}
	assistant := req.Messages[0]
	if assistant.Role != ir.RoleAssistant || len(assistant.Content) != 1 || assistant.Content[0].ToolCall == nil {
		t.Fatalf("assistant = %+v", assistant)
	}
	if assistant.Content[0].ToolCall.ID != "call_1" || string(assistant.Content[0].ToolCall.Arguments) != `{"city":"Berlin"}` {
		t.Errorf("tool call = %+v", assistant.Content[0].ToolCall)
	}
	toolMsg := req.Messages[1]
	if toolMsg.Role != ir.RoleTool || toolMsg.Content[0].ToolResult == nil || toolMsg.Content[0].ToolResult.ID != "call_1" || toolMsg.Content[0].ToolResult.Content != "sunny" {
		t.Errorf("tool result = %+v", toolMsg)
	}
}

func TestParseRequestImageAndReasoning(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"reasoning_effort": "high",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "see"}, {"type": "image_url", "image_url": {"url": "https://x/i.png"}}]}]
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	image := req.Messages[0].Content[1].Image
	if image == nil || image.URL != "https://x/i.png" {
		t.Fatalf("image = %+v", image)
	}
	if req.Reasoning.Effort != "high" || req.Reasoning.Source != ir.ProtocolChat {
		t.Fatalf("reasoning = %+v", req.Reasoning)
	}
	if features := InspectFeatures(body); !features.Image || !features.Reasoning {
		t.Fatalf("features = %+v", features)
	}
}

func TestParseRequestRejectsBadTool(t *testing.T) {
	body := []byte(`{"model":"m","messages":[],"tools":[{"type":"bogus","name":"x"}]}`)
	if _, err := ParseRequest(body); err == nil {
		t.Error("bogus tool accepted")
	}
}

// eventSource feeds a fixed event list.
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

func decodeChunk(t *testing.T, data string) map[string]any {
	t.Helper()
	payload := strings.TrimPrefix(data, "data: ")
	var doc map[string]any
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatalf("chunk not JSON: %q: %v", data, err)
	}
	return doc
}

func TestEncodeStreamTextOrder(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventTextDelta, Text: "Hel"},
		{Type: ir.EventTextDelta, Text: "lo"},
		{Type: ir.EventCompleted, StopReason: "stop"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n\n")
	if len(lines) < 5 || lines[len(lines)-1] != "data: [DONE]" {
		t.Fatalf("stream = %q", out)
	}
	var texts []string
	for _, l := range lines {
		if !strings.HasPrefix(l, "data: ") || strings.Contains(l, "[DONE]") {
			continue
		}
		doc := decodeChunk(t, l)
		choices := doc["choices"].([]any)
		delta := choices[0].(map[string]any)["delta"].(map[string]any)
		if c, ok := delta["content"].(string); ok && c != "" {
			texts = append(texts, c)
		}
	}
	if strings.Join(texts, "") != "Hello" {
		t.Errorf("text chunks = %v, want Hel+lo in order", texts)
	}
	// 完成事件带 finish_reason 且以 [DONE] 结束。
	last := decodeChunk(t, lines[len(lines)-2])
	fr := last["choices"].([]any)[0].(map[string]any)["finish_reason"]
	if fr != "stop" {
		t.Errorf("finish_reason = %v", fr)
	}
}

func TestEncodeStreamToolCallsKeepIds(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventToolCallStarted, ToolCallID: "call_a", ToolName: "fna"},
		{Type: ir.EventToolCallStarted, ToolCallID: "call_b", ToolName: "fnb"},
		{Type: ir.EventToolCallArgumentsDlt, ToolCallID: "call_b", ArgumentsDelta: `{"b":`},
		{Type: ir.EventToolCallArgumentsDlt, ToolCallID: "call_a", ArgumentsDelta: `{"a":1}`},
		{Type: ir.EventToolCallArgumentsDlt, ToolCallID: "call_b", ArgumentsDelta: `1}`},
		{Type: ir.EventCompleted, StopReason: "tool_calls"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	// 按 index 收集 arguments 片段，验证各调用不串。
	type acc struct{ args, id string }
	byIndex := map[float64]*acc{}
	var order []float64
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n\n") {
		if !strings.HasPrefix(l, "data: ") || strings.Contains(l, "[DONE]") {
			continue
		}
		doc := decodeChunk(t, l)
		choices := doc["choices"].([]any)
		delta := choices[0].(map[string]any)["delta"].(map[string]any)
		tcs, ok := delta["tool_calls"].([]any)
		if !ok {
			continue
		}
		tc := tcs[0].(map[string]any)
		idx := tc["index"].(float64)
		if _, seen := byIndex[idx]; !seen {
			byIndex[idx] = &acc{}
			order = append(order, idx)
		}
		if id, ok := tc["id"].(string); ok && id != "" {
			byIndex[idx].id = id
		}
		fn := tc["function"].(map[string]any)
		if a, ok := fn["arguments"].(string); ok && a != "" {
			byIndex[idx].args += a
		}
	}
	if len(byIndex) != 2 {
		t.Fatalf("tool indexes = %v", order)
	}
	if byIndex[order[0]].id != "call_a" || byIndex[order[0]].args != `{"a":1}` {
		t.Errorf("call_a = %+v", byIndex[order[0]])
	}
	if byIndex[order[1]].id != "call_b" || byIndex[order[1]].args != `{"b":1}` {
		t.Errorf("call_b = %+v", byIndex[order[1]])
	}
}

func TestEncodeStreamErrorNoFakeCompletion(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventTextDelta, Text: "partial"},
		{Type: ir.EventError, Error: &ir.ErrorInfo{Message: "boom"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "[DONE]") {
		t.Errorf("error stream must not end with [DONE]: %q", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("error stream should carry the error message: %q", out)
	}
}

func TestEncodeStreamBrokenStreamNoFakeCompletion(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventTextDelta, Text: "partial"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "[DONE]") {
		t.Errorf("broken stream must not fabricate [DONE]: %q", out)
	}
	if !strings.Contains(out, "unexpectedly") {
		t.Errorf("broken stream should end with an error event: %q", out)
	}
}

func TestEncodeNonStream(t *testing.T) {
	var buf bytes.Buffer
	resp := &ir.Response{
		Text:      "hello",
		ToolCalls: []ir.ToolCall{{ID: "call_1", Name: "f", Arguments: json.RawMessage(`{"a":1}`)}},
		Usage:     ir.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
	}
	if err := EncodeNonStream(&buf, "m", resp); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	msg := doc["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Errorf("content = %v", msg["content"])
	}
	tcs := msg["tool_calls"].([]any)
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_1" || tc["function"].(map[string]any)["name"] != "f" {
		t.Errorf("tool_calls = %v", tcs)
	}
	if doc["usage"].(map[string]any)["total_tokens"] != float64(3) {
		t.Errorf("usage = %v", doc["usage"])
	}
}
