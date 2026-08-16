package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

// fixture loads a desensitized protocol fixture from testdata/protocols/chat.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "protocols", "chat", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// fakeRequest is one request captured by a fake upstream.
type fakeRequest struct {
	Path      string
	Model     string
	Stream    bool
	Auth      string
	Accept    string
	ContentTy string
	UserAgent string
	Fields    map[string]json.RawMessage
}

// fakeUpstream is a configurable OpenAI-chat upstream that records every
// request it receives.
type fakeUpstream struct {
	t *testing.T
	*httptest.Server
	mu       sync.Mutex
	captured []fakeRequest
}

// newFakeUpstream starts an upstream that answers with handler (may be nil
// for a default echo). Captured requests can be inspected via requests().
func newFakeUpstream(t *testing.T, handler http.HandlerFunc) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{t: t}
	// Default: respond with the request's own model echoed as a plain
	// completion; streamed requests get an SSE single chunk.
	defaultHandler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(body, &fields)
		stream := false
		if raw, ok := fields["stream"]; ok {
			_ = json.Unmarshal(raw, &stream)
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "data: {\"id\":\"chatcmpl-fake\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"id":"chatcmpl-fake","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`, string(fields["model"]))
	}
	if handler == nil {
		handler = defaultHandler
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(body, &fields)
		var model string
		_ = json.Unmarshal(fields["model"], &model)
		stream := false
		if raw, ok := fields["stream"]; ok {
			_ = json.Unmarshal(raw, &stream)
		}
		f.mu.Lock()
		f.captured = append(f.captured, fakeRequest{
			Path:      r.URL.Path,
			Model:     model,
			Stream:    stream,
			Auth:      r.Header.Get("Authorization"),
			Accept:    r.Header.Get("Accept"),
			ContentTy: r.Header.Get("Content-Type"),
			UserAgent: r.Header.Get("User-Agent"),
			Fields:    fields,
		})
		f.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeUpstream) requests() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeRequest(nil), f.captured...)
}

func (f *fakeUpstream) last() fakeRequest {
	reqs := f.requests()
	if len(reqs) == 0 {
		f.t.Fatalf("fake upstream received no requests")
	}
	return reqs[len(reqs)-1]
}

// dataPlaneConfig builds a config with two providers pointing at the fake
// upstreams: openrouter (route target of codex/claude/grok, optionally keyed)
// and ollama (generic's route, keyless).
func dataPlaneConfig(up1, up2 string, withSecret bool) *config.Config {
	c := config.Defaults()
	c.Providers["openrouter"] = config.Provider{
		Name: "OpenRouter", Adapter: "openai-chat",
		BaseURL: up1, DefaultModel: "anthropic/claude-sonnet-4",
	}
	if withSecret {
		p := c.Providers["openrouter"]
		p.SecretRef = "provider.openrouter"
		c.Providers["openrouter"] = p
	}
	c.Providers["ollama"] = config.Provider{
		Name: "Ollama", Adapter: "openai-chat",
		BaseURL: up2, DefaultModel: "qwen3",
	}
	return c
}

// startDataPlane wires a gateway with fake upstreams. store may be nil for a
// fresh MemStore. Returns server, addr and the two fakes.
func startDataPlane(t *testing.T, up1, up2 string, withSecret bool, store secret.Store) (*Server, string) {
	t.Helper()
	if store == nil {
		store = secret.NewMemStore()
	}
	cfg := dataPlaneConfig(up1, up2, withSecret)
	s, addr := startWithStore(t, cfg, store)
	return s, addr
}

// chatPost sends a data-plane request with optional inbound headers.
func chatPost(t *testing.T, addr, path string, body []byte, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

func TestChatRoutingFourClientsIsolated(t *testing.T) {
	up1 := newFakeUpstream(t, nil)
	up2 := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up1.URL, up2.URL, false, nil)

	// 每个客户端经自己的路由到达对应 provider：codex/claude/grok →
	// openrouter(up1)，generic/无前缀 → ollama(up2)。默认模型互不影响。
	cases := []struct {
		path string
		up   *fakeUpstream
	}{
		{"/c/codex/v1/chat/completions", up1},
		{"/c/claude/v1/chat/completions", up1},
		{"/c/grok/v1/chat/completions", up1},
		{"/c/generic/v1/chat/completions", up2},
		{"/v1/chat/completions", up2}, // 无前缀等价 generic
	}
	for _, tc := range cases {
		resp, data := chatPost(t, addr, tc.path, []byte(`{"model":"gateway-default","messages":[{"role":"user","content":"hi"}]}`), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, body %s", tc.path, resp.StatusCode, data)
		}
		req := tc.up.last()
		wantModel := "anthropic/claude-sonnet-4"
		if tc.up == up2 {
			wantModel = "qwen3"
		}
		if req.Model != wantModel {
			t.Errorf("%s: upstream model = %q, want %q", tc.path, req.Model, wantModel)
		}
		if tc.up == up1 && len(up2.requests()) != 0 {
			t.Errorf("%s: request leaked to ollama", tc.path)
		}
	}
}

func TestChatRoutingEmptyModelUsesRouteModel(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)
	resp, _ := chatPost(t, addr, "/c/generic/v1/chat/completions", []byte(`{"messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := up.last().Model; got != "qwen3" {
		t.Errorf("empty model routed as %q, want qwen3", got)
	}
}

