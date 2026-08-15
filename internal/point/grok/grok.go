package grok

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

func Transform(original []byte, baseURL string) ([]byte, error) {
	doc, err := parse(original)
	if err != nil {
		return nil, err
	}
	models, err := object(doc, "models")
	if err != nil {
		return nil, err
	}
	modelSet, err := object(doc, "model")
	if err != nil {
		return nil, err
	}
	models["default"] = "ai-gateway"
	modelSet["ai-gateway"] = map[string]any{
		"model": "gateway-default", "base_url": baseURL + "/c/grok/v1",
		"name": "ai-gateway", "api_backend": "responses", "api_key": "sk-ai-gateway-local",
	}
	out, err := toml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode Grok config: %w", err)
	}
	return out, nil
}

func Check(data []byte, baseURL string) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	models, ok := doc["models"].(map[string]any)
	if !ok || models["default"] != "ai-gateway" {
		return false, nil
	}
	modelSet, ok := doc["model"].(map[string]any)
	if !ok {
		return false, nil
	}
	p, ok := modelSet["ai-gateway"].(map[string]any)
	if !ok {
		return false, nil
	}
	return p["model"] == "gateway-default" && p["base_url"] == baseURL+"/c/grok/v1" &&
		p["name"] == "ai-gateway" && p["api_backend"] == "responses" && p["api_key"] == "sk-ai-gateway-local", nil
}

func parse(data []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(data) == 0 {
		return doc, nil
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse Grok config: %w", err)
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
			return nil, fmt.Errorf("Grok config field %q must be a table", key)
		}
		return out, nil
	}
	out := map[string]any{}
	parent[key] = out
	return out, nil
}
