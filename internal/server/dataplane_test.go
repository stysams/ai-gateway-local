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
	"ai-gateway/internal/route"
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


// setProviderKeyGroup mutates one key group under a provider.
func setProviderKeyGroup(cfg *config.Config, providerID, keyID string, mutate func(*config.KeyGroup)) {
	provider := cfg.Providers[providerID]
	if provider.KeyGroups == nil {
		provider.KeyGroups = map[string]config.KeyGroup{}
	}
	group := provider.KeyGroups[keyID]
	mutate(&group)
	provider.KeyGroups[keyID] = group
	cfg.Providers[providerID] = provider
}

// setKeyGroupWire configures the key-group adapter and matching default endpoint.
func setKeyGroupWire(cfg *config.Config, providerID, keyID, adapter string) {
	setProviderKeyGroup(cfg, providerID, keyID, func(group *config.KeyGroup) {
		group.Adapter = adapter
		switch adapter {
		case "openai-chat":
			group.Endpoint = "/chat/completions"
		case "openai-responses":
			group.Endpoint = "/responses"
		case "anthropic":
			group.Endpoint = "/messages"
		}
	})
}

// setKeyGroupAPIKey sets or clears the plaintext key for one key group.
func setKeyGroupAPIKey(cfg *config.Config, providerID, keyID, apiKey string) {
	setProviderKeyGroup(cfg, providerID, keyID, func(group *config.KeyGroup) {
		group.APIKey = apiKey
	})
}

// setKeyGroupModels replaces the model catalog and default model of one key group.
// Routes that still point at this provider/key but no longer name an enabled
// model are rewritten to the new default so Validate stays green.
func setKeyGroupModels(cfg *config.Config, providerID, keyID, defaultModel string, models []config.ProviderModel) {
	setProviderKeyGroup(cfg, providerID, keyID, func(group *config.KeyGroup) {
		if defaultModel != "" {
			group.DefaultModel = defaultModel
		}
		group.Models = models
	})
	group := cfg.Providers[providerID].KeyGroups[keyID]
	enabled := map[string]bool{}
	for _, model := range group.Models {
		if model.EnabledValue() {
			enabled[model.ID] = true
		}
	}
	repair := func(route *config.Route) {
		if route.Provider != providerID || route.KeyID != keyID {
			return
		}
		if !enabled[route.Model] {
			route.Model = group.DefaultModel
		}
	}
	repair(&cfg.Routes.Codex)
	repair(&cfg.Routes.Claude)
	repair(&cfg.Routes.ClaudeDesktop)
	repair(&cfg.Routes.Grok)
	repair(&cfg.Routes.Generic)
}

// setRoute sets a client route to a provider/key-group/model triple.
func setRoute(cfg *config.Config, client, providerID, keyID, model string) {
	routeValue := config.Route{Provider: providerID, KeyID: keyID, Model: model}
	switch client {
	case "codex":
		cfg.Routes.Codex = routeValue
	case "claude":
		cfg.Routes.Claude = routeValue
	case "claude_desktop", "claude-desktop":
		cfg.Routes.ClaudeDesktop = routeValue
	case "grok":
		cfg.Routes.Grok = routeValue
	case "generic":
		cfg.Routes.Generic = routeValue
	default:
		panic("unknown client " + client)
	}
}

