// Package endpoint joins provider base URLs onto completion paths
// (docs/v1-scheme.md §10). Preset Claude and GPT adapters add /v1 when the
// base URL does not already end with it. A custom model endpoint is used
// exactly as written.
package endpoint

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	Chat      = "openai-chat"
	Responses = "openai-responses"
	Messages  = "anthropic"
	Custom    = "custom"
)

// IsWire reports whether adapter is one of the three outbound wire protocols.
func IsWire(adapter string) bool {
	switch adapter {
	case Chat, Responses, Messages:
		return true
	}
	return false
}

// IsModelAdapter reports whether adapter is allowed on models[].adapter.
func IsModelAdapter(adapter string) bool {
	return IsWire(adapter) || adapter == Custom
}

// PresetPath is the locked request path shown for a preset adapter.
func PresetPath(adapter string) string {
	switch adapter {
	case Chat:
		return "/v1/chat/completions"
	case Responses:
		return "/v1/responses"
	case Messages:
		return "/v1/messages"
	}
	return ""
}

// WirePath is the path appended when base_url already ends with /v1.
func WirePath(adapter string) string {
	switch adapter {
	case Chat:
		return "/chat/completions"
	case Responses:
		return "/responses"
	case Messages:
		return "/messages"
	}
	return ""
}

// InferWire maps a custom request path onto a wire protocol.
func InferWire(customEndpoint string) (string, bool) {
	path := strings.TrimRight(strings.TrimSpace(customEndpoint), "/")
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return Chat, true
	case strings.HasSuffix(path, "/responses"):
		return Responses, true
	case strings.HasSuffix(path, "/messages"):
		return Messages, true
	}
	return "", false
}

// ValidateCustom reports whether a user-maintained request path is usable.
func ValidateCustom(customEndpoint string) error {
	path := strings.TrimSpace(customEndpoint)
	if path == "" {
		return fmt.Errorf("must not be empty")
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return fmt.Errorf("must be an absolute URL path starting with /")
	}
	if strings.ContainsAny(path, " \t\r\n#?") {
		return fmt.Errorf("must not contain whitespace, query string, or fragment")
	}
	if _, err := url.Parse(path); err != nil {
		return fmt.Errorf("must be a valid URL path")
	}
	if _, ok := InferWire(path); !ok {
		return fmt.Errorf("must end with /chat/completions, /responses, or /messages")
	}
	return nil
}

// Join builds the upstream completion URL.
//
// When customEndpoint is empty, the preset adapter path is used and /v1 is
// inserted unless baseURL already ends with /v1. A custom endpoint is
// appended as written and does not receive a /v1 prefix.
func Join(baseURL, adapter, customEndpoint string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if custom := strings.TrimSpace(customEndpoint); custom != "" {
		if !strings.HasPrefix(custom, "/") {
			custom = "/" + custom
		}
		return base + custom
	}
	path := WirePath(adapter)
	if path == "" {
		return base
	}
	if strings.HasSuffix(base, "/v1") {
		return base + path
	}
	return base + "/v1" + path
}
