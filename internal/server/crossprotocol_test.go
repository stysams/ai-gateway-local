package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

// dualProviderConfig builds a config with two providers: upChat (adapter
// openai-chat, route target of codex) and upAnt (adapter anthropic,
// generic's route target).
func dualProviderConfig(t *testing.T, chatAdapter, antAdapter string) (*config.Config, string, string) {
	t.Helper()
	chatUp := newFakeUpstream(t, nil)
	antUp := newFakeUpstream(t, nil)
	c := config.Defaults()
	c.Providers["openrouter"] = config.Provider{
		Name: "OpenRouter", Adapter: chatAdapter,
		BaseURL: chatUp.URL, DefaultModel: "anthropic/claude-sonnet-4",
	}
	c.Providers["ollama"] = config.Provider{
		Name: "Ollama", Adapter: antAdapter,
		BaseURL: antUp.URL, DefaultModel: "qwen3",
	}
	c.Routes.Codex = config.Route{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"}
	c.Routes.Generic = config.Route{Provider: "ollama", Model: "qwen3"}
	return c, chatUp.URL, antUp.URL
}

// responsesTextHandler answers a non-streaming Responses document.
func responsesTextHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}
}

// messagesTextHandler answers a non-streaming Messages document.
func messagesTextHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}
}

const responsesTextBody = `{
	"id":"resp_1","object":"response","status":"completed","model":"m",
	"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello from responses","annotations":[]}]}],
	"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}
}`

const messagesTextBody = `{
	"id":"msg_1","type":"message","role":"assistant","model":"m",
	"content":[{"type":"text","text":"hello from messages"}],
	"stop_reason":"end_turn","stop_sequence":null,
	"usage":{"input_tokens":3,"output_tokens":4}
}`

// chatToResponsesNonStream: chat 客户端 → responses 上游，文本直转。
func TestCrossChatToResponsesNonStream(t *testing.T) {
	up := newFakeUpstream(t, responsesTextHandler(responsesTextBody))
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/chat/completions",
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "hello from responses") {
		t.Errorf("body = %s", data)
	}
	// 上游必须收到 Responses 请求外形（input 列表）。
	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream requests = %d", len(reqs))
	}
	if _, ok := reqs[0].Fields["input"]; !ok {
		t.Errorf("upstream body is not a Responses request: %v", reqs[0].Fields)
	}
	// 响应内容类型与 usage 外形是 chat.completion。
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
}

// responsesToChatNonStream: responses 客户端 → chat 上游。
func TestCrossResponsesToChatNonStream(t *testing.T) {
	up := newFakeUpstream(t, nil) // chat 默认 handler 返回 pong
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-chat"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/responses",
		[]byte(`{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "pong") {
		t.Errorf("body = %s", data)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["object"] != "response" {
		t.Errorf("object = %v, want response", doc["object"])
	}
	output := doc["output"].([]any)
	if len(output) == 0 {
		t.Fatalf("output empty: %s", data)
	}
	first := output[0].(map[string]any)
	if first["type"] != "message" {
		t.Errorf("output[0].type = %v", first["type"])
	}
}

// chatToMessagesNonStream 已在 TestChatToMessagesCrossProtocolDispatch 覆盖。

// messagesToChatNonStream: messages 客户端 → chat 上游。
func TestCrossMessagesToChatNonStream(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-chat"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/messages",
		[]byte(`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "pong") {
		t.Errorf("body = %s", data)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["type"] != "message" {
		t.Errorf("type = %v, want message", doc["type"])
	}
}

func TestCrossClaudeMessagesSystemRoleToChatStream(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"id\":\"chatcmpl-claude\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n")
		io.WriteString(w, "data: {\"id\":\"chatcmpl-claude\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		io.WriteString(w, "data: {\"id\":\"chatcmpl-claude\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "openai-chat"
	cfg.Providers["openrouter"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/claude/v1/messages", []byte(`{
		"model":"gateway-default",
		"max_tokens":32000,
		"stream":true,
		"system":[{"type":"text","text":"Base instructions","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"}]},
			{"role":"system","content":[{"type":"text","text":"Deferred ToolSearch guidance","cache_control":{"type":"ephemeral"}}]}
		]
	}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || !strings.Contains(string(data), "event: message_stop") {
		t.Fatalf("unexpected Messages stream: content-type=%q body=%s", resp.Header.Get("Content-Type"), data)
	}

	captured := up.last()
	var messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(captured.Fields["messages"], &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("upstream messages = %+v", messages)
	}
	if messages[0].Role != "system" || messages[0].Content != "Base instructions\n\nDeferred ToolSearch guidance" {
		t.Fatalf("upstream system message = %+v", messages[0])
	}
	if messages[1].Role != "user" || messages[1].Content != "hello" {
		t.Fatalf("upstream user message = %+v", messages[1])
	}
}

