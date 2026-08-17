package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ai-gateway/internal/ir"
	"ai-gateway/internal/secret"
)

func testIRRequest() *ir.Request {
	return &ir.Request{
		Model:  "claude-sonnet-4",
		Stream: false,
		System: []ir.Block{{Type: ir.BlockText, Text: "Be brief."}},
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.Block{{Type: ir.BlockText, Text: "hi"}}},
			{Role: ir.RoleAssistant, Content: []ir.Block{
				{Type: ir.BlockToolCall, ToolCall: &ir.ToolCall{
					ID: "toolu_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Berlin"}`),
				}},
			}},
			{Role: ir.RoleTool, Content: []ir.Block{
				{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{ID: "toolu_1", Content: "sunny"}},
			}},
		},
		Tools: []ir.Tool{{Name: "get_weather", Description: "weather", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
}

func TestDoWithHeadersOverlaysAnthropicDefaults(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := NewPool(nil).Client(server.URL, "").DoWithHeaders(context.Background(), []byte(`{}`), false, map[string]string{"User-Agent": "claude-cli/2.1.228 (external, cli)", "Anthropic-Beta": "claude-code-20250219"})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Get("User-Agent") != "claude-cli/2.1.228 (external, cli)" || got.Get("Anthropic-Beta") != "claude-code-20250219" || got.Get("Anthropic-Version") != APIVersion {
		t.Fatalf("custom headers = %v", got)
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
	if doc["model"] != "claude-sonnet-4" {
		t.Errorf("model = %v", doc["model"])
	}
	if doc["system"] != "Be brief." {
		t.Errorf("system = %v", doc["system"])
	}
	if doc["max_tokens"] == nil {
		t.Error("max_tokens required by Messages API is missing")
	}
	msgs := doc["messages"].([]any)
	user := msgs[0].(map[string]any)
	if user["role"] != "user" {
		t.Errorf("first message role = %v", user["role"])
	}
	assistant := msgs[1].(map[string]any)
	if assistant["role"] != "assistant" {
		t.Errorf("second message role = %v", assistant["role"])
	}
	content := assistant["content"].([]any)
	toolUse := content[0].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["id"] != "toolu_1" || toolUse["name"] != "get_weather" {
		t.Errorf("tool_use block = %v", toolUse)
	}
	toolResultMsg := msgs[2].(map[string]any)
	// 工具结果必须位于 user 消息中，且是 tool_result 块。
	if toolResultMsg["role"] != "user" {
		t.Errorf("tool result message role = %v", toolResultMsg["role"])
	}
	tr := toolResultMsg["content"].([]any)[0].(map[string]any)
	if tr["type"] != "tool_result" || tr["tool_use_id"] != "toolu_1" || tr["content"] != "sunny" {
		t.Errorf("tool_result block = %v", tr)
	}
	tools := doc["tools"].([]any)
	tool0 := tools[0].(map[string]any)
	if tool0["name"] != "get_weather" || tool0["input_schema"] == nil {
		t.Errorf("tools = %v", tools)
	}
}

func TestGenerateRequestMergesConsecutiveUser(t *testing.T) {
	req := testIRRequest()
	req.Messages = []ir.Message{
		{Role: ir.RoleUser, Content: []ir.Block{{Type: ir.BlockText, Text: "one"}}},
		{Role: ir.RoleUser, Content: []ir.Block{{Type: ir.BlockText, Text: "two"}}},
	}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Messages) != 1 || doc.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v", doc.Messages)
	}
	if len(doc.Messages[0].Content) != 2 || doc.Messages[0].Content[0].Text != "one" || doc.Messages[0].Content[1].Text != "two" {
		t.Fatalf("content = %+v", doc.Messages[0].Content)
	}
}

func TestGenerateRequestCustomToolSchema(t *testing.T) {
	req := testIRRequest()
	req.Tools = []ir.Tool{{Name: "exec", Description: "Run JS", Custom: true}}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Tools) != 1 || doc.Tools[0].Name != "exec" || !ir.ValidJSONObject(doc.Tools[0].InputSchema) {
		t.Fatalf("tools = %+v", doc.Tools)
	}
	if !strings.Contains(string(doc.Tools[0].InputSchema), `"input"`) {
		t.Fatalf("schema = %s", doc.Tools[0].InputSchema)
	}
}

func TestGenerateRequestToolChoiceMapping(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"auto"`, `"auto"`},
		{`"none"`, `"none"`},
		{`"required"`, `"any"`}, // required 在 Messages 中最近似 any
		{`{"type":"function","name":"f"}`, `{"name":"f","type":"tool"}`},
	}
	for _, tc := range cases {
		req := testIRRequest()
		req.ToolChoice = json.RawMessage(tc.in)
		body, err := GenerateRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(doc["tool_choice"])
		if string(got) != tc.want {
			t.Errorf("tool_choice %s → %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestGenerateRequestImageAndThinking(t *testing.T) {
	req := testIRRequest()
	req.Messages = []ir.Message{{Role: ir.RoleUser, Content: []ir.Block{
		{Type: ir.BlockImage, Image: &ir.Image{Base64: "AAAA", MediaType: "image/png"}},
	}}}
	req.Reasoning = ir.ReasoningConfig{Enabled: true, Type: "enabled", BudgetTokens: 2048, Source: ir.ProtocolMessages}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	thinking := doc["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(2048) {
		t.Fatalf("thinking = %v", thinking)
	}
	messages := doc["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	source := content[0].(map[string]any)["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "image/png" || source["data"] != "AAAA" {
		t.Fatalf("source = %v", source)
	}
}

func TestParseResponse(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"m",
		"content":[
			{"type":"thinking","thinking":"considering","signature":"sig"},
			{"type":"text","text":"hello"},
			{"type":"tool_use","id":"toolu_1","name":"f","input":{"a":1}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":3,"output_tokens":4}
	}`)
	events, err := ParseResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	var toolID string
	var toolArgs string
	var reasoning string
	usage := ir.Usage{}
	for _, ev := range events {
		switch ev.Type {
		case ir.EventReasoningDelta:
			reasoning += ev.Text
		case ir.EventToolCallStarted:
			toolID = ev.ToolCallID
		case ir.EventToolCallCompleted:
			toolArgs = ev.Arguments
		case ir.EventUsage:
			usage = ev.Usage
		}
	}
	if reasoning != "considering" {
		t.Errorf("reasoning = %q", reasoning)
	}
	if toolID != "toolu_1" {
		t.Errorf("tool id = %q", toolID)
	}
	if toolArgs != `{"a":1}` {
		t.Errorf("tool args = %q", toolArgs)
	}
	if usage.TotalTokens != 7 {
		t.Errorf("usage = %+v (total must be in+out)", usage)
	}
	if events[len(events)-1].Type != ir.EventCompleted || events[len(events)-1].StopReason != "tool_use" {
		t.Errorf("last event = %+v", events[len(events)-1])
	}
}

func TestStreamReaderTextAndToolDelta(t *testing.T) {
	stream := `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"m","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\": \"Ber"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"lin\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Done"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}
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
	resp := seq.Accumulate()
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_1" || tc.Name != "get_weather" {
		t.Errorf("tool call = %+v", tc)
	}
	if string(tc.Arguments) != `{"city": "Berlin"}` {
		t.Errorf("arguments = %q (input_json_delta 按序拼接)", tc.Arguments)
	}
	if resp.Text != "Done" {
		t.Errorf("text = %q", resp.Text)
	}
	if !resp.Completed {
		t.Error("not completed")
	}
}

func TestStreamReaderErrorEvent(t *testing.T) {
	stream := `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"m"}}

event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}
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
	if last.Type != ir.EventError || last.Error == nil || last.Error.Message != "overloaded" {
		t.Errorf("last = %+v", last)
	}
}

func TestStreamReaderThinking(t *testing.T) {
	stream := `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"m"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"considering"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}
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
		{"https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
		{"https://api.anthropic.com/", "https://api.anthropic.com/v1/messages"},
		{"https://gateway.example/v1", "https://gateway.example/v1/messages"},
	} {
		if got := CompletionURL(tc.base); got != tc.want {
			t.Errorf("CompletionURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// captureUpstream records auth headers and the raw body of one request.
type captureUpstream struct {
	t *testing.T
	*httptest.Server
	mu    sync.Mutex
	auth  string
	ver   string
	body  string
	count int
}

func newCaptureUpstream(t *testing.T) *captureUpstream {
	t.Helper()
	c := &captureUpstream{t: t}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.auth = r.Header.Get("x-api-key")
		c.ver = r.Header.Get("anthropic-version")
		c.body = string(body)
		c.count++
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"m","content":[],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`))
	}))
	t.Cleanup(c.Close)
	return c
}

func TestDoAnthropicAuthHeaders(t *testing.T) {
	up := newCaptureUpstream(t)
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.claude", []byte("sk-ant-secret")); err != nil {
		t.Fatal(err)
	}
	pool := NewPool(store)
	client := pool.Client(up.URL, "provider.claude")
	resp, err := client.Do(context.Background(), []byte(`{}`), false)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if up.auth != "sk-ant-secret" {
		t.Errorf("x-api-key = %q", up.auth)
	}
	if up.ver != APIVersion {
		t.Errorf("anthropic-version = %q, want %q", up.ver, APIVersion)
	}
	// 无钥匙 provider 不发认证。
	up2 := newCaptureUpstream(t)
	client2 := NewPool(nil).Client(up2.URL, "")
	resp2, err := client2.Do(context.Background(), []byte(`{}`), false)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if up2.auth != "" {
		t.Errorf("keyless provider sent x-api-key %q", up2.auth)
	}
}
