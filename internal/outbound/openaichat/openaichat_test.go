package openaichat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ai-gateway/internal/ir"
	"ai-gateway/internal/secret"
)

func TestCompletionURL(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		// base_url 以 /v1 结尾：直接拼 /chat/completions，不重复 /v1。
		{"https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1/chat/completions"},
		{"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1/chat/completions"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		// base_url 不带 /v1：也拼在根路径（预设语义，不做字符串猜测）。
		{"https://api.deepseek.com", "https://api.deepseek.com/chat/completions"},
		{"https://api.x.ai", "https://api.x.ai/chat/completions"},
		// 尾斜杠必须去掉，不能出现重复斜杠。
		{"https://example.com/v1/", "https://example.com/v1/chat/completions"},
		{"https://example.com/", "https://example.com/chat/completions"},
		{"https://example.com", "https://example.com/chat/completions"},
	}
	for _, tc := range cases {
		if got := CompletionURL(tc.base); got != tc.want {
			t.Errorf("CompletionURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
	for _, u := range []string{
		"https://openrouter.ai/api/v1/chat/completions",
		"http://127.0.0.1:11434/v1/chat/completions",
		"https://api.deepseek.com/chat/completions",
	} {
		if strings.Contains(u, "//chat") || strings.Contains(u, "/v1/v1") {
			t.Errorf("URL shape violation: %q", u)
		}
	}
}

func TestGenerateRequestImageAndReasoning(t *testing.T) {
	req := &ir.Request{
		Model:     "m",
		Reasoning: ir.ReasoningConfig{Enabled: true, Effort: "high", Source: ir.ProtocolResponses},
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.Block{
			{Type: ir.BlockText, Text: "look"},
			{Type: ir.BlockImage, Image: &ir.Image{Base64: "AAAA", MediaType: "image/png", Detail: "high"}},
		}}},
	}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %v", doc["reasoning_effort"])
	}
	messages := doc["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	imageURL := content[1].(map[string]any)["image_url"].(map[string]any)
	if imageURL["url"] != "data:image/png;base64,AAAA" || imageURL["detail"] != "high" {
		t.Fatalf("image_url = %v", imageURL)
	}
}

func TestGenerateRequestToolCallArgumentsAreJSONString(t *testing.T) {
	// IR stores tool arguments as raw JSON. Chat Completions requires
	// function.arguments to be a JSON string of that payload, not an object.
	wrapped := ir.WrapFreeformInput(`{"cmd": "Get-ChildItem Env:"}`)
	req := &ir.Request{
		Model: "m",
		Messages: []ir.Message{{
			Role: ir.RoleAssistant,
			Content: []ir.Block{{
				Type: ir.BlockToolCall,
				ToolCall: &ir.ToolCall{
					ID:        "call_00_abc",
					Name:      "exec",
					Arguments: wrapped,
					Custom:    true,
				},
			}},
		}},
	}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Messages []struct {
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Messages) != 1 || len(doc.Messages[0].ToolCalls) != 1 {
		t.Fatalf("body = %s", body)
	}
	raw := doc.Messages[0].ToolCalls[0].Function.Arguments
	if len(raw) == 0 || raw[0] != '"' {
		t.Fatalf("arguments wire type is not a JSON string: %s", raw)
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err != nil {
		t.Fatal(err)
	}
	if asString != string(wrapped) {
		t.Fatalf("arguments = %s, want %s", asString, wrapped)
	}
	if doc.Messages[0].ToolCalls[0].Function.Name != "exec" {
		t.Fatalf("name = %s", doc.Messages[0].ToolCalls[0].Function.Name)
	}
}

func TestGenerateRequestToolResultsFollowToolCalls(t *testing.T) {
	// Responses history can put dropped-reasoning leftovers and later
	// assistant text between a tool call and its result. Chat Completions
	// requires the tool message to follow the tool_calls message immediately.
	req := &ir.Request{
		Model: "m",
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.Block{{Type: ir.BlockText, Text: "你不是deepseek吗"}}},
			{Role: ir.RoleAssistant, Content: []ir.Block{{
				Type: ir.BlockToolCall,
				ToolCall: &ir.ToolCall{
					ID:        "call_00_abc",
					Name:      "exec_command",
					Arguments: json.RawMessage(`{"cmd":"Get-ChildItem Env:"}`),
				},
			}}},
			{Role: ir.RoleAssistant, Content: nil},
			{Role: ir.RoleAssistant, Content: []ir.Block{{Type: ir.BlockText, Text: "模型ID：Codex"}}},
			{Role: ir.RoleTool, Content: []ir.Block{{
				Type:       ir.BlockToolResult,
				ToolResult: &ir.ToolResult{ID: "call_00_abc", Content: "ok"},
			}}},
		},
	}
	body, err := GenerateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Messages) != 4 {
		t.Fatalf("messages = %+v", doc.Messages)
	}
	if doc.Messages[0].Role != "user" || doc.Messages[0].Content != "你不是deepseek吗" {
		t.Fatalf("user = %+v", doc.Messages[0])
	}
	if doc.Messages[1].Role != "assistant" || len(doc.Messages[1].ToolCalls) != 1 || doc.Messages[1].ToolCalls[0].ID != "call_00_abc" {
		t.Fatalf("tool_calls = %+v", doc.Messages[1])
	}
	if doc.Messages[2].Role != "tool" || doc.Messages[2].ToolCallID != "call_00_abc" || doc.Messages[2].Content != "ok" {
		t.Fatalf("tool = %+v", doc.Messages[2])
	}
	if doc.Messages[3].Role != "assistant" || doc.Messages[3].Content != "模型ID：Codex" {
		t.Fatalf("assistant text = %+v", doc.Messages[3])
	}
}