// dataPlaneConfig builds a config with two providers pointing at the fake
// upstreams and the current provider/key-group contract.
func dataPlaneConfig(up1, up2 string, withSecret bool) *config.Config {
	c := config.Defaults()
	if !strings.HasSuffix(strings.TrimRight(up1, "/"), "/v1") {
		up1 = strings.TrimRight(up1, "/") + "/v1"
	}
	if !strings.HasSuffix(strings.TrimRight(up2, "/"), "/v1") {
		up2 = strings.TrimRight(up2, "/") + "/v1"
	}
	openrouterKey := ""
	if withSecret {
		openrouterKey = "sk-openrouter"
	}
	c.Providers["openrouter"] = config.Provider{Name: "OpenRouter", BaseURL: up1, KeyGroups: map[string]config.KeyGroup{
		"default": {Name: "Default", Adapter: "openai-chat", Endpoint: "/chat/completions", DefaultModel: "anthropic/claude-sonnet-4", APIKey: openrouterKey, Models: []config.ProviderModel{
			{ID: "anthropic/claude-sonnet-4"}, {ID: "route-model-codex"}, {ID: "route-model-claude"}, {ID: "route-model-grok"}, {ID: "route-model-generic"}, {ID: "enabled-model"}, {ID: "disabled-model", Enabled: config.BoolPtr(false)},
		}},
	}}
	// ollama stays keyless unless a test explicitly sets an API key.
	c.Providers["ollama"] = config.Provider{Name: "Ollama", BaseURL: up2, KeyGroups: map[string]config.KeyGroup{
		"default": {Name: "Default", Adapter: "openai-chat", Endpoint: "/chat/completions", DefaultModel: "qwen3", Models: []config.ProviderModel{{ID: "qwen3"}}},
	}}
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

func TestChatRoutingPrefixOverrideModelOwnerAndUnmatched(t *testing.T) {
	up1 := newFakeUpstream(t, nil)
	up2 := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up1.URL, up2.URL, false, nil)

	// 前缀命中已配置 provider：openrouter/anthropic/claude-sonnet-4 从
	// generic 路由覆盖到 openrouter，模型剥离前缀一次。
	resp, data := chatPost(t, addr, "/c/generic/v1/chat/completions",
		[]byte(`{"model":"openrouter/default/anthropic/claude-sonnet-4","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("override: status %d, %s", resp.StatusCode, data)
	}
	req := up1.last()
	if req.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("override model = %q, want anthropic/claude-sonnet-4", req.Model)
	}

	// 前缀未命中，但完整模型名由 openrouter 唯一登记：generic 请求仍应
	// 按模型归属到达 openrouter，且不得剥离模型名中的 anthropic/。
	resp, data = chatPost(t, addr, "/c/generic/v1/chat/completions",
		[]byte(`{"model":"anthropic/claude-sonnet-4","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("model owner: status %d, %s", resp.StatusCode, data)
	}
	req = up1.last()
	if req.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("owned model = %q, want the full model on the owning provider", req.Model)
	}

	// 未命中 provider 前缀、也未被任何 provider 登记的模型不得透传，
	// 必须在接触上游之前返回明确的选择错误。
	beforeUnknown := len(up2.requests())
	resp, data = chatPost(t, addr, "/c/generic/v1/chat/completions",
		[]byte(`{"model":"vendor/unknown-model","messages":[]}`), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unmatched: status %d, %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), route.UnmatchedModelMessage("vendor/unknown-model")) {
		t.Fatalf("unmatched body = %s", data)
	}
	if got := len(up2.requests()); got != beforeUnknown {
		t.Fatalf("unmatched model reached generic upstream: %d requests, want %d", got, beforeUnknown)
	}
}