// Codex Desktop 的 custom / namespace / web_search 工具跨到 Messages。
func TestCrossResponsesCodexDesktopToolsToMessages(t *testing.T) {
	up := newFakeUpstream(t, messagesTextHandler(messagesTextBody))
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "anthropic"
	cfg.Providers["openrouter"] = p
	s, addr := startWithStore(t, cfg, secret.NewMemStore())

	body := []byte(`{
		"model":"aa/claude-opus-4-6",
		"instructions":"You are Codex",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"app context"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"你是谁"}]}
		],
		"tools":[
			{"type":"custom","name":"exec","description":"Run JS","format":{"type":"grammar","syntax":"lark","definition":"start: SOURCE"}},
			{"type":"function","name":"wait","description":"wait","parameters":{"type":"object","properties":{"cell_id":{"type":"string"}},"required":["cell_id"]}},
			{"type":"namespace","name":"collaboration","tools":[
				{"type":"function","name":"spawn_agent","description":"spawn","parameters":{"type":"object","properties":{}}}
			]},
			{"type":"web_search","external_web_access":true}
		]
	}`)
	resp, data := chatPost(t, addr, "/c/codex/v1/responses", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream requests = %d", len(reqs))
	}
	var tools []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(reqs[0].Fields["tools"], &tools); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = true
		if tool.Name == "web_search" || tool.Type == "web_search" {
			t.Fatalf("hosted web_search reached upstream: %+v", tools)
		}
	}
	for _, name := range []string{"exec", "wait", "spawn_agent"} {
		if !got[name] {
			t.Errorf("missing upstream tool %s in %+v", name, tools)
		}
	}
	if !strings.Contains(string(reqs[0].Fields["system"]), "You are Codex") ||
		!strings.Contains(string(reqs[0].Fields["system"]), "app context") {
		t.Fatalf("system = %s", reqs[0].Fields["system"])
	}
	var messages []struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(reqs[0].Fields["messages"], &messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != "user" {
		t.Fatalf("upstream messages = %+v", messages)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(s.cfg.Path()), "logs", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("log files = %v, err = %v", files, err)
	}
	warning, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(warning), `"code":"tool_dropped"`) ||
		!strings.Contains(string(warning), `"web_search"`) ||
		!strings.Contains(string(warning), `"custom_format"`) {
		t.Fatalf("warning = %s", warning)
	}
}

