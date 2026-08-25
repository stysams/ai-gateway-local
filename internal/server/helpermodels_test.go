package server

import (
	"net/http"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/ir"
	"ai-gateway/internal/route"
	"ai-gateway/internal/secret"
)

func TestCodexHelperModelClassification(t *testing.T) {
	cfg := config.Defaults()
	cfg.Clients.Codex.SubagentModel = "openrouter/default/anthropic/claude-sonnet-4"
	cfg.Clients.Codex.TitleModel = "ollama/default/qwen3"

	tests := []struct {
		name      string
		client    route.ClientID
		protocol  ir.Protocol
		requested string
		headers   http.Header
		model     string
		reason    string
	}{
		{
			name:   "explicit subagent header wins over helper model",
			client: route.Codex, protocol: ir.ProtocolResponses, requested: "gpt-5.6-luna",
			headers: http.Header{"X-Openai-Subagent": {"collab_spawn"}},
			model:   cfg.Clients.Codex.SubagentModel, reason: "subagent",
		},
		{
			name:   "metadata thread spawn",
			client: route.Codex, protocol: ir.ProtocolResponses, requested: "gpt-5",
			headers: http.Header{"X-Codex-Turn-Metadata": {`{"subagent_kind":"thread_spawn"}`}},
			model:   cfg.Clients.Codex.SubagentModel, reason: "subagent",
		},
		{
			name:   "shadow helper without metadata",
			client: route.Codex, protocol: ir.ProtocolResponses, requested: "gpt-5.6-luna-2026-08-01",
			model: cfg.Clients.Codex.TitleModel, reason: "title_or_helper",
		},
		{
			name:   "normal turn is not a helper",
			client: route.Codex, protocol: ir.ProtocolResponses, requested: "gpt-5.6-luna",
			headers: http.Header{"X-Codex-Turn-Metadata": {`{"request_kind":"turn"}`}},
			model:   "gpt-5.6-luna",
		},
		{
			name:   "provider-qualified model is not a helper",
			client: route.Codex, protocol: ir.ProtocolResponses, requested: "openrouter/gpt-5.6-luna",
			model: "openrouter/gpt-5.6-luna",
		},
		{
			name:   "generic client is untouched",
			client: route.Generic, protocol: ir.ProtocolResponses, requested: "gpt-5.6-luna",
			model: "gpt-5.6-luna",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, reason := codexHelperModel(test.client, test.protocol, test.requested, test.headers, cfg)
			if model != test.model || reason != test.reason {
				t.Fatalf("codexHelperModel() = (%q, %q), want (%q, %q)", model, reason, test.model, test.reason)
			}
		})
	}
}

func TestConfiguredHelperModelFallsBackWhenDisabled(t *testing.T) {
	cfg := config.Defaults()
	configured := "ollama/default/qwen3"
	provider := cfg.Providers["ollama"]
	provider.Enabled = config.BoolPtr(false)
	cfg.Providers["ollama"] = provider
	if got := availableHelperModel(configured, cfg); got != route.ReservedModel {
		t.Fatalf("availableHelperModel() = %q, want %q", got, route.ReservedModel)
	}
	cfg.Clients.Claude.SubagentModel = configured
	s := &Server{}
	if got := s.clientSettings(cfg, "claude").SubagentModel; got != "" {
		t.Fatalf("disabled Claude subagent setting = %q, want gateway-default fallback", got)
	}
}

func TestCodexHelperModelsReachSelectedUpstreams(t *testing.T) {
	primary := newFakeUpstream(t, nil)
	fast := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(primary.URL, fast.URL, false)
	openrouter := cfg.Providers["openrouter"]
	group := openrouter.KeyGroups["default"]
	group.Adapter = "openai-responses"
	group.Endpoint = "/v1/responses"
	openrouter.KeyGroups["default"] = group
	cfg.Providers["openrouter"] = openrouter
	ollama := cfg.Providers["ollama"]
	group = ollama.KeyGroups["default"]
	group.Adapter = "openai-responses"
	group.Endpoint = "/v1/responses"
	ollama.KeyGroups["default"] = group
	cfg.Providers["ollama"] = ollama
	cfg.Clients.Codex.SubagentModel = "openrouter/default/anthropic/claude-sonnet-4"
	cfg.Clients.Codex.TitleModel = "ollama/default/qwen3"
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	body := []byte(`{"model":"gpt-5.6-luna","input":[{"role":"user","content":"hi"}],"stream":false}`)

	resp, data := chatPost(t, addr, "/c/codex/v1/responses", body, map[string]string{codexSubagentHeader: "collab_spawn"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subagent status = %d, body %s", resp.StatusCode, data)
	}
	if got := primary.last().Model; got != "anthropic/claude-sonnet-4" {
		t.Fatalf("subagent upstream model = %q", got)
	}

	resp, data = chatPost(t, addr, "/c/codex/v1/responses", body, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("title/helper status = %d, body %s", resp.StatusCode, data)
	}
	if got := fast.last().Model; got != "qwen3" {
		t.Fatalf("title/helper upstream model = %q", got)
	}
}
