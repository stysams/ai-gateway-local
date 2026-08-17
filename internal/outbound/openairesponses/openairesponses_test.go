package openairesponses

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-gateway/internal/ir"
)

func TestDoCompactWithHeaders(t *testing.T) {
	var got http.Header
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, path = r.Header.Clone(), r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := NewPool(nil).Client(server.URL, "").DoCompactWithHeaders(context.Background(), []byte(`{}`), map[string]string{"Originator": "codex_cli_rs"})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if path != "/responses/compact" || got.Get("Originator") != "codex_cli_rs" {
		t.Fatalf("path = %q, headers = %v", path, got)
	}
}

func testIRRequest() *ir.Request {
	return &ir.Request{
		Model:  "qwen3",
		Stream: false,
		System: []ir.Block{{Type: ir.BlockText, Text: "Be brief."}},
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.Block{{Type: ir.BlockText, Text: "hi"}}},
			{Role: ir.RoleAssistant, Content: []ir.Block{
				{Type: ir.BlockToolCall, ToolCall: &ir.ToolCall{
					ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Berlin"}`),
				}},
			}},
			{Role: ir.RoleTool, Content: []ir.Block{
				{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{ID: "call_1", Content: "sunny"}},
			}},
		},
		Tools: []ir.Tool{{Name: "get_weather", Description: "weather", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
}

func TestGenerateRequest(t *testing.T) {
	body, err := GenerateRequest(testIRRequest())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if doc["model"] != "qwen3" {
		t.Errorf("model = %v", doc["model"])
	}
	if doc["instructions"] != "Be brief." {
		t.Errorf("instructions = %v", doc["instructions"])
	}
	input, ok := doc["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("input = %v", doc["input"])
	}
	// user 文本 → message item。
	first := input[0].(map[string]any)
	if first["type"] != "message" || first["role"] != "user" {
		t.Errorf("first input item = %v", first)
	}
	// 工具调用 → function_call item（call_id 保持稳定）。
	tc := input[1].(map[string]any)
	if tc["type"] != "function_call" || tc["call_id"] != "call_1" || tc["name"] != "get_weather" {
		t.Errorf("tool call item = %v", tc)
	}
	// 工具结果 → function_call_output。
	tr := input[2].(map[string]any)
	if tr["type"] != "function_call_output" || tr["call_id"] != "call_1" || tr["output"] != "sunny" {
		t.Errorf("tool result item = %v", tr)
	}
	tools := doc["tools"].([]any)
	tool0 := tools[0].(map[string]any)
	if tool0["type"] != "function" || tool0["name"] != "get_weather" {
		t.Errorf("tools = %v", tools)
	}
}

func TestGenerateRequestUserToolResultEmitsFunctionCallOutput(t *testing.T) {
	// Claude Code keeps tool_result on the user turn, mixed with later
	// text. Responses rejects a function_call that has no matching output
	// (docs/v1-scheme.md §20 2026-08-16, req_028fc2898f548d37d54f89be).
	req := &ir.Request{
		Model: "gpt-5.6-terra",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.Block{{Type: ir.BlockText, Text: "成都双流今日天气如何"}}},
			{Role: ir.RoleAssistant, Content: []ir.Block{{
				Type: ir.BlockToolCall,
				ToolCall: &ir.ToolCall{
					ID:        "call_qIj90apTNbh1A1zQOxjoYiex",
					Name:      "ToolSearch",
					Arguments: json.RawMessage(`{"query":"select:WebSearch"}`),
				},
			}}},
			{Role: ir.RoleUser, Content: []ir.Block{
				{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
					ID:      "call_qIj90apTNbh1A1zQOxjoYiex",
					Content: `[{"type":"tool_reference","tool_name":"WebSearch"}]`,
				}},
				{Type: ir.BlockText, Text: "Tool loaded."},
			}},
		},
	}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			CallID  string `json:"call_id"`
			Name    string `json:"name"`
			Output  string `json:"output"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Input) != 4 {
		t.Fatalf("input = %#v", doc.Input)
	}
	if doc.Input[1].Type != "function_call" || doc.Input[1].CallID != "call_qIj90apTNbh1A1zQOxjoYiex" || doc.Input[1].Name != "ToolSearch" {
		t.Fatalf("function_call = %#v", doc.Input[1])
	}
	if doc.Input[2].Type != "function_call_output" || doc.Input[2].CallID != "call_qIj90apTNbh1A1zQOxjoYiex" || doc.Input[2].Output != `[{"type":"tool_reference","tool_name":"WebSearch"}]` {
		t.Fatalf("function_call_output = %#v", doc.Input[2])
	}
	if doc.Input[3].Type != "message" || doc.Input[3].Role != "user" || len(doc.Input[3].Content) != 1 || doc.Input[3].Content[0].Text != "Tool loaded." {
		t.Fatalf("leftover user = %#v", doc.Input[3])
	}
}

func TestGenerateRequestAssistantHistoryUsesOutputText(t *testing.T) {
	req := &ir.Request{
		Model: "gpt-5.6-terra",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.Block{{Type: ir.BlockText, Text: "你是谁"}}},
			{Role: ir.RoleAssistant, Content: []ir.Block{{Type: ir.BlockText, Text: "我是助手"}}},
			{Role: ir.RoleUser, Content: []ir.Block{{Type: ir.BlockText, Text: "继续"}}},
		},
	}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Input) != 3 {
		t.Fatalf("input = %#v", doc.Input)
	}
	if doc.Input[0].Role != "user" || doc.Input[0].Content[0].Type != "input_text" {
		t.Fatalf("user item = %#v", doc.Input[0])
	}
	if doc.Input[1].Role != "assistant" || doc.Input[1].Content[0].Type != "output_text" || doc.Input[1].Content[0].Text != "我是助手" {
		t.Fatalf("assistant item = %#v", doc.Input[1])
	}
	if doc.Input[2].Role != "user" || doc.Input[2].Content[0].Type != "input_text" {
		t.Fatalf("follow-up user item = %#v", doc.Input[2])
	}
}

func TestGenerateRequestImageAndReasoning(t *testing.T) {
	req := testIRRequest()
	req.Messages = []ir.Message{{Role: ir.RoleUser, Content: []ir.Block{
		{Type: ir.BlockImage, Image: &ir.Image{URL: "https://x/i.png"}},
	}}}
	req.Reasoning = ir.ReasoningConfig{Enabled: true, Effort: "high", Summary: "concise", Source: ir.ProtocolChat}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	reasoning := doc["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "concise" {
		t.Fatalf("reasoning = %v", reasoning)
	}
	input := doc["input"].([]any)
	content := input[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["image_url"] != "https://x/i.png" {
		t.Fatalf("content = %v", content)
	}
}

func TestParseResponse(t *testing.T) {
	body := []byte(`{
		"id":"resp_1","object":"response","status":"completed","model":"m",
		"output":[
			{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"considering"}]},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]},
			{"id":"fc_1","type":"function_call","call_id":"call_1","name":"f","arguments":"{\"a\":1}","status":"completed"}
		],
		"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}
	}`)
	events, err := ParseResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Type != ir.EventStarted {
		t.Errorf("first event = %v", events[0].Type)
	}
	if events[1].Type != ir.EventReasoningDelta || events[1].Text != "considering" {
		t.Fatalf("reasoning event = %+v", events[1])
	}
	found := map[ir.EventType]int{}
	for _, ev := range events {
		found[ev.Type]++
	}
	if found[ir.EventTextDelta] != 1 || found[ir.EventToolCallStarted] != 1 {
		t.Errorf("events = %v", found)
	}
	// 工具 id 稳定。
	for _, ev := range events {
		if ev.Type == ir.EventToolCallStarted && ev.ToolCallID != "call_1" {
			t.Errorf("tool id = %q", ev.ToolCallID)
		}
	}
	// 参数完整到达。
	for _, ev := range events {
		if ev.Type == ir.EventToolCallCompleted && ev.Arguments != `{"a":1}` {
			t.Errorf("arguments = %q", ev.Arguments)
		}
	}
	if events[len(events)-1].Type != ir.EventCompleted {
		t.Errorf("last event = %v", events[len(events)-1].Type)
	}
}

func TestParseResponseFailedStatus(t *testing.T) {
	body := []byte(`{"id":"r","object":"response","status":"failed","output":[],"error":{"code":"x","message":"boom"}}`)
	events, err := ParseResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != ir.EventError || last.Error == nil || last.Error.Message != "boom" {
		t.Errorf("last = %+v", last)
	}
}

func TestStreamReaderTextAndTools(t *testing.T) {
	stream := `event: response.created