func TestCrossResponsesCustomToolCallStream(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"m","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_exec","name":"exec","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"input\":\"await tools.exec_command({cmd: 'hi'})\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}

`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "anthropic"
	cfg.Providers["openrouter"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	body := []byte(`{
		"model":"m","stream":true,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools":[{"type":"custom","name":"exec","description":"Run JS"}]
	}`)
	resp, data := chatPost(t, addr, "/c/codex/v1/responses", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	out := string(data)
	if !strings.Contains(out, `"type":"custom_tool_call"`) || !strings.Contains(out, "await tools.exec_command({cmd: 'hi'})") {
		t.Fatalf("client stream = %s", out)
	}
	if strings.Contains(out, `"type":"function_call"`) {
		t.Fatalf("custom tool leaked as function_call: %s", out)
	}
}

func TestCrossResponsesChatToolCallIndexStream(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_00_abc\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"input\\\":\\\"await tools.exec_command({cmd: 'hi'})\\\"}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	body := []byte(`{
		"model":"m","stream":true,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools":[{"type":"custom","name":"exec","description":"Run JS"}]
	}`)
	resp, data := chatPost(t, addr, "/c/codex/v1/responses", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	out := string(data)
	if !strings.Contains(out, "event: response.completed") {
		t.Fatalf("missing response.completed: %s", out)
	}
	if strings.Contains(out, "response.failed") || strings.Contains(out, "unknown tool call") {
		t.Fatalf("stream failed: %s", out)
	}
	if !strings.Contains(out, `"type":"custom_tool_call"`) || !strings.Contains(out, "await tools.exec_command({cmd: 'hi'})") {
		t.Fatalf("custom tool call missing: %s", out)
	}
}

// responsesToMessagesNonStream: responses 客户端 → messages 上游。
func TestCrossResponsesToMessagesNonStream(t *testing.T) {
	up := newFakeUpstream(t, messagesTextHandler(messagesTextBody))
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "anthropic"
	cfg.Providers["openrouter"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/responses",
		[]byte(`{"model":"m","input":"hi"}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "hello from messages") {
		t.Errorf("body = %s", data)
	}
	// 上游必须收到 Messages 请求（max_tokens）。
	reqs := up.requests()
	if len(reqs) != 1 || reqs[0].Fields["max_tokens"] == nil {
		t.Errorf("upstream should receive a Messages request: %+v", reqs)
	}
}

