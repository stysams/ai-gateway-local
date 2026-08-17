package server

import (
	"net/http"
	"strings"
)

const anthropicBetaHeader = "Anthropic-Beta"

// mergeInboundAnthropicBeta copies extra headers and unions inbound
// Anthropic-Beta tokens onto the configured value. Other inbound headers
// stay blocked (docs/v1-scheme.md §10.1).
func mergeInboundAnthropicBeta(extra map[string]string, inbound http.Header) map[string]string {
	inboundParts := inbound.Values(anthropicBetaHeader)
	if len(inboundParts) == 0 {
		return extra
	}
	out := cloneHeaderMap(extra)
	key, configured := headerMapLookup(out, anthropicBetaHeader)
	parts := append([]string{configured}, inboundParts...)
	merged := unionCommaTokens(parts...)
	if merged == "" {
		return extra
	}
	if key == "" {
		out[anthropicBetaHeader] = merged
	} else {
		out[key] = merged
	}
	return out
}

func cloneHeaderMap(source map[string]string) map[string]string {
	out := make(map[string]string, len(source)+1)
	for name, value := range source {
		out[name] = value
	}
	return out
}

func headerMapLookup(headers map[string]string, name string) (string, string) {
	want := http.CanonicalHeaderKey(name)
	for key, value := range headers {
		if http.CanonicalHeaderKey(key) == want {
			return key, value
		}
	}
	return "", ""
}

func unionCommaTokens(parts ...string) string {
	seen := make(map[string]bool)
	var ordered []string
	for _, part := range parts {
		for _, token := range strings.Split(part, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			fold := strings.ToLower(token)
			if seen[fold] {
				continue
			}
			seen[fold] = true
			ordered = append(ordered, token)
		}
	}
	return strings.Join(ordered, ",")
}