func TestMessagesGenericRejectsUnattributedModel(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	resp, data := chatPost(t, addr, "/v1/messages",
		[]byte(`{"model":"claude-opus-5","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), route.UnmatchedModelMessage("claude-opus-5")) {
		t.Fatalf("body = %s", data)
	}
	if got := len(up.requests()); got != 0 {
		t.Fatalf("unmatched model reached upstream %d times", got)
	}
}

func TestMessagesMergesInboundAnthropicBeta(t *testing.T) {
	var got http.Header
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"qwen3","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	provider := cfg.Providers["ollama"]
	provider.ExtraHeaders = map[string]string{
		"Anthropic-Beta": "claude-code-20250219,effort-2025-11-24",
		"User-Agent":     "claude-cli/2.1.228 (external, cli)",
	}
	cfg.Providers["ollama"] = provider
	setKeyGroupWire(cfg, "ollama", "default", "anthropic")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/messages",
		[]byte(`{"model":"qwen3","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`),
		map[string]string{
			"Anthropic-Beta":           "claude-code-20250219,context-1m-2025-08-07,effort-2025-11-24",
			"X-Claude-Code-Session-Id": "sess-should-not-forward",
			"Authorization":            "Bearer inbound-must-not-leak",
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if got.Get("Anthropic-Beta") != "claude-code-20250219,effort-2025-11-24,context-1m-2025-08-07" {
		t.Fatalf("upstream Anthropic-Beta = %q", got.Get("Anthropic-Beta"))
	}
	if got.Get("User-Agent") != "claude-cli/2.1.228 (external, cli)" {
		t.Fatalf("upstream User-Agent = %q", got.Get("User-Agent"))
	}
	if got.Get("X-Claude-Code-Session-Id") != "" {
		t.Fatal("inbound session header was forwarded")
	}
	if strings.Contains(got.Get("Authorization"), "inbound-must-not-leak") {
		t.Fatal("inbound authorization was forwarded")
	}
}

func TestGenericDisguiseAppliesClaudeHeaders(t *testing.T) {
	var got http.Header
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"qwen3","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	provider := cfg.Providers["ollama"]
	provider.DisguiseClient = config.DisguiseClientClaude
	cfg.Providers["ollama"] = provider
	setKeyGroupWire(cfg, "ollama", "default", "anthropic")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/messages",
		[]byte(`{"model":"qwen3","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`),
		map[string]string{"Anthropic-Beta": "context-1m-2025-08-07"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if got.Get("User-Agent") != "claude-cli/2.1.228 (external, cli)" {
		t.Fatalf("User-Agent = %q", got.Get("User-Agent"))
	}
	if got.Get("X-App") != "cli" {
		t.Fatalf("X-App = %q", got.Get("X-App"))
	}
	if got.Get("Anthropic-Dangerous-Direct-Browser-Access") != "true" {
		t.Fatalf("browser-access = %q", got.Get("Anthropic-Dangerous-Direct-Browser-Access"))
	}
	if got.Get("Anthropic-Beta") != claudeDisguiseHeaders["Anthropic-Beta"]+",context-1m-2025-08-07" {
		t.Fatalf("Anthropic-Beta = %q", got.Get("Anthropic-Beta"))
	}
}

func TestGenericClaudeDisguiseAppliesThinkingAndSystemCache(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"qwen3","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	provider := cfg.Providers["ollama"]
	provider.DisguiseClient = config.DisguiseClientClaude
	provider.Capabilities = config.Capabilities{Reasoning: true}
	cfg.Providers["ollama"] = provider
	setKeyGroupWire(cfg, "ollama", "default", "anthropic")
	s, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/messages",
		[]byte(`{"model":"qwen3","max_tokens":32,"output_config":{"effort":"medium"},"system":[{"type":"text","text":"You are Ally."}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"tools":[{"name":"read","input_schema":{"type":"object"}}]}`),
		nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	req := up.last()
	if string(req.Fields["thinking"]) != `{"type":"adaptive"}` {
		t.Fatalf("thinking = %s", req.Fields["thinking"])
	}
	if string(req.Fields["output_config"]) != `{"effort":"medium"}` {
		t.Fatalf("output_config rewritten: %s", req.Fields["output_config"])
	}
	var system []map[string]json.RawMessage
	if err := json.Unmarshal(req.Fields["system"], &system); err != nil {
		t.Fatal(err)
	}
	if len(system) != 1 || string(system[0]["text"]) != `"You are Ally."` {
		t.Fatalf("system text changed: %s", req.Fields["system"])
	}
	if string(system[0]["cache_control"]) != `{"type":"ephemeral"}` {
		t.Fatalf("system cache = %s", system[0]["cache_control"])
	}
	if !strings.Contains(string(req.Fields["tools"]), `"read"`) {
		t.Fatalf("tools lost: %s", req.Fields["tools"])
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(s.cfg.Path()), "logs", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("warning files = %v, err = %v", files, err)
	}
	warning, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(warning), `"code":"claude_disguise_applied"`) {
		t.Fatalf("warning = %s", warning)
	}
}

func TestFirstClassClientSkipsDisguise(t *testing.T) {
	var got http.Header
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"anthropic/claude-sonnet-4","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	provider := cfg.Providers["openrouter"]
	provider.DisguiseClient = config.DisguiseClientClaude
	provider.Capabilities = config.Capabilities{Reasoning: true}
	cfg.Providers["openrouter"] = provider
	setKeyGroupWire(cfg, "openrouter", "default", "anthropic")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/claude/v1/messages",
		[]byte(`{"model":"gateway-default","max_tokens":32,"system":[{"type":"text","text":"Be brief."}],"messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if got.Get("User-Agent") != "ai-gateway" {
		t.Fatalf("first-class User-Agent = %q, want adapter default", got.Get("User-Agent"))
	}
	if got.Get("X-App") != "" {
		t.Fatalf("first-class received disguise X-App = %q", got.Get("X-App"))
	}
	req := up.last()
	if _, ok := req.Fields["thinking"]; ok {
		t.Fatalf("first-class received disguise thinking: %s", req.Fields["thinking"])
	}
	if strings.Contains(string(req.Fields["system"]), "cache_control") {
		t.Fatalf("first-class received disguise cache_control: %s", req.Fields["system"])
	}
}

func TestGenericDisguiseCodexHeaders(t *testing.T) {
	var got http.Header
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	provider := cfg.Providers["ollama"]
	provider.DisguiseClient = config.DisguiseClientCodex
	cfg.Providers["ollama"] = provider
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/chat/completions",
		[]byte(`{"model":"qwen3","messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if got.Get("User-Agent") != "codex_cli_rs/0.147.0" || got.Get("Originator") != "codex_cli_rs" {
		t.Fatalf("codex disguise = ua %q originator %q", got.Get("User-Agent"), got.Get("Originator"))
	}
}

func TestGenericDisguisePiHeaders(t *testing.T) {
	var got http.Header
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	provider := cfg.Providers["ollama"]
	provider.DisguiseClient = config.DisguiseClientPi
	cfg.Providers["ollama"] = provider
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/chat/completions",
		[]byte(`{"model":"qwen3","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true},"store":false}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	if got.Get("User-Agent") != "Pi Agent/1.0" {
		t.Fatalf("Pi disguise User-Agent = %q", got.Get("User-Agent"))
	}
}

func TestMessagesGenericRoutesUniqueModelToOwningProvider(t *testing.T) {
	agentrouter := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-opus-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	opencode := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(agentrouter.URL, opencode.URL, false)
	cfg.Providers["agentrouter"] = config.Provider{
		Name: "AgentRouter", Adapter: "anthropic", BaseURL: agentrouter.URL,
		DefaultModel: "claude-opus-5",
		Models:       []config.ProviderModel{{ID: "claude-opus-5"}},
	}
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/v1/messages",
		[]byte(`{"model":"claude-opus-5","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %s", resp.StatusCode, data)
	}
	request := agentrouter.last()
	if request.Path != "/v1/messages" || request.Model != "claude-opus-5" {
		t.Errorf("agentrouter request = %+v", request)
	}
	if len(opencode.requests()) != 0 {
		t.Fatal("unique agentrouter model was sent to the generic route provider")
	}
}

func TestGenericModelOwnerConvertsOpenAIProtocolsToProviderAdapter(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		body       string
		responseID string
	}{
		{
			name: "chat completions", path: "/v1/chat/completions",
			body:       `{"model":"claude-opus-5","messages":[{"role":"user","content":"hi"}]}`,
			responseID: `"object":"chat.completion"`,
		},
		{
			name: "responses", path: "/v1/responses",
			body:       `{"model":"claude-opus-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			responseID: `"object":"response"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentrouter := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-opus-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			})
			opencode := newFakeUpstream(t, nil)
			cfg := dataPlaneConfig(agentrouter.URL, opencode.URL, false)
			cfg.Providers["agentrouter"] = config.Provider{
				Name: "AgentRouter", Adapter: "anthropic", BaseURL: agentrouter.URL,
				DefaultModel: "claude-opus-5",
				Models:       []config.ProviderModel{{ID: "claude-opus-5"}},
			}
			_, addr := startWithStore(t, cfg, secret.NewMemStore())

			resp, data := chatPost(t, addr, tc.path, []byte(tc.body), nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d, body %s", resp.StatusCode, data)
			}
			if !strings.Contains(string(data), tc.responseID) {
				t.Errorf("response was not converted back to the inbound protocol: %s", data)
			}
			request := agentrouter.last()
			if request.Path != "/v1/messages" || request.Model != "claude-opus-5" {
				t.Errorf("agentrouter request = %+v", request)
			}
			if _, ok := request.Fields["max_tokens"]; !ok {
				t.Errorf("upstream body is not Anthropic Messages: %v", request.Fields)
			}
			if len(opencode.requests()) != 0 {
				t.Fatal("request reached the generic route provider instead of the model owner")
			}
			requestID := resp.Header.Get("X-Request-Id")
			_, detail := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/"+requestID, nil)
			logText := string(detail)
			if !strings.Contains(logText, `"outbound_protocol":"messages"`) ||
				!strings.Contains(logText, `"converted":true`) {
				t.Errorf("route log did not record protocol conversion: %s", detail)
			}
		})
	}
}

func TestChatUnknownClientAndPath404(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)
	body := []byte(`{"model":"gateway-default","messages":[]}`)

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
	big := []byte(`{"model":"gateway-default","name":"` + strings.Repeat("x", 129<<20) + `"}`)
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
	p.Capabilities = config.Capabilities{Reasoning: true}
	cfg.Providers["openrouter"] = p
	setKeyGroupWire(cfg, "openrouter", "default", "anthropic")
	setKeyGroupModels(cfg, "openrouter", "default", "claude-opus-4-6", []config.ProviderModel{
		{ID: "claude-opus-4-6", Endpoint: "/messages"},
	})
	s, addr := startWithStore(t, cfg, secret.NewMemStore())
	body := []byte(`{"model":"gateway-default","stream":true,"messages":[],"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}}`)
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
	p.Capabilities = config.Capabilities{Reasoning: true, ContextManagement: true}
	cfg.Providers["openrouter"] = p
	setKeyGroupWire(cfg, "openrouter", "default", "anthropic")
	setKeyGroupModels(cfg, "openrouter", "default", "claude-opus-4-6", []config.ProviderModel{
		{ID: "claude-opus-4-6", Endpoint: "/messages"},
	})
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	body := []byte(`{"model":"gateway-default","stream":true,"messages":[],"context_management":{"edits":[]}}`)
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
	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), headers)
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
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	setKeyGroupAPIKey(cfg, "openrouter", "default", "sk-upstream-secret-1")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	// 有 api_key 的密钥组（openrouter，经 codex 路由）：注入 Bearer。
	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, %s", resp.StatusCode, data)
	}
	if auth := up.last().Auth; auth != "Bearer sk-upstream-secret-1" {
		t.Errorf("Authorization = %q, want Bearer sk-upstream-secret-1", auth)
	}
	// 无钥匙的密钥组（ollama，generic）：不发认证。
	resp, data = chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
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
	// Current contract: empty api_key is keyless and may still reach upstream.
	// Missing credentials are a readiness concern, not a hard data-plane 500.
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	setKeyGroupAPIKey(cfg, "openrouter", "default", "")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200 for keyless key group, body %s", resp.StatusCode, data)
	}
	if auth := up.last().Auth; auth != "" {
		t.Errorf("keyless key group got Authorization %q", auth)
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

	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
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
			resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
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
	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
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
	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
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
		strings.NewReader(`{"model":"gateway-default","messages":[],"stream":true}`))
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
		strings.NewReader(`{"model":"gateway-default","messages":[],"stream":true}`))
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