func TestChatRoutingPrefixOverrideAndPassthrough(t *testing.T) {
	up1 := newFakeUpstream(t, nil)
	up2 := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up1.URL, up2.URL, false, nil)

	// 前缀命中已配置 provider：openrouter/anthropic/claude-sonnet-4 从
	// generic 路由覆盖到 openrouter，模型剥离前缀一次。
	resp, data := chatPost(t, addr, "/c/generic/v1/chat/completions",
		[]byte(`{"model":"openrouter/anthropic/claude-sonnet-4","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("override: status %d, %s", resp.StatusCode, data)
	}
	req := up1.last()
	if req.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("override model = %q, want anthropic/claude-sonnet-4", req.Model)
	}

	// 前缀未命中（anthropic 不是 provider id）：模型完整交给当前 provider
	//（generic → ollama），即使含斜杠也不报未知供应商。
	resp, data = chatPost(t, addr, "/c/generic/v1/chat/completions",
		[]byte(`{"model":"anthropic/claude-sonnet-4","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("passthrough: status %d, %s", resp.StatusCode, data)
	}
	req = up2.last()
	if req.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("passthrough model = %q, want the full model on the route provider", req.Model)
	}
}

func TestChatUnknownClientAndPath404(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)
	body := []byte(`{"model":"m","messages":[]}`)

	for _, path := range []string{
		"/c/bogus/v1/chat/completions",
		"/c/CODEX/v1/chat/completions",
		"/c/desktop/v1/chat/completions",
	} {
		resp, data := chatPost(t, addr, path, body, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 (%s)", path, resp.StatusCode, data)
		}
	}
	// 未实现的协议路径 404；已实现端点的错误请求不算。
	for _, path := range []string{
		"/v1/responses/extra",
		"/v1/messages/extra",
		"/c/codex/v1/responses/extra",
		"/nope",
	} {
		resp, _ := chatPost(t, addr, path, body, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
	if len(up.requests()) != 0 {
		t.Error("404 paths still reached the upstream")
	}
}

func TestChatRequestValidation(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`not json`), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad json: status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "error") {
		t.Errorf("error shape missing: %s", data)
	}
	resp, _ = chatPost(t, addr, "/v1/chat/completions", []byte(`{"stream":"yes"}`), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad stream: status %d", resp.StatusCode)
	}
	// 超过 128 MiB → 413。
	big := []byte(`{"model":"m","name":"` + strings.Repeat("x", 129<<20) + `"}`)
	resp, data = chatPost(t, addr, "/v1/chat/completions", big, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("big body: status %d, %s", resp.StatusCode, data)
	}
	if len(up.requests()) != 0 {
		t.Error("invalid requests reached the upstream")
	}
}

