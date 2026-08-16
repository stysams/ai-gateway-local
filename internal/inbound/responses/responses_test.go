package responses

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"ai-gateway/internal/ir"
)

func TestParseRequestItems(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"instructions": "Be brief.",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]},
			{"type": "function_call", "call_id": "call_1", "name": "f", "arguments": "{\"a\":1}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "ok"}
		]
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
	if req.Messages[0].Role != ir.RoleUser || req.Messages[0].Content[0].Text != "hi" {
		t.Errorf("user = %+v", req.Messages[0])
	}
	if req.Messages[1].Content[0].ToolCall == nil || req.Messages[1].Content[0].ToolCall.ID != "call_1" {
		t.Errorf("tool call = %+v", req.Messages[1])
	}
	if req.Messages[2].Role != ir.RoleTool || req.Messages[2].Content[0].ToolResult.Content != "ok" {
		t.Errorf("tool result = %+v", req.Messages[2])
	}
}

func TestParseRequestAssistantOutputText(t *testing.T) {
	body := []byte(`{
		"model": "opencode/deepseek-v4-flash",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "你师祖"}]},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "闲聊"}], "encrypted_content": null},
			{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "我是 Codex"}]}
		]
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %+v", req.Messages)
	}
	if req.Messages[0].Role != ir.RoleUser || req.Messages[0].Content[0].Text != "你师祖" {
		t.Fatalf("user = %+v", req.Messages[0])
	}
	if req.Messages[1].Role != ir.RoleAssistant || req.Messages[1].Content[0].Type != ir.BlockReasoning {
		t.Fatalf("reasoning = %+v", req.Messages[1])
	}
	if req.Messages[2].Role != ir.RoleAssistant || req.Messages[2].Content[0].Type != ir.BlockText || req.Messages[2].Content[0].Text != "我是 Codex" {
		t.Fatalf("assistant = %+v", req.Messages[2])
	}
}

func TestParseRequestCodexDesktopTools(t *testing.T) {
	body := []byte(`{
		"model": "aa/claude-opus-4-6",
		"instructions": "You are Codex",
		"input": [
			{"type": "message", "role": "developer", "content": [{"type": "input_text", "text": "app context"}]},
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "你是谁"}]}
		],
		"tools": [
			{"type": "custom", "name": "exec", "description": "Run JS", "format": {"type": "grammar", "syntax": "lark", "definition": "start: SOURCE"}},
			{"type": "function", "name": "wait", "description": "wait", "parameters": {"type": "object", "properties": {"cell_id": {"type": "string"}}, "required": ["cell_id"]}},
			{"type": "namespace", "name": "collaboration", "tools": [
				{"type": "function", "name": "spawn_agent", "description": "spawn", "parameters": {"type": "object", "properties": {}}}
			]},
			{"type": "web_search", "external_web_access": true}
		]
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 2 || req.System[0].Text != "You are Codex" || req.System[1].Text != "app context" {
		t.Fatalf("system = %+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != ir.RoleUser || req.Messages[0].Content[0].Text != "你是谁" {
		t.Fatalf("messages = %+v", req.Messages)
	}
	if len(req.Tools) != 3 {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if !req.Tools[0].Custom || req.Tools[0].Name != "exec" || !ir.ValidJSONObject(req.Tools[0].Parameters) {
		t.Fatalf("exec = %+v", req.Tools[0])
	}
	if req.Tools[1].Custom || req.Tools[1].Name != "wait" {
		t.Fatalf("wait = %+v", req.Tools[1])
	}
	if req.Tools[2].Name != "spawn_agent" {
		t.Fatalf("namespace flatten = %+v", req.Tools[2])
	}
	if len(req.DroppedTools) != 2 {
		t.Fatalf("dropped = %+v", req.DroppedTools)
	}
	if req.DroppedTools[0].Type != "custom_format" || req.DroppedTools[0].Name != "exec" {
		t.Fatalf("format drop = %+v", req.DroppedTools[0])
	}
	if req.DroppedTools[1].Type != "web_search" {
		t.Fatalf("hosted drop = %+v", req.DroppedTools[1])
	}
}

func TestParseRequestCustomToolCallRoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"input": [
			{"type": "custom_tool_call", "call_id": "call_exec", "name": "exec", "input": "await tools.exec_command({cmd: 'hi'})"},
			{"type": "custom_tool_call_output", "call_id": "call_exec", "output": "ok"}
		]
	}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %+v", req.Messages)
	}
	call := req.Messages[0].Content[0].ToolCall
	if call == nil || !call.Custom || call.Name != "exec" {
		t.Fatalf("call = %+v", call)
	}
	if ir.UnwrapFreeformInput(call.Arguments) != "await tools.exec_command({cmd: 'hi'})" {
		t.Fatalf("arguments = %s", call.Arguments)
	}
	if req.Messages[1].Content[0].ToolResult.Content != "ok" {
		t.Fatalf("output = %+v", req.Messages[1])
	}
}