func TestDataPlaneConcurrencyLimitRejectsImmediatelyAndReleases(t *testing.T) {
	started := make(chan struct{})
	releaseUpstream := make(chan struct{})
	var startedOnce sync.Once
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-releaseUpstream
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-limit","object":"chat.completion","choices":[]}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	cfg.Limits = config.Limits{Global: 1, PerClient: 1, PerProvider: 1, StreamIdleSeconds: 300}
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	firstDone := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Post("http://"+addr+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"gateway-default","messages":[]}`))
		if err != nil {
			t.Errorf("first request: %v", err)
			return
		}
		firstDone <- resp
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not reach upstream")
	}

	second, body := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second status=%d, body=%s; want 429", second.StatusCode, body)
	}
	if !strings.Contains(string(body), "concurrency_limit") {
		t.Fatalf("second error missing concurrency_limit: %s", body)
	}
	close(releaseUpstream)
	first := <-firstDone
	first.Body.Close()

	third, thirdBody := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if third.StatusCode != http.StatusOK {
		t.Fatalf("third status=%d, body=%s; slot was not released", third.StatusCode, thirdBody)
	}
}

func TestDataPlaneRequestLimitsRejectBeforeUpstream(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	cfg.Limits.RequestBodyBytes = 32
	cfg.Limits.RequestHeaderBytes = 256
	cfg.Limits.StreamIdleSeconds = 300
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	tooLarge, body := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[{"role":"user","content":"this request is larger than thirty-two bytes"}]}`), nil)
	if tooLarge.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(body), "request_too_large") {
		t.Fatalf("body limit status=%d body=%s", tooLarge.StatusCode, body)
	}
	if len(up.requests()) != 0 {
		t.Fatal("oversized body reached upstream")
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions", strings.NewReader(`{"model":"gateway-default","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Large", strings.Repeat("x", 512))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	headerBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge || !strings.Contains(string(headerBody), "request_headers_too_large") {
		t.Fatalf("header limit status=%d body=%s", resp.StatusCode, headerBody)
	}
	if len(up.requests()) != 0 {
		t.Fatal("oversized headers reached upstream")
	}
}

