package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"ai-gateway/internal/point/clientcatalog"
)

const apiKeyPlaceholder = "sk-ai-gateway-local"

// modelKeys are the single-value slots Claude Code reads for its startup model.
var modelKeys = []string{
	"ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
}

// discoveryKey makes Claude Code query the gateway for selectable models at
// startup. Claude Code keeps only ids matching /(claude|anthropic)/i, so its
// picker cannot show the whole enabled catalog — that is client behaviour and
// must not be worked around by rewriting model ids. The complete catalog stays
// reachable through /c/claude/v1/models and `claude --model <id>`.
// See docs/v1-scheme.md §12.4 and the 2026-08-15 evidence in §20.
const discoveryKey = "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"

// Transform merges the gateway's env slots into Claude Code's user settings,
// leaving every unrelated `env` variable untouched.
func Transform(original []byte, baseURL string, settings clientcatalog.Settings) ([]byte, error) {
	doc, err := parse(original)
	if err != nil {
		return nil, err
	}
	env, err := object(doc, "env")
	if err != nil {
		return nil, err
	}
	for name, value := range targets(baseURL, settings) {
		env[name] = value
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Claude settings: %w", err)
	}
	return append(out, '\n'), nil
}

func Check(data []byte, baseURL string, settings clientcatalog.Settings) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	env, ok := doc["env"].(map[string]any)
	if !ok {
		return false, nil
	}
	for name, value := range targets(baseURL, settings) {
		if env[name] != value {
			return false, nil
		}
	}
	return true, nil
}

// Managed reports whether ai-gateway owns these settings, independent of which
// model was written into them.
func Managed(data []byte, baseURL string) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	env, ok := doc["env"].(map[string]any)
	if !ok {
		return false, nil
	}
	return env["ANTHROPIC_BASE_URL"] == baseURL+"/c/claude", nil
}

func targets(baseURL string, settings clientcatalog.Settings) map[string]string {
	out := map[string]string{
		"ANTHROPIC_BASE_URL": baseURL + "/c/claude",
		"ANTHROPIC_API_KEY":  apiKeyPlaceholder,
		discoveryKey:         "1",
	}
	preferred := settings.Model()
	for _, key := range modelKeys {
		out[key] = preferred
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
