package main

import "testing"

func TestTrayCatalogListsEnabledModelsAsProviderModelID(t *testing.T) {
	enabled := true
	disabled := false
	got := trayCatalog([]trayProvider{
		{
			ID: "openrouter", Enabled: &enabled, DefaultModel: "gpt-5",
			Models: []trayModel{
				{ID: "gpt-5"},
				{ID: "anthropic/claude-sonnet-4"},
				{ID: "hidden", Enabled: &disabled},
			},
		},
		{ID: "ollama", Enabled: &disabled, DefaultModel: "qwen3", Models: []trayModel{{ID: "qwen3"}}},
		{ID: "deepseek", DefaultModel: "deepseek-chat"},
	})
	want := []trayRoute{
		{Provider: "openrouter", Model: "gpt-5"},
		{Provider: "openrouter", Model: "anthropic/claude-sonnet-4"},
		{Provider: "deepseek", Model: "deepseek-chat"},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("catalog[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