func TestDataPlaneClientRateLimitRejectsBeforeUpstream(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	cfg.Limits.ClientRatePerMinute = 1
	cfg.Limits.StreamIdleSeconds = 300
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	first, firstBody := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.StatusCode, firstBody)
	}
	second, secondBody := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if second.StatusCode != http.StatusTooManyRequests || !strings.Contains(string(secondBody), "client_rate_limit") {
		t.Fatalf("second status=%d body=%s; want client rate 429", second.StatusCode, secondBody)
	}
	if got := len(up.requests()); got != 1 {
		t.Fatalf("rate-limited request reached upstream: %d requests", got)
	}
}

func TestDataPlaneStreamIdleTimeoutReleasesSlot(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	cfg.Limits.Global = 1
	cfg.Limits.StreamIdleSeconds = 1
	server, addr := startWithStore(t, cfg, secret.NewMemStore())

	started := time.Now()
	resp, err := http.Post("http://"+addr+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"gateway-default","stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if elapsed := time.Since(started); elapsed < 700*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("stream idle timeout elapsed=%s, want about one second", elapsed)
	}
	if active := server.limiter.activeCount(); active != 0 {
		t.Fatalf("active slot not released after idle timeout: %d", active)
	}
}

func TestChatConcurrentNoCrossTalk(t *testing.T) {
	// 两个带各自认证的密钥组；并发请求必须不串路由、不串认证。
	upA := newFakeUpstream(t, nil)
	upB := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(upA.URL, upB.URL, false)
	setKeyGroupAPIKey(cfg, "openrouter", "default", "sk-secret-A")
	setKeyGroupAPIKey(cfg, "ollama", "default", "sk-secret-B")
	s, addr := startWithStore(t, cfg, secret.NewMemStore())

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
			resp, _ := chatPost(t, addr, path, []byte(`{"model":"gateway-default","messages":[]}`), nil)
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
		if list.Object != "list" || len(list.Data) < 3 {
			t.Fatalf("%s: %s", path, data)
		}
		want := []string{
			"gateway-default",
			"ollama/default/qwen3",
			"openrouter/default/anthropic/claude-sonnet-4",
		}
		gotIDs := make([]string, 0, len(list.Data))
		for _, item := range list.Data {
			gotIDs = append(gotIDs, item.ID)
			if item.DisplayName != item.ID {
				t.Errorf("%s: display_name %q != id %q", path, item.DisplayName, item.ID)
			}
		}
		joined := strings.Join(gotIDs, ",")
		for _, w := range want {
			if !strings.Contains(joined, w) {
				t.Errorf("%s: missing model %q in %v", path, w, gotIDs)
			}
		}
		if strings.Contains(joined, "disabled-model") {
			t.Errorf("%s: disabled model leaked: %v", path, gotIDs)
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
		{"claude-gw2-ollama--default~sqwen3", "ollama/default/qwen3"},
		{"claude-gw2-openrouter--default~santhropic~sclaude-sonnet-4", "openrouter/default/anthropic/claude-sonnet-4"},
	}
	byDisplay := map[string]string{}
	for _, item := range list.Data {
		byDisplay[item.DisplayName] = item.ID
	}
	for _, w := range want {
		if byDisplay[w.display] != w.id {
			t.Errorf("display %q -> id %q, want %q (body=%s)", w.display, byDisplay[w.display], w.id, data)
		}
	}
	if strings.Contains(string(data), `"id":"gateway-default"`) {
		t.Fatal("claude catalog leaked the unaliased reserved id")
	}
	if strings.Contains(string(data), `"id":"ollama/default/qwen3"`) {
		t.Fatal("claude catalog leaked an unaliased provider/model id")
	}
}