func TestChatUnknownFieldsPreserved(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	body := fixture(t, "request.json") // 含 temperature/user/x-custom-field
	resp, data := chatPost(t, addr, "/v1/chat/completions", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	req := up.last()
	// gateway-default → 路由模型 qwen3。
	if req.Model != "qwen3" {
		t.Errorf("model = %q, want qwen3", req.Model)
	}
	if req.Stream {
		t.Error("stream rewritten to true")
	}
	// 未知字段的原始值必须原样保留（键序/空白不保证）。
	if string(req.Fields["x-custom-field"]) == "" {
		t.Error("x-custom-field lost")
	}
	var custom struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(req.Fields["x-custom-field"], &custom); err != nil || custom.Note == "" {
		t.Errorf("x-custom-field = %s", req.Fields["x-custom-field"])
	}
	var temp float64
	if err := json.Unmarshal(req.Fields["temperature"], &temp); err != nil || temp != 0.2 {
		t.Errorf("temperature = %v", temp)
	}
	if string(req.Fields["user"]) != `"fixture-user-1"` {
		t.Errorf("user = %s", req.Fields["user"])
	}
	if req.Fields["messages"] == nil {
		t.Error("messages lost")
	}
}

func TestMessagesDropsUnsupportedContextManagement(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "anthropic"
	p.DefaultModel = "claude-opus-4-6"
	p.Capabilities = config.Capabilities{Reasoning: true}
	cfg.Providers["openrouter"] = p
	s, addr := startWithStore(t, cfg, secret.NewMemStore())
	body := []byte(`{"model":"m","stream":true,"messages":[],"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}}`)
	resp, data := chatPost(t, addr, "/c/claude/v1/messages", body, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), "message_start") {
		t.Fatalf("response status=%d body=%s", resp.StatusCode, data)
	}
	req := up.last()
	if _, ok := req.Fields["context_management"]; ok {
		t.Fatalf("unsupported context_management reached upstream: %s", req.Fields["context_management"])
	}
	if _, ok := req.Fields["messages"]; !ok {
		t.Fatal("messages field was lost")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(s.cfg.Path()), "logs", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("warning files = %v, err = %v", files, err)
	}
	warning, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(warning), `"code":"context_management_dropped"`) {
		t.Fatalf("warning = %s", warning)
	}
}

func TestMessagesPreservesSupportedContextManagement(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\"}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "anthropic"
	p.DefaultModel = "claude-opus-4-6"
	p.Capabilities = config.Capabilities{Reasoning: true, ContextManagement: true}
	cfg.Providers["openrouter"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	body := []byte(`{"model":"m","stream":true,"messages":[],"context_management":{"edits":[]}}`)
	resp, _ := chatPost(t, addr, "/c/claude/v1/messages", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if _, ok := up.last().Fields["context_management"]; !ok {
		t.Fatal("supported context_management was removed")
	}
}

func TestChatInboundAuthDropped(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	headers := map[string]string{
		"Authorization":   "Bearer client-side-placeholder-key",
		"x-api-key":       "client-x-api-key-value",
		"X-Custom-Header": "must-not-forward",
	}
	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), headers)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	req := up.last()
	if req.Auth != "" {
		t.Errorf("inbound Authorization forwarded: %q", req.Auth)
	}
	if _, ok := req.Fields["x-api-key"]; ok {
		t.Error("x-api-key leaked into the upstream body")
	}
	if _, ok := req.Fields["X-Custom-Header"]; ok {
		t.Error("custom header leaked into the upstream body")
	}
}

func TestChatAuthInjectedFromKeyStore(t *testing.T) {
	up := newFakeUpstream(t, nil)
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.openrouter", []byte("sk-upstream-secret-1")); err != nil {
		t.Fatal(err)
	}
	_, addr := startDataPlane(t, up.URL, up.URL, true, store)

	// 有 secret_ref 的 provider（openrouter，经 codex 路由）：注入 Bearer。
	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if auth := up.last().Auth; auth != "Bearer sk-upstream-secret-1" {
		t.Errorf("Authorization = %q, want Bearer sk-upstream-secret-1", auth)
	}
	// 无钥匙的 provider（ollama，generic）：不发认证。
	resp, data = chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if auth := up.last().Auth; auth != "" {
		t.Errorf("keyless provider got Authorization %q", auth)
	}
	// 任何响应、错误都不含钥匙。
	if strings.Contains(string(data), "sk-upstream-secret-1") {
		t.Error("response leaked the key")
	}
}

func TestChatAuthMissingSecretFails(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, true, secret.NewMemStore()) // 空 store

	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500, body %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), "sk-") {
		t.Errorf("error leaked key material: %s", data)
	}
	if len(up.requests()) != 0 {
		t.Error("unauthenticated request reached the upstream")
	}
}

