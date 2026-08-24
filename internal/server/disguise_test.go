package server

import (
	"net/http"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/route"
)

func TestMergeDisguiseHeadersClaudeThenExtraAndInboundBeta(t *testing.T) {
	extra := map[string]string{
		"User-Agent":     "operator-override",
		"Anthropic-Beta": "context-1m-2025-08-07",
		"X-Custom":       "kept",
	}
	got := mergeDisguiseHeaders(config.DisguiseClientClaude, extra)
	if got["User-Agent"] != "operator-override" {
		t.Fatalf("User-Agent = %q, want extra_headers overlay", got["User-Agent"])
	}
	if got["X-App"] != "cli" {
		t.Fatalf("X-App = %q", got["X-App"])
	}
	if got["X-Custom"] != "kept" {
		t.Fatalf("custom header lost: %v", got)
	}
	if got["Anthropic-Beta"] != claudeDisguiseHeaders["Anthropic-Beta"]+",context-1m-2025-08-07" {
		t.Fatalf("union beta = %q", got["Anthropic-Beta"])
	}
	if extra["Anthropic-Beta"] != "context-1m-2025-08-07" {
		t.Fatal("merge mutated extra_headers")
	}
}

func TestMergeDisguiseHeadersEmptyLeavesExtra(t *testing.T) {
	extra := map[string]string{"X-Custom": "kept"}
	got := mergeDisguiseHeaders("", extra)
	if got["X-Custom"] != "kept" || len(got) != 1 {
		t.Fatalf("empty disguise changed extra: %v", got)
	}
}

func TestMergeDisguiseHeadersPiThenExtra(t *testing.T) {
	got := mergeDisguiseHeaders(config.DisguiseClientPi, map[string]string{"X-Custom": "kept"})
	if got["User-Agent"] != "Pi Agent/1.0" {
		t.Fatalf("Pi User-Agent = %q", got["User-Agent"])
	}
	if got["X-Custom"] != "kept" {
		t.Fatalf("custom header lost: %v", got)
	}
	overridden := mergeDisguiseHeaders(config.DisguiseClientPi, map[string]string{"user-agent": "operator-override"})
	if overridden["user-agent"] != "operator-override" || len(overridden) != 1 {
		t.Fatalf("Pi extra_headers overlay = %v", overridden)
	}
}

func TestOutboundExtraHeadersDisguiseOnlyGeneric(t *testing.T) {
	p := config.Provider{
		DisguiseClient: config.DisguiseClientCodex,
		ExtraHeaders:   map[string]string{"X-Trace": "1"},
	}
	generic := outboundExtraHeaders(p, route.Generic, http.Header{})
	if generic["User-Agent"] != "codex_cli_rs/0.147.0" || generic["Originator"] != "codex_cli_rs" {
		t.Fatalf("generic disguise = %v", generic)
	}
	if generic["X-Trace"] != "1" {
		t.Fatalf("extra_headers dropped: %v", generic)
	}
	claude := outboundExtraHeaders(p, route.Claude, http.Header{})
	if claude["User-Agent"] != "" || claude["Originator"] != "" {
		t.Fatalf("first-class client received disguise: %v", claude)
	}
	if claude["X-Trace"] != "1" {
		t.Fatalf("first-class extra_headers = %v", claude)
	}
}

func TestOutboundExtraHeadersPiDisguiseOnlyGeneric(t *testing.T) {
	p := config.Provider{DisguiseClient: config.DisguiseClientPi}
	generic := outboundExtraHeaders(p, route.Generic, http.Header{})
	if generic["User-Agent"] != "Pi Agent/1.0" {
		t.Fatalf("generic Pi disguise = %v", generic)
	}
	if got := outboundExtraHeaders(p, route.Codex, http.Header{}); got["User-Agent"] != "" {
		t.Fatalf("first-class client received Pi disguise: %v", got)
	}
}
