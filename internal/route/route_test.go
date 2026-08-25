package route

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"ai-gateway/internal/config"
)

// testConfig builds a two-provider config: openrouter (route target of the
// three first-class clients) and ollama (generic's route). It mirrors the
// default config shape.
func testConfig() *config.Config {
	c := config.Defaults()
	c.Providers["openrouter"] = config.Provider{
		Name: "OpenRouter", Adapter: "openai-chat",
		BaseURL: "https://openrouter.ai/api/v1", DefaultModel: "anthropic/claude-sonnet-4",
	}
	c.Providers["ollama"] = config.Provider{
		Name: "Ollama", Adapter: "openai-chat",
		BaseURL: "http://127.0.0.1:11434/v1", DefaultModel: "qwen3",
	}
	return c
}

func TestParseClientID(t *testing.T) {
	valid := []string{"codex", "claude", "claude-desktop", "grok", "generic"}
	for _, s := range valid {
		id, err := ParseClientID(s)
		if err != nil || !id.Valid() {
			t.Errorf("ParseClientID(%q) = %v, %v; want valid", s, id, err)
		}
	}
	for _, s := range []string{"", "bogus", "CODEX", "codex/extra", "desktop"} {
		if _, err := ParseClientID(s); err == nil {
			t.Errorf("ParseClientID(%q) succeeded, want error", s)
		}
	}
}

func TestRouteFor(t *testing.T) {
	cfg := testConfig()
	cases := []struct {
		client   ClientID
		provider string
	}{
		{Codex, "openrouter"},
		{Claude, "openrouter"},
		{ClaudeDesktop, "openrouter"},
		{Grok, "openrouter"},
		{Generic, "ollama"},
	}
	for _, tc := range cases {
		r := RouteFor(cfg, tc.client)
		if r.Provider != tc.provider {
			t.Errorf("RouteFor(%s) provider = %q, want %q", tc.client, r.Provider, tc.provider)
		}
	}
}

func TestResolveDefaultModel(t *testing.T) {
	cfg := testConfig()
	// 四个客户端路由互不影响：每个客户端用自己的 route 模型。
	for _, tc := range []struct {
		client   ClientID
		provider string
		model    string
	}{
		{Codex, "openrouter", "anthropic/claude-sonnet-4"},
		{Claude, "openrouter", "anthropic/claude-sonnet-4"},
		{Grok, "openrouter", "anthropic/claude-sonnet-4"},
		{Generic, "ollama", "qwen3"},
	} {
		for _, m := range []string{"", ReservedModel} {
			res, err := Resolve(tc.client, m, cfg)
			if err != nil {
				t.Fatalf("Resolve(%s, %q): %v", tc.client, m, err)
			}
			if res.Provider != tc.provider || res.Model != tc.model {
				t.Errorf("Resolve(%s, %q) = %+v, want provider=%s model=%s",
					tc.client, m, res, tc.provider, tc.model)
			}
		}
	}
}

func TestResolveClaudePickerAlias(t *testing.T) {
	cfg := testConfig()
	cases := []struct {
		requested string
		provider  string
		model     string
	}{
		{ClaudePickerDefault, "openrouter", "anthropic/claude-sonnet-4"},
		{"claude-gw-ollama--qwen3", "ollama", "qwen3"},
		{"claude-gw2-openrouter--anthropic~sclaude-sonnet-4", "openrouter", "anthropic/claude-sonnet-4"},
		{"claude-gw2-openrouter--openai~sgpt-5", "openrouter", "openai/gpt-5"},
	}
	for _, tc := range cases {
		res, err := Resolve(Claude, tc.requested, cfg)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", tc.requested, err)
		}
		if res.Provider != tc.provider || res.Model != tc.model {
			t.Errorf("Resolve(%q) = %+v, want provider=%s model=%s",
				tc.requested, res, tc.provider, tc.model)
		}
	}
}

