package server

import (
	"net/http"
	"testing"
)

func TestMergeInboundAnthropicBetaUnionsTokens(t *testing.T) {
	extra := map[string]string{
		"Anthropic-Beta": "claude-code-20250219,effort-2025-11-24",
		"User-Agent":     "claude-cli/2.1.228 (external, cli)",
	}
	inbound := http.Header{}
	inbound.Set("Anthropic-Beta", "claude-code-20250219,context-1m-2025-08-07,effort-2025-11-24")
	inbound.Set("X-Claude-Code-Session-Id", "sess-1")

	got := mergeInboundAnthropicBeta(extra, inbound)
	if got["Anthropic-Beta"] != "claude-code-20250219,effort-2025-11-24,context-1m-2025-08-07" {
		t.Fatalf("merged beta = %q", got["Anthropic-Beta"])
	}
	if got["User-Agent"] != extra["User-Agent"] {
		t.Fatalf("user-agent changed: %q", got["User-Agent"])
	}
	if _, ok := got["X-Claude-Code-Session-Id"]; ok {
		t.Fatal("session header leaked into extra headers")
	}
	if extra["Anthropic-Beta"] != "claude-code-20250219,effort-2025-11-24" {
		t.Fatal("merge mutated the configured extra_headers map")
	}
}

func TestMergeInboundAnthropicBetaUsesInboundWhenUnset(t *testing.T) {
	inbound := http.Header{}
	inbound.Add("anthropic-beta", "context-1m-2025-08-07")
	inbound.Add("Anthropic-Beta", "effort-2025-11-24")
	got := mergeInboundAnthropicBeta(nil, inbound)
	if got["Anthropic-Beta"] != "context-1m-2025-08-07,effort-2025-11-24" {
		t.Fatalf("inbound-only beta = %q", got["Anthropic-Beta"])
	}
}

func TestMergeInboundAnthropicBetaLeavesExtraWhenInboundMissing(t *testing.T) {
	extra := map[string]string{"Anthropic-Beta": "claude-code-20250219"}
	got := mergeInboundAnthropicBeta(extra, http.Header{})
	got["Anthropic-Beta"] = "mutated"
	if extra["Anthropic-Beta"] != "mutated" {
		t.Fatal("missing inbound beta must keep the original extra_headers map")
	}
}