func TestParseRequestUnknownToolType(t *testing.T) {
	_, err := ParseRequest([]byte(`{"model":"m","tools":[{"type":"local_shell"}]}`))
	if err == nil || !strings.Contains(err.Error(), `tool type "local_shell"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEncodeStreamCustomToolCall(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventToolCallStarted, ToolCallID: "call_exec", ToolName: "exec", ToolCustom: true},
		{Type: ir.EventToolCallArgumentsDlt, ToolCallID: "call_exec", ArgumentsDelta: `{"in`, ToolCustom: true},
		{Type: ir.EventToolCallCompleted, ToolCallID: "call_exec", ToolName: "exec", Arguments: `{"input":"await tools.exec_command({cmd: 'hi'})"}`, ToolCustom: true},
		{Type: ir.EventCompleted},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`"type":"custom_tool_call"`,
		`"call_id":"call_exec"`,
		"event: response.custom_tool_call_input.done",
		`"input":"await tools.exec_command({cmd: 'hi'})"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"type":"function_call"`) || strings.Contains(out, "function_call_arguments") {
		t.Errorf("custom tool encoded as function call:\n%s", out)
	}
	if strings.Contains(out, `"delta":"{\"in`) {
		t.Errorf("JSON wrapper delta leaked:\n%s", out)
	}
}

func TestParseRequestStringInput(t *testing.T) {
	req, err := ParseRequest([]byte(`{"model":"m","input":"just text"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content[0].Text != "just text" {
		t.Errorf("messages = %+v", req.Messages)
	}
}

func TestParseRequestImageAndReasoning(t *testing.T) {
	body := []byte(`{"model":"m","reasoning":{"effort":"medium","summary":"concise"},"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://x/i.png"}]}]}`)
	req, err := ParseRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	image := req.Messages[0].Content[0].Image
	if image == nil || image.URL != "https://x/i.png" {
		t.Fatalf("image = %+v", image)
	}
	if req.Reasoning.Effort != "medium" || req.Reasoning.Summary != "concise" || req.Reasoning.Source != ir.ProtocolResponses {
		t.Fatalf("reasoning = %+v", req.Reasoning)
	}
	if features := InspectFeatures(body); !features.Image || !features.Reasoning {
		t.Fatalf("features = %+v", features)
	}
}

func TestFieldsRewritePreservesUnknown(t *testing.T) {
	req, err := Parse([]byte(`{"model":"m","input":"hi","temperature":0.5,"x-extra":{"k":1}}`))
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
	if doc["model"] != "new-model" || doc["stream"] != true {
		t.Errorf("doc = %v", doc)
	}
	if doc["x-extra"] == nil || doc["temperature"] == nil {
		t.Error("unknown fields lost")
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

func TestEncodeStreamTextAndCompletion(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventTextDelta, Text: "Hel"},
		{Type: ir.EventTextDelta, Text: "lo"},
		{Type: ir.EventCompleted},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.content_part.added",
		"event: response.output_text.delta",
		`"delta":"Hel"`,
		`"delta":"lo"`,
		"event: response.output_text.done",
		"event: response.output_item.done",
		"event: response.completed",
		`"type":"response.completed"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "response.failed") {
		t.Errorf("successful stream contains failed: %s", out)
	}
	if strings.Count(out, "event: response.output_item.added") != 1 {
		t.Errorf("text deltas must share one output item:\n%s", out)
	}
	if !strings.Contains(out, `"text":"Hello"`) {
		t.Errorf("output_text.done must carry concatenated text:\n%s", out)
	}
	completedAt := strings.Index(out, "event: response.completed")
	doneAt := strings.Index(out, "event: response.output_item.done")
	if completedAt < 0 || doneAt < 0 || doneAt > completedAt {
		t.Errorf("output_item.done must precede response.completed:\n%s", out)
	}
	// 文本 delta 顺序。
	if strings.Index(out, "Hel") > strings.Index(out, "lo") {
		t.Error("delta order wrong")
	}
}

