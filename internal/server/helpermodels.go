package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"ai-gateway/internal/config"
	"ai-gateway/internal/ir"
	"ai-gateway/internal/route"
)

const (
	codexSubagentHeader = "X-OpenAI-Subagent"
	codexMetadataHeader = "X-Codex-Turn-Metadata"
)

var codexHelperModelPrefixes = []string{"gpt-5.6-luna", "gpt-5.4-mini"}

type codexTurnMetadata struct {
	SubagentKind string `json:"subagent_kind"`
	RequestKind  string `json:"request_kind"`
}

// codexHelperModel mirrors the request categories emitted by current Codex
// clients. Subagent classification must run before the broader shadow/helper
// classification because a spawned agent may itself use a small model.
func codexHelperModel(client route.ClientID, proto ir.Protocol, requested string, headers http.Header, cfg *config.Config) (string, string) {
	if client != route.Codex || proto != ir.ProtocolResponses {
		return requested, ""
	}
	if isCodexThreadSpawn(headers) {
		return availableHelperModel(cfg.Clients.Codex.SubagentModel, cfg), "subagent"
	}
	if isCodexShadowHelper(requested, headers) {
		return availableHelperModel(cfg.Clients.Codex.TitleModel, cfg), "title_or_helper"
	}
	return requested, ""
}

func availableHelperModel(configured string, cfg *config.Config) string {
	if configured = configuredHelperModel(configured, cfg); configured != "" {
		return configured
	}
	return route.ReservedModel
}

// configuredHelperModel returns a configured provider/model only while it is
// selectable. Keeping this separate lets Claude sync gateway-default into its
// fixed environment slots without erasing the user's saved preference.
func configuredHelperModel(configured string, cfg *config.Config) string {
	configured = strings.TrimSpace(configured)
	if configured == "" || configured == route.ReservedModel {
		return ""
	}
	parts := strings.SplitN(configured, "/", 3)
	if len(parts) != 3 {
		return ""
	}
	providerID, keyID, modelID := parts[0], parts[1], parts[2]
	provider, exists := cfg.Providers[providerID]
	group, groupExists := provider.KeyGroups[keyID]
	if modelID == "" || !exists || !provider.EnabledValue() || !groupExists || !group.EnabledValue() || !group.ModelEnabled(modelID) {
		return ""
	}
	return configured
}

func isCodexThreadSpawn(headers http.Header) bool {
	if headers.Get(codexSubagentHeader) == "collab_spawn" {
		return true
	}
	metadata, ok := parseCodexMetadata(headers)
	return ok && metadata.SubagentKind == "thread_spawn"
}

func isCodexShadowHelper(requested string, headers http.Header) bool {
	if strings.Contains(requested, "/") {
		return false
	}
	matched := false
	for _, prefix := range codexHelperModelPrefixes {
		if strings.HasPrefix(requested, prefix) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	raw := strings.TrimSpace(headers.Get(codexMetadataHeader))
	if raw == "" {
		return true
	}
	metadata, ok := parseCodexMetadata(headers)
	return !ok || metadata.RequestKind != "turn"
}

func parseCodexMetadata(headers http.Header) (codexTurnMetadata, bool) {
	var metadata codexTurnMetadata
	raw := strings.TrimSpace(headers.Get(codexMetadataHeader))
	if raw == "" || json.Unmarshal([]byte(raw), &metadata) != nil {
		return codexTurnMetadata{}, false
	}
	return metadata, true
}
