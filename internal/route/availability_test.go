package route

import (
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