func TestResolveClaudeDesktopFamilyModelsUseDesktopRoute(t *testing.T) {
	cfg := testConfig()
	cfg.Routes.Claude = config.Route{Provider: "ollama", Model: "qwen3"}
	cfg.Routes.ClaudeDesktop = config.Route{Provider: "openrouter", Model: "anthropic/claude-opus-5"}
	for _, model := range []string{"claude-sonnet-5", "claude-opus-5", "claude-fable-5", "claude-haiku-4-5"} {
		res, err := Resolve(ClaudeDesktop, model, cfg)
		if err != nil {
			t.Fatalf("Resolve(ClaudeDesktop, %q): %v", model, err)
		}
		if res.Provider != "openrouter" || res.Model != "anthropic/claude-opus-5" {
			t.Errorf("Resolve(ClaudeDesktop, %q) = %+v, want Desktop route", model, res)
		}
	}
	res, err := Resolve(Claude, "claude-sonnet-5", cfg)
	if err == nil || err.Error() != UnmatchedModelMessage("claude-sonnet-5") {
		t.Fatalf("Claude Code family name = %v, want unchanged routing semantics", err)
	}
	res, err = Resolve(Generic, "claude-sonnet-5", cfg)
	if err == nil || res.Provider != "" {
		t.Fatalf("Generic family name = %+v, %v, want no Desktop mapping", res, err)
	}
}

func TestResolveClaudeDesktopWithoutRouteIsNotConfigured(t *testing.T) {
	cfg := testConfig()
	cfg.Routes.ClaudeDesktop = config.Route{}
	if _, err := Resolve(ClaudeDesktop, "claude-sonnet-5", cfg); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Desktop without route error = %v", err)
	}
}

func TestResolveProviderPrefixOverride(t *testing.T) {
	cfg := testConfig()
	// openrouter/anthropic/claude-sonnet-4 → provider openrouter, model
	// anthropic/claude-sonnet-4 (prefix stripped once).
	res, err := Resolve(Generic, "openrouter/anthropic/claude-sonnet-4", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "openrouter" || res.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("prefix override = %+v, want provider=openrouter model=anthropic/claude-sonnet-4", res)
	}
}

