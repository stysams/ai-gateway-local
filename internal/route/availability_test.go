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