// messagesToResponsesNonStream: messages 客户端 → responses 上游。
func TestCrossMessagesToResponsesNonStream(t *testing.T) {
	up := newFakeUpstream(t, responsesTextHandler(responsesTextBody))
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "openai-responses"
	cfg.Providers["openrouter"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/messages",
		[]byte(`{"model":"m","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "hello from responses") {
		t.Errorf("body = %s", data)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["type"] != "message" {
		t.Errorf("type = %v, want message", doc["type"])
	}
}

// chatToMessagesStream: chat 客户端 → messages 上游，SSE 事件顺序与文本。
func TestCrossChatToMessagesStream(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		body := `event: message_start
data: {"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"m","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}

event: message_stop
data: {"type":"message_stop"}
`
		w.Write([]byte(body))
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "anthropic"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/chat/completions",
		[]byte(`{"model":"m","messages":[],"stream":true}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
	out := string(data)
	if !strings.Contains(out, "data: [DONE]") {
		t.Errorf("stream missing [DONE]: %q", out)
	}
	// 文本增量顺序。
	first := strings.Index(out, "Hel")
	second := strings.Index(out, "lo")
	if first < 0 || second < 0 || first > second {
		t.Errorf("text delta order wrong: %q", out)
	}
}

// responsesToChatStreamToolCall: responses 上游 SSE 工具调用 → chat 客户端
// 流式，验证 id 保持与参数拼接。
func TestCrossResponsesToChatStreamToolCall(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		body := `event: response.created
data: {"type":"response.created","response":{"id":"r"}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_abc","name":"get_weather","arguments":"","status":"in_progress"}}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"city\": \"Ber"}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"lin\"}"}

event: response.function_call_arguments.done
data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"city\": \"Berlin\"}"}

event: response.completed
data: {"type":"response.completed","response":{"id":"r"}}
`
		w.Write([]byte(body))
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/chat/completions",
		[]byte(`{"model":"m","messages":[],"stream":true}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	out := string(data)
	if !strings.Contains(out, `"id":"call_abc"`) {
		t.Errorf("tool call id not preserved: %q", out)
	}
	if !strings.Contains(out, `"name":"get_weather"`) {
		t.Errorf("tool name missing: %q", out)
	}
	// 参数 delta 按序拼接（chunk JSON 中 arguments 是转义字符串）。
	if !strings.Contains(out, `{\"city\": \"Ber`) || !strings.Contains(out, `lin\"}`) {
		t.Errorf("arguments deltas missing: %q", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Errorf("missing [DONE]: %q", out)
	}
}

// messagesToResponsesStream: messages 客户端 → responses 上游 SSE。
func TestCrossMessagesToResponsesStream(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		body := `event: response.created
data: {"type":"response.created","response":{"id":"r"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hola"}

event: response.output_text.done
data: {"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"hola"}

event: response.completed
data: {"type":"response.completed","response":{"id":"r"}}
`
		w.Write([]byte(body))
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/messages",
		[]byte(`{"model":"m","max_tokens":100,"messages":[],"stream":true}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	out := string(data)
	for _, want := range []string{"event: message_start", "event: content_block_delta", "hola", "event: message_stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("stream missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "message_delta") == false {
		t.Errorf("stream missing message_delta:\n%s", out)
	}
}

// TestCrossImageRejected: provider 未声明图片能力时返回 422，不达上游。
func TestCrossImageRejected(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	body := `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"https://x/i.png"}}]}]}`
	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(body), nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422, body %s", resp.StatusCode, data)
	}
	if len(up.requests()) != 0 {
		t.Error("image request reached the upstream")
	}
	// messages 侧同样拒绝。
	up2 := newFakeUpstream(t, nil)
	cfg2 := dataPlaneConfig(up2.URL, up2.URL, false)
	p2 := cfg2.Providers["ollama"]
	p2.Adapter = "openai-chat"
	cfg2.Providers["ollama"] = p2
	_, addr2 := startWithStore(t, cfg2, secret.NewMemStore())
	body2 := `{"model":"m","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}`
	resp2, data2 := chatPost(t, addr2, "/v1/messages", []byte(body2), nil)
	if resp2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("messages image: status %d, want 422, body %s", resp2.StatusCode, data2)
	}

	up3 := newFakeUpstream(t, nil)
	cfg3 := dataPlaneConfig(up3.URL, up3.URL, false)
	_, addr3 := startWithStore(t, cfg3, secret.NewMemStore())
	resp3, data3 := chatPost(t, addr3, "/v1/chat/completions", []byte(body), nil)
	if resp3.StatusCode != http.StatusUnprocessableEntity || len(up3.requests()) != 0 {
		t.Fatalf("same-protocol image: status %d, upstream=%d, body %s", resp3.StatusCode, len(up3.requests()), data3)
	}
}

func TestCrossImageSupportedPreservesURLAndBase64(t *testing.T) {
	up := newFakeUpstream(t, responsesTextHandler(responsesTextBody))
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	p.Capabilities.ImageInput = true
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	body := `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"x"},{"type":"image_url","image_url":{"url":"https://x/i.png"}}]}]}`
	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(body), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("URL image: status %d, body %s", resp.StatusCode, data)
	}
	input := up.last().Fields["input"]
	if !strings.Contains(string(input), `"type":"input_image"`) || !strings.Contains(string(input), `"image_url":"https://x/i.png"`) {
		t.Fatalf("responses input did not preserve URL image: %s", input)
	}

	up2 := newFakeUpstream(t, nil)
	cfg2 := dataPlaneConfig(up2.URL, up2.URL, false)
	p2 := cfg2.Providers["ollama"]
	p2.Adapter = "openai-chat"
	p2.Capabilities.ImageInput = true
	cfg2.Providers["ollama"] = p2
	_, addr2 := startWithStore(t, cfg2, secret.NewMemStore())
	body2 := `{"model":"m","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}`
	resp2, data2 := chatPost(t, addr2, "/v1/messages", []byte(body2), nil)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("base64 image: status %d, body %s", resp2.StatusCode, data2)
	}
	messagesField := up2.last().Fields["messages"]
	if !strings.Contains(string(messagesField), `data:image/png;base64,AAAA`) {
		t.Fatalf("chat input did not preserve base64 image: %s", messagesField)
	}
}

