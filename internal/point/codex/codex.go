package codex

import (
	"fmt"

	"ai-gateway/internal/point/clientcatalog"
	"github.com/pelletier/go-toml/v2"
)

const PlaceholderEnvironment = "AI_GATEWAY_PLACEHOLDER_KEY"
const PlaceholderValue = "ai-gateway-local"

// Transform points Codex at the gateway.
//
// Only the startup preferred model is written. Codex can express a full picker
// catalog through the root `model_catalog_json` key, but that catalog replaces
// the bundled one *and* its per-entry instructions replace Codex's own agent
// system prompt — measured at 21178 characters down to 32. Codex also reaches
// any `<provider-id>/<model-id>` without a catalog entry, so the catalog buys
// picker rows at the cost of crippling the client. See docs/v1-scheme.md §12.3
// and the 2026-08-15 verification record in §20.
func Transform(original []byte, baseURL string, settings clientcatalog.Settings) ([]byte, error) {
	doc, err := parse(original)
	if err != nil {
		return nil, err
	}
	providers, err := object(doc, "model_providers")
	if err != nil {
		return nil, err
	}
	doc["model_provider"] = "ai-gateway"
	doc["model"] = settings.Model()
	providers["ai-gateway"] = map[string]any{
		"name": "ai-gateway", "base_url": baseURL + "/c/codex/v1",
		"wire_api": "responses", "env_key": PlaceholderEnvironment,
	}
	out, err := toml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode Codex config: %w", err)
	}
	return out, nil
}

func Check(data []byte, baseURL string, settings clientcatalog.Settings) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	p, ok := providerBlock(doc)
	if !ok {
		return false, nil
	}
	return doc["model_provider"] == "ai-gateway" && doc["model"] == settings.Model() &&
		p["name"] == "ai-gateway" && p["base_url"] == baseURL+"/c/codex/v1" &&
		p["wire_api"] == "responses" && p["env_key"] == PlaceholderEnvironment, nil
}

// Managed reports whether ai-gateway owns this configuration, regardless of
// which model or catalog generation was written into it. The point transaction
// uses it to update a managed file in place instead of creating a second
// restore point over an already pointed configuration.
func Managed(data []byte, baseURL string) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	p, ok := providerBlock(doc)
	if !ok {
		return false, nil
	}
	return doc["model_provider"] == "ai-gateway" && p["base_url"] == baseURL+"/c/codex/v1", nil
}

func providerBlock(doc map[string]any) (map[string]any, bool) {
	providers, ok := doc["model_providers"].(map[string]any)
	if !ok {
		return nil, false
	}
	p, ok := providers["ai-gateway"].(map[string]any)
	return p, ok
}

func parse(data []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(data) == 0 {
		return doc, nil
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse Codex config: %w", err)
	}
	return doc, nil
}

func object(parent map[string]any, key string) (map[string]any, error) {
	if current, ok := parent[key]; ok {
		if current == nil {
			out := map[string]any{}
			parent[key] = out
			return out, nil
		}
		out, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Codex config field %q must be a table", key)
		}
		return out, nil
	}
	out := map[string]any{}
	parent[key] = out
	return out, nil
}
