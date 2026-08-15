package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var targetEnvironment = map[string]string{
	"ANTHROPIC_BASE_URL":             "",
	"ANTHROPIC_API_KEY":              "sk-ai-gateway-local",
	"ANTHROPIC_MODEL":                "gateway-default",
	"ANTHROPIC_DEFAULT_OPUS_MODEL":   "gateway-default",
	"ANTHROPIC_DEFAULT_SONNET_MODEL": "gateway-default",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL":  "gateway-default",
}

func Transform(original []byte, baseURL string, displayModel ...string) ([]byte, error) {
	doc, err := parse(original)
	if err != nil {
		return nil, err
	}
	env, err := object(doc, "env")
	if err != nil {
		return nil, err
	}
	for name, value := range targets(baseURL, displayModel...) {
		env[name] = value
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Claude settings: %w", err)
	}
	return append(out, '\n'), nil
}

func Check(data []byte, baseURL string, displayModel ...string) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	env, ok := doc["env"].(map[string]any)
	if !ok {
		return false, nil
	}
	for name, value := range targets(baseURL, displayModel...) {
		if env[name] != value {
			return false, nil
		}
	}
	return true, nil
}

func targets(baseURL string, displayModel ...string) map[string]string {
	out := make(map[string]string, len(targetEnvironment))
	for k, v := range targetEnvironment {
		out[k] = v
	}
	out["ANTHROPIC_BASE_URL"] = baseURL + "/c/claude"
	if len(displayModel) > 0 && displayModel[0] != "" {
		for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
			out[key] = displayModel[0]
		}
	}
	return out
}

func parse(data []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return doc, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse Claude settings: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse Claude settings: trailing JSON data")
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
			return nil, fmt.Errorf("Claude settings field %q must be an object", key)
		}
		return out, nil
	}
	out := map[string]any{}
	parent[key] = out
	return out, nil
}