data: {"type":"response.created","response":{"id":"r"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":"","status":"in_progress"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"city\": \"Ber"}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"lin\"}"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hi"}

event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"city\": \"Berlin\"}"}

event: response.completed
data: {"type":"response.completed","response":{"id":"r"}}
`
	sr := NewStreamReader(strings.NewReader(stream))
	seq := ir.NewSequencer()
	var got []ir.Event
	for {
		ev, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := seq.Push(ev); err != nil {
			t.Fatalf("push %s: %v", ev.Type, err)
		}
		got = append(got, ev)
	}
	resp := seq.Accumulate()
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "get_weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if string(tc.Arguments) != `{"city": "Berlin"}` {
		t.Errorf("arguments = %q (delta 必须按序拼接)", tc.Arguments)
	}
	if resp.Text != "hi" {
		t.Errorf("text = %q", resp.Text)
	}
	if !resp.Completed {
		t.Error("not completed")
	}
}

func TestStreamReaderFailedEvent(t *testing.T) {
	stream := `event: response.created
data: {"type":"response.created"}

event: response.failed
data: {"type":"response.failed","response":{"id":"r","status":"failed","error":{"code":"e","message":"boom"}}}
`
	sr := NewStreamReader(strings.NewReader(stream))
	var last ir.Event
	for {
		ev, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		last = ev
	}
	if last.Type != ir.EventError || last.Error == nil || last.Error.Message != "boom" {
		t.Errorf("last = %+v", last)
	}
}

func TestStreamReaderReasoning(t *testing.T) {
	stream := `event: response.created