func TestChatNonStreamingForwarded(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Marker", "keep-or-drop")
		w.WriteHeader(http.StatusOK)
		w.Write(fixture(t, "response.json"))
	})
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(string(data), "One, two, three.") {
		t.Errorf("body not forwarded: %s", data)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	if x := resp.Header.Get("X-Upstream-Marker"); x != "" {
		t.Errorf("non-whitelisted header forwarded: %q", x)
	}
}

func TestChatUpstreamErrorsPreserved(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		fixture string
	}{
		{"upstream 400", http.StatusBadRequest, "error-400.json"},
		{"upstream 500", http.StatusInternalServerError, "error-500.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				w.Write(fixture(t, tc.fixture))
			})
			_, addr := startDataPlane(t, up.URL, up.URL, false, nil)
			resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if !strings.Contains(string(data), "error") {
				t.Errorf("upstream error body not preserved: %s", data)
			}
		})
	}
}

func TestChatUpstreamUnreachable502(t *testing.T) {
	// 未监听端口 → 连接拒绝 → 502。走 codex 路由（→ openrouter → up1）。
	dead := "http://127.0.0.1:1"
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, dead, up.URL, false, nil)
	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "error") {
		t.Errorf("error shape missing: %s", data)
	}
}

func TestChatUpstreamTimeout504(t *testing.T) {
	slow := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // 超过测试用的响应头超时
		w.WriteHeader(http.StatusOK)
	})
	up := newFakeUpstream(t, nil)
	s, addr := startDataPlane(t, slow.URL, up.URL, false, nil)
	s.upstreamsChat.SetResponseHeaderTimeout(200 * time.Millisecond)

	start := time.Now()
	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504, body %s", resp.StatusCode, data)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("504 took too long: %v", time.Since(start))
	}
}

// TestChatStreamingFlushIsPrompt verifies real-time SSE forwarding: the
// upstream flushes chunk 1 immediately, waits, then flushes chunk 2. A
// gateway that buffers the whole response would deliver chunk 1 only after
// the wait; the gateway must deliver it almost immediately.
func TestChatStreamingFlushIsPrompt(t *testing.T) {
	const wait = 600 * time.Millisecond
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		fl.Flush()
		time.Sleep(wait)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"second\"}}]}\n\n")
		fl.Flush()
	})
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	start := time.Now()
	var firstAt time.Time
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 && firstAt.IsZero() {
			firstAt = time.Now()
		}
		if rerr != nil {
			break
		}
	}
	if firstAt.IsZero() {
		t.Fatal("no data received")
	}
	elapsed := firstAt.Sub(start)
	if elapsed > wait/2 {
		t.Errorf("first chunk arrived after %v; the gateway buffered the stream instead of flushing", elapsed)
	}
}

// TestChatStreamingClientCancelCancelsUpstream verifies that a client
// disconnect propagates: the upstream request context must be cancelled
// shortly after the client hangs up (docs/v1-scheme.md §9.4).
func TestChatStreamingClientCancelCancelsUpstream(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		io.WriteString(w, "data: first\n\n")
		fl.Flush()
		<-r.Context().Done()
		close(upstreamCanceled)
	})
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("first read: %v", err)
	}
	// 客户端断开。
	cancel()
	resp.Body.Close()

	select {
	case <-upstreamCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream context was not cancelled after client disconnect")
	}
}

func TestChatConcurrentNoCrossTalk(t *testing.T) {
	// 两个带各自认证的 provider；并发请求必须不串路由、不串认证。
	upA := newFakeUpstream(t, nil)
	upB := newFakeUpstream(t, nil)
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.openrouter", []byte("sk-secret-A")); err != nil {
		t.Fatal(err)
	}
	// B 用不同 ref。
	if err := store.Put(context.Background(), "provider.ollama", []byte("sk-secret-B")); err != nil {
		t.Fatal(err)
	}
	cfg := dataPlaneConfig(upA.URL, upB.URL, true)
	p := cfg.Providers["ollama"]
	p.SecretRef = "provider.ollama"
	cfg.Providers["ollama"] = p
	s, addr := startWithStore(t, cfg, store)

	_ = s
	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := "/v1/chat/completions"
			if i%2 == 0 {
				path = "/c/codex/v1/chat/completions" // → A, Bearer sk-secret-A
			}
			resp, _ := chatPost(t, addr, path, []byte(`{"model":"m","messages":[]}`), nil)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("request %d: status %d", i, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()

	for _, r := range upA.requests() {
		if r.Auth != "Bearer sk-secret-A" {
			t.Errorf("upstream A got auth %q", r.Auth)
		}
	}
	for _, r := range upB.requests() {
		if r.Auth != "Bearer sk-secret-B" {
			t.Errorf("upstream B got auth %q", r.Auth)
		}
	}
}