func TestParseResponseReasoning(t *testing.T) {
	events, err := ParseResponse([]byte(`{"id":"c1","choices":[{"message":{"role":"assistant","reasoning_content":"considering","content":"answer"},"finish_reason":"stop"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 5 || events[1].Type != ir.EventReasoningDelta || events[1].Text != "considering" {
		t.Fatalf("events = %+v", events)
	}
}

func TestStreamReaderReasoning(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"step \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"one\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
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
	if resp.Reasoning != "step one" || resp.Text != "answer" || !resp.Completed {
		t.Fatalf("response = %+v", resp)
	}
}

func TestStreamReaderToolCallIndexDeltas(t *testing.T) {
	// Official Chat Completions stream: first fragment has id+name,
	// later fragments only have index + argument delta.
	stream := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_00_abc\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"input\\\":\\\"await tools.exec_command({cmd: 'hi'})\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	sr := NewStreamReader(strings.NewReader(stream))
	seq := ir.NewSequencer()
	var types []ir.EventType
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
		types = append(types, ev.Type)
		if ev.Type == ir.EventToolCallArgumentsDlt && ev.ToolCallID != "call_00_abc" {
			t.Fatalf("delta id = %q", ev.ToolCallID)
		}
	}
	resp := seq.Accumulate()
	if !resp.Completed || len(resp.ToolCalls) != 1 {
		t.Fatalf("response = %+v events=%v", resp, types)
	}
	if resp.ToolCalls[0].ID != "call_00_abc" || resp.ToolCalls[0].Name != "exec" {
		t.Fatalf("tool = %+v", resp.ToolCalls[0])
	}
	if ir.UnwrapFreeformInput(resp.ToolCalls[0].Arguments) != "await tools.exec_command({cmd: 'hi'})" {
		t.Fatalf("arguments = %s", resp.ToolCalls[0].Arguments)
	}
}

// trackingStore observes zeroing of the key bytes returned by Get.
type trackingStore struct {
	inner secret.Store
	mu    sync.Mutex
	got   [][]byte
}

func (s *trackingStore) Get(ctx context.Context, ref string) ([]byte, error) {
	b, err := s.inner.Get(ctx, ref)
	if err == nil {
		s.mu.Lock()
		s.got = append(s.got, b)
		s.mu.Unlock()
	}
	return b, err
}

func (s *trackingStore) Put(ctx context.Context, ref string, v []byte) error {
	return s.inner.Put(ctx, ref, v)
}
func (s *trackingStore) Delete(ctx context.Context, ref string) error {
	return s.inner.Delete(ctx, ref)
}
func (s *trackingStore) Available(ctx context.Context) error {
	return s.inner.Available(ctx)
}

func (s *trackingStore) allZeroed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.got {
		for _, v := range b {
			if v != 0 {
				return false
			}
		}
	}
	return true
}

// capture is a fake upstream that records the request it receives.
type capture struct {
	t *testing.T
	*httptest.Server
	mu        sync.Mutex
	gotAuth   string
	gotBody   string
	gotCT     string
	gotAccept string
	gotUserUA string
	headerSet int
	status    int
	response  string
}

func newCapture(t *testing.T, status int, response string) *capture {
	t.Helper()
	c := &capture{t: t, status: status, response: response}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		c.mu.Lock()
		c.gotAuth = r.Header.Get("Authorization")
		c.gotBody = string(body)
		c.gotCT = r.Header.Get("Content-Type")
		c.gotAccept = r.Header.Get("Accept")
		c.gotUserUA = r.Header.Get("User-Agent")
		c.headerSet = len(r.Header)
		c.mu.Unlock()
		w.WriteHeader(status)
		io.WriteString(w, response)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *capture) snapshot() (auth, body, ct, accept, ua string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gotAuth, c.gotBody, c.gotCT, c.gotAccept, c.gotUserUA
}

func TestDoSendsOnlyAdapterHeadersAndInjectsAuth(t *testing.T) {
	ctx := context.Background()
	store := &trackingStore{inner: secret.NewMemStore()}
	if err := store.Put(ctx, "provider.openrouter", []byte("sk-secret-abc")); err != nil {
		t.Fatal(err)
	}
	up := newCapture(t, http.StatusOK, `{"id":"ok"}`)

	pool := NewPool(store)
	client := pool.Client(up.URL+"/v1", "provider.openrouter")
	resp, err := client.Do(ctx, []byte(`{"model":"m"}`), false)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"id":"ok"}` {
		t.Errorf("body = %s", body)
	}

	auth, gotBody, ct, accept, ua := up.snapshot()
	if !strings.HasPrefix(gotBody, `{"model":"m"}`) {
		t.Errorf("upstream body = %q", gotBody)
	}
	if auth != "Bearer sk-secret-abc" {
		t.Errorf("Authorization = %q", auth)
	}
	if ct != "application/json" || accept != "application/json" {
		t.Errorf("Content-Type=%q Accept=%q", ct, accept)
	}
	if ua == "" || ua == "Go-http-client/1.1" {
		t.Errorf("User-Agent = %q, want explicit gateway UA", ua)
	}
	// Only the adapter-required headers may be present (Content-Type,
	// Accept, User-Agent, Authorization, plus Go's implicit Host etc. —
	// nothing else).
	if up.headerSet > 6 {
		t.Errorf("upstream received %d headers, want only adapter-required ones", up.headerSet)
	}
	// The key bytes handed to the adapter must be zeroed after use.
	if !store.allZeroed() {
		t.Error("secret bytes were not zeroed after building the header")
	}
}

func TestDoWithoutSecretSendsNoAuth(t *testing.T) {
	up := newCapture(t, http.StatusOK, "{}")
	pool := NewPool(nil) // no key store at all
	client := pool.Client(up.URL, "")
	resp, err := client.Do(context.Background(), []byte(`{}`), false)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	auth, _, _, _, _ := up.snapshot()
	if auth != "" {
		t.Errorf("Authorization = %q, want none for keyless provider", auth)
	}
}

func TestDoMissingSecretFails(t *testing.T) {
	store := secret.NewMemStore() // empty: ref has no secret
	pool := NewPool(store)
	client := pool.Client("http://127.0.0.1:1", "provider.missing")
	_, err := client.Do(context.Background(), []byte(`{}`), false)
	if !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("err = %v, want ErrSecretMissing", err)
	}
	if strings.Contains(err.Error(), "sk-") {
		t.Errorf("error leaks key material: %v", err)
	}
}

func TestDoForwardsStatusAndBody(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusOK, `{"choices":[]}`},
		{http.StatusBadRequest, `{"error":{"message":"bad","type":"invalid_request_error"}}`},
		{http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`},
		{http.StatusInternalServerError, `{"error":{"message":"boom"}}`},
	} {
		up := newCapture(t, tc.status, tc.body)
		pool := NewPool(nil)
		client := pool.Client(up.URL, "")
		resp, err := client.Do(context.Background(), []byte(`{}`), false)
		if err != nil {
			t.Fatalf("status %d: %v", tc.status, err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.status || string(got) != tc.body {
			t.Errorf("status %d: got %d %q", tc.status, resp.StatusCode, got)
		}
	}
}

func TestDoAcceptHeaderByStreaming(t *testing.T) {
	up := newCapture(t, http.StatusOK, "{}")
	pool := NewPool(nil)
	client := pool.Client(up.URL, "")

	if _, err := client.Do(context.Background(), []byte(`{}`), true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, accept, _ := up.snapshot(); accept != "text/event-stream" {
		t.Errorf("streaming Accept = %q, want text/event-stream", accept)
	}

	if _, err := client.Do(context.Background(), []byte(`{}`), false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, accept, _ := up.snapshot(); accept != "application/json" {
		t.Errorf("non-streaming Accept = %q, want application/json", accept)
	}
}

// brokenStore returns ErrUnavailable for every operation.
type brokenStore struct{ secret.Store }

func (brokenStore) Available(context.Context) error { return secret.ErrUnavailable }
func (brokenStore) Get(context.Context, string) ([]byte, error) {
	return nil, secret.ErrUnavailable
}
func (brokenStore) Put(context.Context, string, []byte) error { return secret.ErrUnavailable }
func (brokenStore) Delete(context.Context, string) error      { return secret.ErrUnavailable }

func TestDoSecretStoreError(t *testing.T) {
	// A broken key store is an internal/config problem: the error must wrap
	// ErrSecretStore, never look like an upstream transport failure.
	pool := NewPool(brokenStore{})
	client := pool.Client("http://127.0.0.1:1", "provider.x")
	_, err := client.Do(context.Background(), []byte(`{}`), false)
	if !errors.Is(err, ErrSecretStore) {
		t.Fatalf("err = %v, want ErrSecretStore", err)
	}
	if strings.Contains(err.Error(), "sk-") || strings.Contains(err.Error(), "Bearer") {
		t.Errorf("error leaked key material: %v", err)
	}
}

func TestDoDoesNotFollowRedirect(t *testing.T) {
	// The provider Authorization header must never leak to a redirect
	// target: redirects are returned to the caller, not followed.
	leak := newCapture(t, http.StatusOK, `{"leaked":true}`)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", leak.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirector.Close()

	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.x", []byte("sk-redirect-secret")); err != nil {
		t.Fatal(err)
	}
	pool := NewPool(store)
	client := pool.Client(redirector.URL, "provider.x")
	resp, err := client.Do(context.Background(), []byte(`{}`), false)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect must not be followed)", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != leak.URL {
		t.Errorf("Location = %q, want %q", loc, leak.URL)
	}
	// The second target must never have seen the request (nor the secret).
	if len(leak.requests()) != 0 {
		t.Errorf("redirect target received %d requests; secret would leak", len(leak.requests()))
	}
}

// requests returns the number of captured request bodies (redirect-leak
// assertion helper).
func (c *capture) requests() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, 1)
	if c.gotBody != "" {
		out = append(out, c.gotBody)
	}
	return out
}

func TestPoolClientsShareTransport(t *testing.T) {
	// Two clients from one pool must share the underlying http.Client and
	// therefore the connection pool; a fresh pool per client would not.
	pool := NewPool(nil)
	a := pool.Client("https://a.example", "")
	b := pool.Client("https://b.example", "")
	if a.http != b.http {
		t.Error("clients of one pool do not share the transport")
	}
}