func TestEncodeStreamToolCall(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventToolCallStarted, ToolCallID: "call_1", ToolName: "f"},
		{Type: ir.EventToolCallArgumentsDlt, ToolCallID: "call_1", ArgumentsDelta: `{"a":`},
		{Type: ir.EventToolCallArgumentsDlt, ToolCallID: "call_1", ArgumentsDelta: `1}`},
		{Type: ir.EventToolCallCompleted, ToolCallID: "call_1", ToolName: "f", Arguments: `{"a":1}`},
		{Type: ir.EventCompleted},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`"type":"function_call"`,
		`"call_id":"call_1"`,
		"event: response.function_call_arguments.delta",
		`"delta":"{\"a\":"`,
		"event: response.function_call_arguments.done",
		`"arguments":"{\"a\":1}"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
}

func TestEncodeStreamReasoningThenTextClosesItems(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventReasoningDelta, Text: "think"},
		{Type: ir.EventTextDelta, Text: "say"},
		{Type: ir.EventCompleted},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"event: response.reasoning_summary_part.added",
		"event: response.reasoning_summary_text.delta",
		"event: response.reasoning_summary_text.done",
		"event: response.reasoning_summary_part.done",
		`"type":"response.completed"`,
		`"sequence_number":`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "event: response.reasoning_summary_text.done") > strings.Index(out, "event: response.output_text.done") {
		t.Errorf("reasoning item must close before later text item:\n%s", out)
	}
}

func TestEncodeStreamEOFWithoutCompletedIsFailed(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventTextDelta, Text: "partial"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "event: response.completed") {
		t.Errorf("truncated stream must not fabricate response.completed: %s", out)
	}
	if !strings.Contains(out, `"type":"response.failed"`) || !strings.Contains(out, "ended before response.completed") {
		t.Errorf("truncated stream should carry response.failed: %s", out)
	}
}

func TestEncodeStreamErrorNoCompletion(t *testing.T) {
	var buf bytes.Buffer
	err := EncodeStream(&buf, func() {}, "m", eventSource([]ir.Event{
		{Type: ir.EventStarted},
		{Type: ir.EventError, Error: &ir.ErrorInfo{Message: "boom"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "response.completed") {
		t.Errorf("error stream must not contain response.completed: %s", out)
	}
	if !strings.Contains(out, "response.failed") || !strings.Contains(out, "boom") {
		t.Errorf("error stream should carry response.failed: %s", out)
	}
}

func TestEncodeNonStream(t *testing.T) {
	var buf bytes.Buffer
	resp := &ir.Response{Text: "hello", ToolCalls: []ir.ToolCall{{ID: "call_1", Name: "f", Arguments: json.RawMessage(`{}`)}}}
	if err := EncodeNonStream(&buf, "m", resp); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["object"] != "response" || doc["status"] != "completed" {
		t.Errorf("doc = %v", doc)
	}
	output := doc["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output = %v", output)
	}
	msg := output[0].(map[string]any)
	if msg["type"] != "message" {
		t.Errorf("output[0] = %v", msg)
	}
	fc := output[1].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" {
		t.Errorf("output[1] = %v", fc)
	}
}