func TestModelsList(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	for _, path := range []string{"/v1/models", "/c/codex/v1/models", "/c/generic/v1/models"} {
		resp, data := chatGet(t, addr, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, %s", path, resp.StatusCode, data)
		}
		var list struct {
			Object string `json:"object"`
			Data   []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if list.Object != "list" || len(list.Data) != 3 {
			t.Fatalf("%s: %s", path, data)
		}
		want := []string{
			"gateway-default",
			"ollama/qwen3",
			"openrouter/anthropic/claude-sonnet-4",
		}
		for i, w := range want {
			if list.Data[i].ID != w {
				t.Errorf("%s: data[%d].id = %q, want %q", path, i, list.Data[i].ID, w)
			}
			// §7.5: display_name equals the selectable id so client pickers
			// show <provider-id>/<model-id> (or gateway-default).
			if list.Data[i].DisplayName != w {
				t.Errorf("%s: data[%d].display_name = %q, want %q", path, i, list.Data[i].DisplayName, w)
			}
		}
	}
	// 非法客户端前缀 → 404。
	resp, _ := chatGet(t, addr, "/c/bogus/v1/models")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bogus models client: status %d, want 404", resp.StatusCode)
	}
}

func TestClaudeModelsListUsesPickerAliases(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)
	resp, data := chatGet(t, addr, "/c/claude/v1/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	var list struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []struct{ id, display string }{
		{"claude-gw-default", "gateway-default"},
		{"claude-gw-ollama--qwen3", "ollama/qwen3"},
		{"claude-gw2-openrouter--anthropic~sclaude-sonnet-4", "openrouter/anthropic/claude-sonnet-4"},
	}
	if len(list.Data) != len(want) {
		t.Fatalf("len=%d body=%s", len(list.Data), data)
	}
	for i, w := range want {
		if list.Data[i].ID != w.id {
			t.Errorf("data[%d].id = %q, want %q", i, list.Data[i].ID, w.id)
		}
		if list.Data[i].DisplayName != w.display {
			t.Errorf("data[%d].display_name = %q, want %q", i, list.Data[i].DisplayName, w.display)
		}
	}
	if strings.Contains(string(data), `"id":"gateway-default"`) {
		t.Fatal("claude catalog leaked the unaliased reserved id")
	}
	if strings.Contains(string(data), `"id":"ollama/qwen3"`) {
		t.Fatal("claude catalog leaked an unaliased provider/model id")
	}
}