func TestClaudePickerAliasRoutesToUpstream(t *testing.T) {
	up1 := newFakeUpstream(t, nil)
	up2 := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up1.URL, up2.URL, false, nil)

	resp, data := chatPost(t, addr, "/c/claude/v1/chat/completions",
		[]byte(`{"model":"claude-gw2-ollama--default~sqwen3","messages":[]}`), nil)
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
		[]byte(`{"model":"claude-gw2-openrouter--default~santhropic~sclaude-sonnet-4","messages":[]}`), nil)
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
		[]byte(`{"model":"openrouter/default/anthropic/claude-sonnet-4","messages":[]}`), nil)
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
	setKeyGroupModels(cfg, "openrouter", "default", "anthropic/claude-sonnet-4", []config.ProviderModel{
		{ID: "anthropic/claude-sonnet-4", Name: "Claude Sonnet", ContextWindow: 200000, MaxOutputTokens: 64000, Endpoint: "/chat/completions"},
		{ID: "openai/gpt-5", Name: "GPT-5", ContextWindow: 400000, MaxOutputTokens: 128000, Endpoint: "/chat/completions"},
	})
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	resp, body := chatGet(t, addr, "/v1/models")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("models: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"id":"openrouter/default/openai/gpt-5"`) {
		t.Fatalf("persisted model catalog missing: %s", body)
	}
	if strings.Count(string(body), `"id":"openrouter/default/anthropic/claude-sonnet-4"`) != 1 {
		t.Fatalf("default model was duplicated: %s", body)
	}
	// §7.5: display_name is the selectable id, not the catalog friendly name.
	if !strings.Contains(string(body), `"display_name":"openrouter/default/openai/gpt-5"`) {
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
		w.Write([]byte(`{"id":"msg_fake","type":"message","role":"assistant","model":"gateway-default","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}`))
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	setKeyGroupWire(cfg, "openrouter", "default", "anthropic")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions",
		[]byte(`{"model":"gateway-default","messages":[{"role":"user","content":"hi"}]}`), nil)
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