data: {"type":"response.created","response":{"id":"r"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[],"status":"in_progress"}}

event: response.reasoning_summary_part.added
data: {"type":"response.reasoning_summary_part.added","item_id":"rs_1","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}

event: response.reasoning_summary_text.delta
data: {"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"summary_index":0,"delta":"considering"}

event: response.reasoning_summary_text.done
data: {"type":"response.reasoning_summary_text.done","item_id":"rs_1","output_index":0,"summary_index":0,"text":"considering"}

event: response.completed
data: {"type":"response.completed","response":{"id":"r"}}
`
	sr := NewStreamReader(strings.NewReader(stream))
	seq := ir.NewSequencer()
	for {
		ev, err := sr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := seq.Push(ev); err != nil {
			t.Fatalf("push %s: %v", ev.Type, err)
		}
	}
	if got := seq.Accumulate().Reasoning; got != "considering" {
		t.Fatalf("reasoning = %q", got)
	}
}

func TestCompletionURL(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://api.openai.com/v1", "https://api.openai.com/v1/responses"},
		{"https://api.x.ai", "https://api.x.ai/responses"},
		{"http://127.0.0.1:11434/", "http://127.0.0.1:11434/responses"},
	} {
		if got := CompletionURL(tc.base); got != tc.want {
			t.Errorf("CompletionURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
		if got := CompactURL(tc.base); got != tc.want+"/compact" {
			t.Errorf("CompactURL(%q) = %q, want %q", tc.base, got, tc.want+"/compact")
		}
	}
}

func TestGenerateRequestToolChoicePassthrough(t *testing.T) {
	req := testIRRequest()
	req.ToolChoice = json.RawMessage(`{"type":"function","name":"get_weather"}`)
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	tc, _ := json.Marshal(doc["tool_choice"])
	if string(tc) != `{"name":"get_weather","type":"function"}` {
		t.Errorf("tool_choice = %s", tc)
	}
}