func TestClaudePickerAliasRoutesToUpstream(t *testing.T) {
	up1 := newFakeUpstream(t, nil)
	up2 := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up1.URL, up2.URL, false, nil)

	resp, data := chatPost(t, addr, "/c/claude/v1/chat/completions",
		[]byte(`{"model":"claude-gw-ollama--qwen3","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ollama alias: status %d, %s", resp.StatusCode, data)
	}
	if got := up2.last().Model; got != "qwen3" {
		t.Errorf("ollama alias upstream model = %q, want qwen3", got)
	}
	if len(up1.requests()) != 0 {
		t.Fatalf("ollama alias leaked to openrouter: %+v", up1.requests())
	}

	resp, data = chatPost(t, addr, "/c/claude/v1/chat/completions",
		[]byte(`{"model":"claude-gw2-openrouter--anthropic~sclaude-sonnet-4","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openrouter alias: status %d, %s", resp.StatusCode, data)
	}
	if got := up1.last().Model; got != "anthropic/claude-sonnet-4" {
		t.Errorf("openrouter alias upstream model = %q, want anthropic/claude-sonnet-4", got)
	}

	resp, data = chatPost(t, addr, "/c/claude/v1/chat/completions",
		[]byte(`{"model":"claude-gw-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("default alias: status %d, %s", resp.StatusCode, data)
	}
	if got := up1.last().Model; got != "anthropic/claude-sonnet-4" {
		t.Errorf("default alias upstream model = %q, want route model", got)
	}

	// 真实 id 仍然可直接请求，不强制走别名。
	resp, data = chatPost(t, addr, "/c/claude/v1/chat/completions",
		[]byte(`{"model":"openrouter/anthropic/claude-sonnet-4","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("raw id: status %d, %s", resp.StatusCode, data)
	}
	if got := up1.last().Model; got != "anthropic/claude-sonnet-4" {
		t.Errorf("raw id upstream model = %q", got)
	}
}

func TestModelsListIncludesPersistedProviderCatalog(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Models = []config.ProviderModel{
		{ID: p.DefaultModel, Name: "Claude Sonnet", ContextWindow: 200000, MaxOutputTokens: 64000},
		{ID: "openai/gpt-5", Name: "GPT-5", ContextWindow: 400000, MaxOutputTokens: 128000},
	}
	cfg.Providers["openrouter"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	resp, body := chatGet(t, addr, "/v1/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("models: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"id":"openrouter/openai/gpt-5"`) {
		t.Fatalf("persisted model catalog missing: %s", body)
	}
	if strings.Count(string(body), `"id":"openrouter/anthropic/claude-sonnet-4"`) != 1 {
		t.Fatalf("default model was duplicated: %s", body)
	}
	// §7.5: display_name is the selectable id, not the catalog friendly name.
	if !strings.Contains(string(body), `"display_name":"openrouter/openai/gpt-5"`) {
		t.Fatalf("selectable id missing from display_name: %s", body)
	}
	if strings.Contains(string(body), `"display_name":"GPT-5"`) {
		t.Fatalf("catalog friendly name leaked into client display_name: %s", body)
	}
}

func chatGet(t *testing.T, addr, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

// TestChatStreamingUsesFixture ensures the streamed response path works end
// to end with the desensitized SSE fixture, preserving its events.

func TestChatToMessagesCrossProtocolDispatch(t *testing.T) {
	// chat 入站 + adapter=anthropic 的 provider：D 包起走跨协议 IR 管线，
	// 上游必须收到 Messages 协议外形（system/messages/max_tokens），绝不
	// 是 chat/completions 结构（docs/v1-scheme.md §8.3）。
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_fake","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`))
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "anthropic"
	cfg.Providers["openrouter"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions",
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "pong") {
		t.Errorf("converted response missing text: %s", data)
	}
	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream requests = %d", len(reqs))
	}
	// 上游收到的必须是 Messages 请求：有 max_tokens 与 messages 块结构。
	if _, ok := reqs[0].Fields["max_tokens"]; !ok {
		t.Errorf("upstream body is not a Messages request: %v", reqs[0].Fields)
	}
	if string(reqs[0].Fields["messages"]) == "" {
		t.Error("messages field missing")
	}
}

func TestChatContentTypeValidation(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	// 缺失或非 JSON Content-Type → 415 原生客户端错误。
	for _, ct := range []string{"", "text/plain", "application/xml", "multipart/form-data"} {
		resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), map[string]string{"Content-Type": ct})
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: status %d, want 415 (%s)", ct, resp.StatusCode, data)
			continue
		}
		if !strings.Contains(string(data), "error") {
			t.Errorf("Content-Type %q: error shape missing: %s", ct, data)
		}
	}
	// 合法 JSON media type 通过：带参数与 +json 后缀。
	for _, ct := range []string{"application/json", "application/json; charset=utf-8", "application/problem+json"} {
		resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), map[string]string{"Content-Type": ct})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Content-Type %q: status %d, want 200 (%s)", ct, resp.StatusCode, data)
		}
	}
	// 被拒请求不得到达上游。
	if n := len(up.requests()); n != 3 {
		t.Errorf("upstream requests = %d, want 3 (only the valid ones)", n)
	}
}

func TestChatSecretStoreErrorIsInternal(t *testing.T) {
	// key store 不可用是内部/配置错误，映射 500 而非 502 upstream
	// unreachable，且错误不得泄漏密钥。
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, true) // openrouter 声明 secret_ref
	_, addr := startWithStore(t, cfg, brokenStore{})

	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), "sk-") || strings.Contains(string(data), "Bearer") {
		t.Errorf("error leaked key material: %s", data)
	}
	if len(up.requests()) != 0 {
		t.Error("request reached the upstream despite the secret store failure")
	}
}

