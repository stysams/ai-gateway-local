package route

import (
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
	valid := []string{"codex", "claude", "grok", "generic"}
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

func TestResolveUnknownPrefixPassedThrough(t *testing.T) {
	cfg := testConfig()
	// anthropic 不是已配置 provider id：模型完整交给当前路由 provider，
	// 即使它含斜杠也不得报"未知供应商"（docs/v1-scheme.md §7.4 step 4）。
	res, err := Resolve(Codex, "anthropic/claude-sonnet-4", cfg)
	if err != nil {
		t.Fatalf("model with '/' must not be rejected: %v", err)
	}
	if res.Provider != "openrouter" || res.Model != "anthropic/claude-sonnet-4" {
		t.Errorf("passthrough = %+v, want provider=openrouter model=anthropic/claude-sonnet-4", res)
	}

	// 单段模型同样完整转发。
	res, err = Resolve(Generic, "qwen2.5", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "ollama" || res.Model != "qwen2.5" {
		t.Errorf("single segment = %+v", res)
	}
}

func TestResolveModelIsExactlyProviderID(t *testing.T) {
	cfg := testConfig()
	// 模型恰好等于 provider id（无斜杠）不触发前缀覆盖：完整转发。
	res, err := Resolve(Codex, "ollama", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Provider != "openrouter" || res.Model != "ollama" {
		t.Errorf("exact id model = %+v, want route provider with full model", res)
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
	// 前缀未命中时空 rest 不是错误：完整透传（如 "foo/" 交给当前 provider）。
	res, err := Resolve(Generic, "unknown/", cfg)
	if err != nil {
		t.Fatalf("unknown prefix with empty rest: %v", err)
	}
	if res.Provider != "ollama" || res.Model != "unknown/" {
		t.Errorf("passthrough = %+v", res)
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