func TestReasoningPreservedBetweenOpenAIProtocols(t *testing.T) {
	up := newFakeUpstream(t, responsesTextHandler(responsesTextBody))
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	p.Capabilities.Reasoning = true
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	body := `{"model":"m","reasoning_effort":"high","messages":[{"role":"user","content":"solve"}]}`
	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(body), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if got := string(up.last().Fields["reasoning"]); !strings.Contains(got, `"effort":"high"`) {
		t.Fatalf("reasoning = %s", got)
	}
}

func TestReasoningDowngradeWritesWarning(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	// Generic provider keeps reasoning=false and uses the same Chat protocol.
	s, addr := startWithStore(t, cfg, secret.NewMemStore())
	body := `{"model":"m","reasoning_effort":"high","messages":[{"role":"user","content":"solve"}]}`
	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(body), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if _, ok := up.last().Fields["reasoning_effort"]; ok {
		t.Fatal("reasoning_effort reached a provider with reasoning disabled")
	}

	root := filepath.Dir(s.cfg.Path())
	files, err := filepath.Glob(filepath.Join(root, "logs", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("warning files = %v, err = %v", files, err)
	}
	warning, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(warning), `"type":"warning"`) || !strings.Contains(string(warning), `"code":"reasoning_dropped"`) {
		t.Fatalf("warning = %s", warning)
	}
}

