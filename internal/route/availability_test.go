package route

import (
	"strings"
	"testing"

	"ai-gateway/internal/config"
)

func TestResolveRejectsDisabledProviderAndModel(t *testing.T) {
	disabled := false
	cfg := config.Defaults()
	p := cfg.Providers["ollama"]
	p.Models = []config.ProviderModel{{ID: "enabled"}, {ID: "disabled", Enabled: &disabled}}
	p.DefaultModel = "enabled"
	cfg.Providers["ollama"] = p
	if _, err := Resolve(Generic, "ollama/disabled", cfg); err == nil {
		t.Fatal("disabled model resolved")
	}
	p.Enabled = &disabled
	cfg.Providers["ollama"] = p
	if _, err := Resolve(Generic, "gateway-default", cfg); err == nil {
		t.Fatal("disabled provider resolved")
	}
}

func TestResolveListedDisabledModelStillReportsDisabled(t *testing.T) {
	cfg := testConfig()
	disabled := false
	p := cfg.Providers["openrouter"]
	p.Models = []config.ProviderModel{{ID: "anthropic/claude-sonnet-4", Enabled: &disabled}}
	cfg.Providers["openrouter"] = p
	_, err := Resolve(Codex, "anthropic/claude-sonnet-4", cfg)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("listed disabled = %v, want disabled", err)
	}
}

func TestResolvePrefixOverrideIgnoresDisabledRouteProvider(t *testing.T) {
	disabled := false
	cfg := testConfig()
	routeProvider := cfg.Providers["openrouter"]
	routeProvider.Enabled = &disabled
	cfg.Providers["openrouter"] = routeProvider

	res, err := Resolve(Codex, "ollama/qwen3", cfg)
	if err != nil {
		t.Fatalf("prefix override against disabled route provider: %v", err)
	}
	if res.Provider != "ollama" || res.Model != "qwen3" {
		t.Fatalf("prefix override = %+v, want ollama/qwen3", res)
	}

	if _, err := Resolve(Codex, ReservedModel, cfg); err == nil || !strings.Contains(err.Error(), `provider "openrouter" is disabled`) {
		t.Fatalf("gateway-default = %v, want disabled route provider", err)
	}
	if _, err := Resolve(Codex, "openrouter/anthropic/claude-sonnet-4", cfg); err == nil || !strings.Contains(err.Error(), `provider "openrouter" is disabled`) {
		t.Fatalf("disabled prefix target = %v, want disabled openrouter", err)
	}

	unattributed := "anyrouter/claude-opus-5"
	if _, err := Resolve(Codex, unattributed, cfg); err == nil || err.Error() != UnmatchedModelMessage(unattributed) {
		t.Fatalf("unattributed model = %v, want unmatched (not route-provider disabled)", err)
	}
}

func TestResolveCodexPrefixWhenDefaultRouteProviderDisabled(t *testing.T) {
	disabled := false
	cfg := testConfig()
	cfg.Providers["tudou"] = config.Provider{
		Name: "土豆", Adapter: "openai-responses",
		BaseURL: "https://example.invalid", DefaultModel: "gpt-5.6-sol",
		Enabled: &disabled,
	}
	cfg.Providers["any"] = config.Provider{
		Name: "anyrouter", Adapter: "anthropic",
		BaseURL: "https://anyrouter.top", DefaultModel: "claude-fable-5[1m]",
		Models: []config.ProviderModel{
			{ID: "claude-fable-5[1m]"},
			{ID: "claude-opus-5"},
		},
	}
	cfg.Providers["agentrouter"] = config.Provider{
		Name: "agent", Adapter: "anthropic",
		BaseURL: "https://agentrouter.org", DefaultModel: "claude-opus-5",
		Models: []config.ProviderModel{{ID: "claude-opus-5"}},
	}
	cfg.Routes.Codex = config.Route{Provider: "tudou", Model: "gpt-5.6-sol"}

	for _, tc := range []struct {
		requested string
		provider  string
		model     string
	}{
		{"any/claude-opus-5", "any", "claude-opus-5"},
		{"agentrouter/claude-opus-5", "agentrouter", "claude-opus-5"},
		{"claude-gw-any--claude-opus-5", "any", "claude-opus-5"},
	} {
		res, err := Resolve(Codex, tc.requested, cfg)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", tc.requested, err)
		}
		if res.Provider != tc.provider || res.Model != tc.model {
			t.Fatalf("Resolve(%q) = %+v, want provider=%s model=%s", tc.requested, res, tc.provider, tc.model)
		}
	}

	if _, err := Resolve(Codex, ReservedModel, cfg); err == nil || !strings.Contains(err.Error(), `provider "tudou" is disabled`) {
		t.Fatalf("gateway-default = %v, want disabled tudou", err)
	}
	if _, err := Resolve(Codex, "gpt-5.6-sol", cfg); err == nil || !strings.Contains(err.Error(), `provider "tudou" is disabled`) {
		t.Fatalf("listed model on disabled route = %v, want disabled tudou", err)
	}
}
