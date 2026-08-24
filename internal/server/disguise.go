package server

import (
	"net/http"

	"ai-gateway/internal/config"
	"ai-gateway/internal/route"
)

// Claude Code 2.1.228, Codex CLI 0.147.0 and Pi 0.84.2 identity headers,
// verified against real client requests and installed client configuration.
// Keep these values aligned with
// desktop/src/headerPresets.ts. Session, install, window and environment
// identifiers stay out (docs/v1-scheme.md §10.1).
var (
	claudeDisguiseHeaders = map[string]string{
		"User-Agent": "claude-cli/2.1.228 (external, cli)",
		"X-App":      "cli",
		"Anthropic-Dangerous-Direct-Browser-Access": "true",
		"Anthropic-Beta": "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,advanced-tool-use-2025-11-20,effort-2025-11-24",
	}
	codexDisguiseHeaders = map[string]string{
		"User-Agent": "codex_cli_rs/0.147.0",
		"Originator": "codex_cli_rs",
	}
	piDisguiseHeaders = map[string]string{
		"User-Agent": "Pi Agent/1.0",
	}
)

func disguiseHeaders(kind string) map[string]string {
	switch kind {
	case config.DisguiseClientClaude:
		return claudeDisguiseHeaders
	case config.DisguiseClientCodex:
		return codexDisguiseHeaders
	case config.DisguiseClientPi:
		return piDisguiseHeaders
	default:
		return nil
	}
}

// outboundExtraHeaders builds the provider extra-header map sent upstream.
// Generic inbound may receive a verified client disguise; first-class
// clients do not. extra_headers overlay the disguise; Anthropic-Beta
// unions with inbound tokens. Claude Messages body overlay lives in
// inbound/messages.ApplyClaudeDisguise (docs/v1-scheme.md §10.1).
func outboundExtraHeaders(p config.Provider, client route.ClientID, inbound http.Header) map[string]string {
	extra := p.ExtraHeaders
	if client == route.Generic {
		extra = mergeDisguiseHeaders(p.DisguiseClient, extra)
	}
	return mergeInboundAnthropicBeta(extra, inbound)
}

// mergeDisguiseHeaders copies the named identity set, then overlays
// extra_headers. extra_headers win on the same name except Anthropic-Beta,
// which is unioned with disguise tokens first.
func mergeDisguiseHeaders(kind string, extra map[string]string) map[string]string {
	preset := disguiseHeaders(kind)
	if len(preset) == 0 {
		return extra
	}
	out := cloneStringMap(preset)
	for name, value := range extra {
		if http.CanonicalHeaderKey(name) == anthropicBetaHeader {
			key, configured := headerMapLookup(out, anthropicBetaHeader)
			merged := unionCommaTokens(configured, value)
			if key == "" {
				out[name] = merged
			} else {
				out[key] = merged
			}
			continue
		}
		if key, _ := headerMapLookup(out, name); key != "" {
			delete(out, key)
		}
		out[name] = value
	}
	return out
}
