package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/point/jsonedit"
)

const apiKeyPlaceholder = "sk-ai-gateway-local"

// subagentModelKeys are the tier aliases Claude Code resolves for Agent tool
// calls. The startup model remains independent in ANTHROPIC_MODEL.
var subagentModelKeys = []string{
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
}

// titleModelKeys are the small/fast aliases Claude Code uses for title and
// other inexpensive helper calls.
var titleModelKeys = []string{
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
}

// discoveryKey makes Claude Code query the gateway for selectable models at
// startup. Claude Code keeps only ids matching /(claude|anthropic)/i, so
// /c/claude/v1/models exposes reversible claude-gw* picker aliases and
// keeps display_name as the real selectable id. The four startup env slots
// stay gateway-default; route.Resolve decodes an alias before §7.4.
// Discovery is not enough on its own: a stale or foreign
// cache/gateway-models.json, or CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC,
// leaves /model empty. Point/sync therefore also pre-writes that cache
// the same way OpenCodex's `ocx claude` does (docs/v1-scheme.md §12.4).
const discoveryKey = "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"

// envKey is the settings.json member holding Claude Code's environment slots.
const envKey = "env"

// Transform merges the gateway's env slots into Claude Code's user settings,
// leaving every unrelated `env` variable untouched (docs/v1-scheme.md §12.4).
//
// Only the named slots are rewritten: permissions, hooks, status line, MCP
// switches and every other member keep their original bytes, order and
// formatting (§12.1, 2026-08-21 evidence in §20).
func Transform(original []byte, baseURL string, settings clientcatalog.Settings) ([]byte, error) {
	out, err := jsonedit.SetObjectStrings(original, envKey, targets(baseURL, settings))
	if err != nil {
		if errors.Is(err, jsonedit.ErrNotObject) {
			return nil, fmt.Errorf("Claude settings field %q must be an object", envKey)
		}
		return nil, fmt.Errorf("edit Claude settings: %w", err)
	}
	return out, nil
}

func Check(data []byte, baseURL string, settings clientcatalog.Settings) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	env, ok := doc[envKey].(map[string]any)
	if !ok {
		return false, nil
	}
	for _, kv := range targets(baseURL, settings) {
		if env[kv.Key] != kv.Value {
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
	env, ok := doc[envKey].(map[string]any)
	if !ok {
		return false, nil
	}
	return env["ANTHROPIC_BASE_URL"] == baseURL+"/c/claude", nil
}

// targets is the ordered set of env slots the gateway owns. The order is fixed
// so a repeated point writes byte-identical settings.
func targets(baseURL string, settings clientcatalog.Settings) []jsonedit.KV {
	out := []jsonedit.KV{
		{Key: "ANTHROPIC_BASE_URL", Value: baseURL + "/c/claude"},
		{Key: "ANTHROPIC_API_KEY", Value: apiKeyPlaceholder},
		{Key: discoveryKey, Value: "1"},
	}
	out = append(out, jsonedit.KV{Key: "ANTHROPIC_MODEL", Value: settings.Model()})
	for _, key := range subagentModelKeys {
		out = append(out, jsonedit.KV{Key: key, Value: settings.SubagentModelValue()})
	}
	for _, key := range titleModelKeys {
		out = append(out, jsonedit.KV{Key: key, Value: settings.TitleModelValue()})
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