func TestReasoningDowngradeWhenTargetProtocolCannotExpress(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-chat"
	p.Capabilities.Reasoning = true
	cfg.Providers["ollama"] = p
	s, addr := startWithStore(t, cfg, secret.NewMemStore())
	body := `{"model":"m","thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"user","content":"solve"}]}`
	resp, data := chatPost(t, addr, "/v1/messages", []byte(body), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if _, ok := up.last().Fields["reasoning_effort"]; ok {
		t.Fatal("Anthropic thinking was incorrectly converted to reasoning_effort")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(s.cfg.Path()), "logs", "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("warning files = %v", files)
	}
}

// TestCrossStreamBrokenUpstream: 上游 SSE 断流（无 completed）→ 客户端收到
// 错误事件而非伪造成功完成。
func TestCrossStreamBrokenUpstream(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		// 断流：无 [DONE]、无 completed。
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/chat/completions",
		[]byte(`{"model":"m","messages":[],"stream":true}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	out := string(data)
	if strings.Contains(out, "[DONE]") {
		t.Errorf("broken stream fabricated [DONE]: %q", out)
	}
	if !strings.Contains(out, "unexpectedly") {
		t.Errorf("broken stream should carry an error event: %q", out)
	}
}

// TestCrossStreamClientCancel: 跨协议流式请求，客户端断开 → 上游取消。
func TestCrossStreamClientCancel(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		// 上游是 responses 协议（跨协议管线按 outProto 解析）。
		io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\n")
		fl.Flush()
		<-r.Context().Done()
		close(upstreamCanceled)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_, _ = resp.Body.Read(buf)
	cancel()
	resp.Body.Close()
	select {
	case <-upstreamCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not cancelled after client disconnect")
	}
}

// TestCrossSameProtocolPassthroughResponses: responses 同协议直通保留未知字段。
func TestCrossSameProtocolPassthroughResponses(t *testing.T) {
	up := newFakeUpstream(t, responsesTextHandler(responsesTextBody))
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	body := `{"model":"m","input":"hi","temperature":0.7,"x-custom":{"a":1}}`
	resp, data := chatPost(t, addr, "/v1/responses", []byte(body), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "hello from responses") {
		t.Errorf("body = %s", data)
	}
	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatal("no upstream request")
	}
	// 未知字段原样保留。
	if string(reqs[0].Fields["x-custom"]) == "" {
		t.Error("x-custom lost")
	}
	if string(reqs[0].Fields["temperature"]) == "" {
		t.Error("temperature lost")
	}
}

// TestCrossSameProtocolPassthroughMessages: messages 同协议直通 + 认证注入。
func TestCrossSameProtocolPassthroughMessages(t *testing.T) {
	up := newFakeUpstream(t, messagesTextHandler(messagesTextBody))
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.ollama", []byte("sk-ant-key-1")); err != nil {
		t.Fatal(err)
	}
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "anthropic"
	p.SecretRef = "provider.ollama"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, store)

	resp, data := chatPost(t, addr, "/v1/messages",
		[]byte(`{"model":"gateway-default","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"x-custom":1}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatal("no upstream request")
	}
	// 同协议：未知字段保留 + 模型重写为路由默认（qwen3）。
	if reqs[0].Fields["x-custom"] == nil {
		t.Error("x-custom lost")
	}
	if string(reqs[0].Fields["model"]) != `"qwen3"` {
		t.Errorf("model = %s, want qwen3 (route rewrite)", reqs[0].Fields["model"])
	}
	if string(reqs[0].Fields["messages"]) == "" {
		t.Error("messages lost")
	}
}

// TestCrossConcurrentIsolation: 并发跨协议请求，认证与协议不串。
func TestCrossConcurrentIsolation(t *testing.T) {
	chatUp := newFakeUpstream(t, nil) // chat 默认 pong
	antUp := newFakeUpstream(t, messagesTextHandler(messagesTextBody))
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.openrouter", []byte("sk-chat-key")); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Providers["openrouter"] = config.Provider{
		Name: "OpenRouter", Adapter: "openai-chat",
		BaseURL: chatUp.URL, DefaultModel: "m1", SecretRef: "provider.openrouter",
	}
	cfg.Providers["ollama"] = config.Provider{
		Name: "Ollama", Adapter: "anthropic",
		BaseURL: antUp.URL, DefaultModel: "m2",
	}
	cfg.Routes.Codex = config.Route{Provider: "openrouter", Model: "m1"}
	cfg.Routes.Generic = config.Route{Provider: "ollama", Model: "m2"}
	_, addr := startWithStore(t, cfg, store)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				// codex → chat 上游（带 key）。
				resp, _ := chatPost(t, addr, "/c/codex/v1/chat/completions",
					[]byte(`{"model":"m","messages":[]}`), nil)
				if resp.StatusCode != http.StatusOK {
					t.Errorf("chat req %d: %d", i, resp.StatusCode)
				}
			} else {
				// generic → messages 上游（无 key）。
				resp, _ := chatPost(t, addr, "/v1/messages",
					[]byte(`{"model":"m","max_tokens":10,"messages":[]}`), nil)
				if resp.StatusCode != http.StatusOK {
					t.Errorf("messages req %d: %d", i, resp.StatusCode)
				}
			}
		}(i)
	}
	wg.Wait()
	for _, r := range chatUp.requests() {
		if r.Auth != "Bearer sk-chat-key" {
			t.Errorf("chat upstream auth = %q", r.Auth)
		}
	}
	for _, r := range antUp.requests() {
		if r.Auth != "" {
			t.Errorf("messages upstream auth = %q", r.Auth)
		}
	}
}

// TestCrossToolResultContinuation: 工具结果续轮（chat 请求含结果 → responses
// 上游收到 function_call_output）。
func TestCrossToolResultContinuation(t *testing.T) {
	up := newFakeUpstream(t, responsesTextHandler(responsesTextBody))
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["ollama"]
	p.Adapter = "openai-responses"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	body := `{"model":"m","messages":[
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_9","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]},
		{"role":"tool","tool_call_id":"call_9","content":"result text"}
	]}`
	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(body), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream requests = %d", len(reqs))
	}
	upBody, _ := json.Marshal(reqs[0].Fields)
	if !strings.Contains(string(upBody), `"type":"function_call_output"`) ||
		!strings.Contains(string(upBody), `"call_id":"call_9"`) ||
		!strings.Contains(string(upBody), "result text") {
		t.Errorf("upstream body lacks the tool result continuation:\\n%s", upBody)
	}
}