func TestResponsesCompactForwardsSameProtocol(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"response.compaction","output":[{"type":"compaction"}]}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	p := cfg.Providers["openrouter"]
	p.Adapter = "openai-responses"
	cfg.Providers["openrouter"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/responses/compact", []byte(`{"model":"gateway-default","input":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), `"object":"response.compaction"`) {
		t.Fatalf("compact response = %s", data)
	}
	req := up.last()
	if req.Path != "/responses/compact" {
		t.Fatalf("upstream path = %q, want /responses/compact", req.Path)
	}
	if req.Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("upstream model = %q", req.Model)
	}
	if req.Accept != "application/json" {
		t.Fatalf("upstream Accept = %q", req.Accept)
	}
}

func TestResponsesCompactRejectsNonResponsesAdapter(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)
	resp, data := chatPost(t, addr, "/c/codex/v1/responses/compact", []byte(`{"model":"gateway-default","input":[]}`), nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "openai-responses") {
		t.Fatalf("error = %s", data)
	}
	if len(up.requests()) != 0 {
		t.Fatal("compact request reached a non-responses upstream")
	}
}

func TestChatForwardedHeadersWhitelist(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.Header().Set("X-Request-Id", "reqid-abc123")
		w.Header().Set("openai-request-id", "reqid-lowercase")
		w.Header().Set("Set-Cookie", "session=evil")
		w.Header().Set("Authorization", "Bearer upstream-internal")
		w.Header().Set("X-Upstream-Secret", "sk-internal")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"slow down"}}`))
	})
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	resp, _ := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	for _, want := range []string{"Retry-After", "X-Request-Id", "OpenAI-Request-ID"} {
		if resp.Header.Get(want) == "" {
			t.Errorf("whitelisted header %s not forwarded", want)
		}
	}
	if ra := resp.Header.Get("Retry-After"); ra != "30" {
		t.Errorf("Retry-After = %q, want 30", ra)
	}
	// 大小写不敏感地按 Go 规范名读取（openai-request-id → OpenAI-Request-ID）。
	if id := resp.Header.Get("OpenAI-Request-ID"); id != "reqid-lowercase" {
		t.Errorf("OpenAI-Request-ID = %q, want reqid-lowercase", id)
	}
	for _, forbidden := range []string{"Set-Cookie", "Authorization", "X-Upstream-Secret"} {
		if v := resp.Header.Get(forbidden); v != "" {
			t.Errorf("forbidden header %s forwarded: %q", forbidden, v)
		}
	}
}

func TestChatRedirectForwardedNotFollowed(t *testing.T) {
	// 上游 302 + Location：状态与 Location 透传给客户端；第二个目标绝不
	// 收到携带 secret 的请求。
	target := newFakeUpstream(t, nil)
	redirector := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+"/v1/chat/completions")
		w.WriteHeader(http.StatusFound)
	})
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.openrouter", []byte("sk-redirect-secret")); err != nil {
		t.Fatal(err)
	}
	_, addr := startDataPlane(t, redirector.URL, redirector.URL, true, store)

	// 客户端自身不得跟随重定向：要断言的是网关透传的 302 与 Location。
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/c/codex/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302, body %s", resp.StatusCode, data)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, target.URL) {
		t.Errorf("Location = %q, want the redirect target URL", loc)
	}
	if n := len(target.requests()); n != 0 {
		t.Errorf("redirect target received %d requests (secret would leak): %v", n, target.requests())
	}
}

func TestChatAcceptHeaderByStreaming(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[]}`), nil)
	if a := up.last().Accept; a != "application/json" {
		t.Errorf("non-streaming Accept = %q, want application/json", a)
	}
	chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"m","messages":[],"stream":true}`), nil)
	if a := up.last().Accept; a != "text/event-stream" {
		t.Errorf("streaming Accept = %q, want text/event-stream", a)
	}
}

func TestChatEmptyRestAfterProviderPrefix(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	// 命中 provider 前缀但模型为空：明确的字段错误，空模型绝不上游。
	resp, data := chatPost(t, addr, "/c/generic/v1/chat/completions",
		[]byte(`{"model":"openrouter/","messages":[]}`), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "model") {
		t.Errorf("error should name the model field: %s", data)
	}
	if len(up.requests()) != 0 {
		t.Error("empty model reached the upstream")
	}
}

func TestChatStreamingUsesFixture(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(fixture(t, "response-stream.txt"))
	})
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{"data: [DONE]", "chatcmpl-fake-stream-1", "One", "three"} {
		if !strings.Contains(body, want) {
			t.Errorf("streamed body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "sk-") {
		t.Error("streamed body contains key material")
	}
}