func TestModelAdapterSelectsOutboundProtocol(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if strings.Contains(r.URL.Path, "/messages") {
			io.WriteString(w, `{"id":"msg_fake","type":"message","role":"assistant","model":"claude-opus","content":[{"type":"text","text":"claude"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			return
		}
		fmt.Fprintf(w, `{"id":"chatcmpl-fake","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"gpt"},"finish_reason":"stop"}]}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	setKeyGroupWire(cfg, "openrouter", "default", "openai-chat")
	setKeyGroupModels(cfg, "openrouter", "default", "gpt-4o", []config.ProviderModel{
		{ID: "gpt-4o", Adapter: "openai-chat", Endpoint: "/chat/completions"},
		{ID: "claude-opus", Adapter: "anthropic", Endpoint: "/messages"},
	})
	setRoute(cfg, "codex", "openrouter", "default", "gpt-4o")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions",
		[]byte(`{"model":"openrouter/default/gpt-4o","messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gpt status = %d, body %s", resp.StatusCode, data)
	}
	resp, data = chatPost(t, addr, "/c/codex/v1/chat/completions",
		[]byte(`{"model":"openrouter/default/claude-opus","messages":[{"role":"user","content":"hi"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claude status = %d, body %s", resp.StatusCode, data)
	}
	reqs := up.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(reqs))
	}
	if reqs[0].Path != "/v1/chat/completions" {
		t.Fatalf("gpt path = %q, want /v1/chat/completions", reqs[0].Path)
	}
	if _, ok := reqs[0].Fields["messages"]; !ok {
		t.Fatalf("gpt upstream is not chat: %v", reqs[0].Fields)
	}
	if reqs[1].Path != "/v1/messages" {
		t.Fatalf("claude path = %q, want /v1/messages", reqs[1].Path)
	}
	if _, ok := reqs[1].Fields["max_tokens"]; !ok {
		t.Fatalf("claude upstream is not messages: %v", reqs[1].Fields)
	}
}

func TestCustomModelEndpointIsUsedAsWritten(t *testing.T) {
	up := newFakeUpstream(t, nil)
	// Keep the provider base URL without a trailing /v1 so the custom path is
	// appended exactly as written (no auto /v1 insertion by endpoint.Join).
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	provider := cfg.Providers["openrouter"]
	provider.BaseURL = strings.TrimRight(up.URL, "/")
	cfg.Providers["openrouter"] = provider
	setKeyGroupWire(cfg, "openrouter", "default", "anthropic")
	setKeyGroupModels(cfg, "openrouter", "default", "legacy-gpt", []config.ProviderModel{
		{ID: "legacy-gpt", Adapter: "custom", Endpoint: "/responses"},
	})
	setRoute(cfg, "codex", "openrouter", "default", "legacy-gpt")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/responses",
		[]byte(`{"model":"openrouter/default/legacy-gpt","input":[{"role":"user","content":"hi"}],"stream":false}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, data)
	}
	if got := up.last().Path; got != "/responses" {
		t.Fatalf("custom path = %q, want /responses without auto /v1", got)
	}
}

func TestChatContentTypeValidation(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startDataPlane(t, up.URL, up.URL, false, nil)

	// 缺失或非 JSON Content-Type → 415 原生客户端错误。
	for _, ct := range []string{"", "text/plain", "application/xml", "multipart/form-data"} {
		resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), map[string]string{"Content-Type": ct})
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
		resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), map[string]string{"Content-Type": ct})
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
	// Key groups no longer consult the system key store on the data plane.
	// A broken store must not block a keyless or plaintext-key request.
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	setKeyGroupAPIKey(cfg, "openrouter", "default", "sk-plain")
	_, addr := startWithStore(t, cfg, brokenStore{})

	resp, data := chatPost(t, addr, "/c/codex/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", resp.StatusCode, data)
	}
	if auth := up.last().Auth; auth != "Bearer sk-plain" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestResponsesCompactForwardsSameProtocol(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"response.compaction","output":[{"type":"compaction"}]}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	setKeyGroupWire(cfg, "openrouter", "default", "openai-responses")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/responses/compact", []byte(`{"model":"gateway-default","input":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), `"object":"response.compaction"`) {
		t.Fatalf("compact response = %s", data)
	}
	req := up.last()
	if req.Path != "/v1/responses/compact" {
		t.Fatalf("upstream path = %q, want /v1/responses/compact", req.Path)
	}
	if req.Model != "anthropic/claude-sonnet-4" {
		t.Fatalf("upstream model = %q", req.Model)
	}
	if req.Accept != "application/json" {
		t.Fatalf("upstream Accept = %q", req.Accept)
	}
}

func TestResponsesCompactUsesModelAdapter(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"response.compaction","output":[{"type":"compaction"}]}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	setKeyGroupWire(cfg, "openrouter", "default", "openai-chat")
	setKeyGroupModels(cfg, "openrouter", "default", "gpt-5", []config.ProviderModel{
		{ID: "gpt-5", Adapter: "openai-responses", Endpoint: "/responses"},
		{ID: "claude-opus", Adapter: "anthropic", Endpoint: "/messages"},
	})
	setRoute(cfg, "codex", "openrouter", "default", "gpt-5")
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, data := chatPost(t, addr, "/c/codex/v1/responses/compact", []byte(`{"model":"gateway-default","input":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body %s", resp.StatusCode, data)
	}
	if up.last().Path != "/v1/responses/compact" {
		t.Fatalf("upstream path = %q", up.last().Path)
	}

	resp, data = chatPost(t, addr, "/c/codex/v1/responses/compact", []byte(`{"model":"openrouter/default/claude-opus","input":[]}`), nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("claude compact status = %d, want 422, body %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), "openai-responses") {
		t.Fatalf("error = %s", data)
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

	resp, _ := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
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
		strings.NewReader(`{"model":"gateway-default","messages":[]}`))
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

	chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if a := up.last().Accept; a != "application/json" {
		t.Errorf("non-streaming Accept = %q, want application/json", a)
	}
	chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[],"stream":true}`), nil)
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
		strings.NewReader(`{"model":"gateway-default","messages":[],"stream":true}`))
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
