package codex

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

const PlaceholderEnvironment = "AI_GATEWAY_PLACEHOLDER_KEY"
const PlaceholderValue = "ai-gateway-local"

func Transform(original []byte, baseURL string) ([]byte, error) {
	doc, err := parse(original)
	if err != nil {
		return nil, err
	}
	providers, err := object(doc, "model_providers")
	if err != nil {
		return nil, err
	}
	doc["model_provider"] = "ai-gateway"
	doc["model"] = "gateway-default"
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

func Check(data []byte, baseURL string) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	providers, ok := doc["model_providers"].(map[string]any)
	if !ok {
		return false, nil
	}
	p, ok := providers["ai-gateway"].(map[string]any)
	if !ok {
		return false, nil
	}
	return doc["model_provider"] == "ai-gateway" && doc["model"] == "gateway-default" &&
		p["name"] == "ai-gateway" && p["base_url"] == baseURL+"/c/codex/v1" &&
		p["wire_api"] == "responses" && p["env_key"] == PlaceholderEnvironment, nil
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
