package route

import (
	"strings"
	"testing"
)

func TestClaudePickerRoundTrip(t *testing.T) {
	cases := []struct {
		selectable string
		alias      string
	}{
		{ReservedModel, ClaudePickerDefault},
		{"ollama/qwen3", "claude-gw-ollama--qwen3"},
		{"zhipu/glm-5", "claude-gw-zhipu--glm-5"},
		{"openrouter/anthropic/claude-sonnet-4", "claude-gw2-openrouter--anthropic~sclaude-sonnet-4"},
		{"openrouter/openai/gpt-5", "claude-gw2-openrouter--openai~sgpt-5"},
		{"acme/model-with~tilde", "claude-gw2-acme--model-with~ttilde"},
		{"ab--cd/foo", "claude-gw3-ab--cd~sfoo"},
	}
	for _, tc := range cases {
		got := ClaudePickerID(tc.selectable)
		if got != tc.alias {
			t.Errorf("ClaudePickerID(%q) = %q, want %q", tc.selectable, got, tc.alias)
		}
		if back := DecodeClaudePickerID(got); back != tc.selectable {
			t.Errorf("DecodeClaudePickerID(%q) = %q, want %q", got, back, tc.selectable)
		}
	}
}

func TestDecodeClaudePickerIDLeavesNonAliasUnchanged(t *testing.T) {
	for _, id := range []string{
		"",
		ReservedModel,
		"openrouter/anthropic/claude-sonnet-4",
		"qwen3",
		"claude-sonnet-4-6",
		"claude-gw-",
		"claude-gw-openrouter--",
		"claude-gw2-openrouter--",
		"claude-ocx-ollama--qwen3",
	} {
		if got := DecodeClaudePickerID(id); got != id {
			t.Errorf("DecodeClaudePickerID(%q) = %q, want unchanged", id, got)
		}
	}
}

func TestClaudePickerV1DoesNotExpandEscapes(t *testing.T) {
	// A historical v1 alias whose model portion literally contained ~s must
	// stay literal, matching the OpenCodex v1 decode rule.
	got := DecodeClaudePickerID("claude-gw-acme--foo~sbar")
	if got != "acme/foo~sbar" {
		t.Errorf("v1 decode = %q, want literal ~s", got)
	}
}

func TestClaudePickerIDsPassClaudeDiscoveryFilter(t *testing.T) {
	for _, selectable := range []string{
		ReservedModel,
		"ollama/qwen3",
		"deepseek/deepseek-chat",
		"openrouter/openai/gpt-5",
		"ab--cd/weird",
	} {
		id := ClaudePickerID(selectable)
		if !claudeDiscoveryKeeps(id) {
			t.Errorf("picker id %q for %q would be dropped by Claude Code", id, selectable)
		}
	}
}

func claudeDiscoveryKeeps(id string) bool {
	// Official filter: id contains "claude" or "anthropic", case-insensitive.
	s := strings.ToLower(id)
	return strings.Contains(s, "claude") || strings.Contains(s, "anthropic")
}