func TestResolveGenericUsesUniqueModelOwner(t *testing.T) {
	cfg := testConfig()
	cfg.Providers["agentrouter"] = config.Provider{
		Name: "AgentRouter", Adapter: "anthropic", BaseURL: "https://agentrouter.org",
		DefaultModel: "claude-opus-5",
		Models:       []config.ProviderModel{{ID: "claude-opus-5"}},
	}

	res, err := Resolve(Generic, "claude-opus-5", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "agentrouter" || res.Model != "claude-opus-5" {
		t.Errorf("unique model owner = %+v, want provider=agentrouter model=claude-opus-5", res)
	}
}

func TestResolveUniqueModelOwnerOnlyAppliesToGeneric(t *testing.T) {
	cfg := testConfig()
	// qwen3 只登记在 ollama。generic 会按唯一归属转到 ollama；
	// 一等客户端不得因此改走别家，也不得把该名字透传给当前路由。
	_, err := Resolve(Codex, "qwen3", cfg)
	if err == nil || err.Error() != UnmatchedModelMessage("qwen3") {
		t.Fatalf("codex unattributed model = %v, want unmatched", err)
	}
	res, err := Resolve(Generic, "qwen3", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "ollama" || res.Model != "qwen3" {
		t.Errorf("generic unique owner = %+v, want provider=ollama model=qwen3", res)
	}
}

func TestResolveGenericAmbiguousModelOwnership(t *testing.T) {
	cfg := testConfig()
	for _, id := range []string{"agent-a", "agent-b"} {
		cfg.Providers[id] = config.Provider{
			Name: id, Adapter: "anthropic", BaseURL: "https://example.com",
			DefaultModel: "shared-model",
		}
	}

	_, err := Resolve(Generic, "shared-model", cfg)
	if err == nil || !strings.Contains(err.Error(), "multiple providers (agent-a, agent-b)") {
		t.Fatalf("ambiguous model error = %v", err)
	}

	ollama := cfg.Providers["ollama"]
	ollama.Models = []config.ProviderModel{{ID: "shared-model"}}
	ollama.DefaultModel = "shared-model"
	cfg.Providers["ollama"] = ollama
	res, err := Resolve(Generic, "shared-model", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "ollama" {
		t.Errorf("route owner did not break ambiguity: %+v", res)
	}
}

func BenchmarkResolveGenericIndexedOwners(b *testing.B) {
	cfg := testConfig()
	for i := 0; i < 128; i++ {
		id := "provider-" + strconv.Itoa(i)
		cfg.Providers[id] = config.Provider{Name: id, Adapter: "openai-chat", BaseURL: "https://example.com", DefaultModel: "model-" + strconv.Itoa(i)}
	}
	m := config.NewManager(filepath.Join(b.TempDir(), "config.yaml"))
	if err := m.Write(cfg); err != nil {
		b.Fatal(err)
	}
	cfg = m.View()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Resolve(Generic, "model-127", cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func TestResolveListedRouteModelKeepsSlash(t *testing.T) {
	cfg := testConfig()
	// anthropic 不是已配置 provider id，但该完整名字是当前路由
	// openrouter 的 default_model：含斜杠也不得报“未知供应商”。
	res, err := Resolve(Codex, "anthropic/claude-sonnet-4", cfg)
	if err != nil {
		t.Fatalf("listed route model with '/' must not be rejected: %v", err)
	}
	if res.Provider != "openrouter" || res.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("listed route model = %+v, want provider=openrouter model=anthropic/claude-sonnet-4", res)
	}
}

func TestResolveRejectsUnattributedModel(t *testing.T) {
	cfg := testConfig()
	for _, tc := range []struct {
		client    ClientID
		requested string
	}{
		{Generic, "qwen2.5"},
		{Generic, "vendor/unknown-model"},
		{Generic, "unknown/"},
		{Codex, "openai/gpt-4o"},
		{Codex, "ollama"},
	} {
		_, err := Resolve(tc.client, tc.requested, cfg)
		if err == nil || err.Error() != UnmatchedModelMessage(tc.requested) {
			t.Errorf("Resolve(%s, %q) = %v, want unmatched", tc.client, tc.requested, err)
		}
	}
}

func TestResolveModelIsExactlyProviderID(t *testing.T) {
	cfg := testConfig()
	// 模型恰好等于 provider id（无斜杠）不触发前缀覆盖，也没有登记归属。
	_, err := Resolve(Codex, "ollama", cfg)
	if err == nil || err.Error() != UnmatchedModelMessage("ollama") {
		t.Fatalf("exact provider id as model = %v, want unmatched", err)
	}
}

func TestResolveRejectsEmptyRestAfterProviderPrefix(t *testing.T) {
	cfg := testConfig()
	// "openrouter/" 命中 provider 前缀但 rest 为空：明确字段错误，绝不
	// 把空模型发往上游。
	for _, m := range []string{"openrouter/", "ollama/"} {
		_, err := Resolve(Generic, m, cfg)
		if err == nil {
			t.Errorf("Resolve(%q) succeeded, want a clear field error", m)
			continue
		}
		if !strings.Contains(err.Error(), "model") || !strings.Contains(err.Error(), "prefix") {
			t.Errorf("Resolve(%q) error unclear: %v", m, err)
		}
	}
	// 前缀未命中时空 rest 不再透传：模型没有可归属供应商。
	_, err := Resolve(Generic, "unknown/", cfg)
	if err == nil || err.Error() != UnmatchedModelMessage("unknown/") {
		t.Fatalf("unknown prefix with empty rest = %v, want unmatched", err)
	}
}

func TestResolveClientRoutesAreIsolated(t *testing.T) {
	cfg := testConfig()
	// 修改 codex 的路由不影响其他客户端。
	cfg.Routes.Codex = config.Route{Provider: "ollama", Model: "llama3"}
	res, err := Resolve(Codex, "", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "ollama" || res.Model != "llama3" {
		t.Errorf("codex route = %+v", res)
	}
	res, err = Resolve(Claude, "", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "openrouter" {
		t.Errorf("claude route leaked codex changes: %+v", res)
	}
}

func TestResolveErrors(t *testing.T) {
	t.Run("unconfigured route", func(t *testing.T) {
		cfg := testConfig()
		cfg.Routes.Grok = config.Route{}
		if _, err := Resolve(Grok, "m", cfg); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Fatalf("want not-configured error, got %v", err)
		}
	})
	t.Run("route references missing provider", func(t *testing.T) {
		cfg := testConfig()
		cfg.Routes.Generic = config.Route{Provider: "ghost", Model: "m"}
		if _, err := Resolve(Generic, "m", cfg); err == nil || !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("want unknown-provider error, got %v", err)
		}
	})
}
