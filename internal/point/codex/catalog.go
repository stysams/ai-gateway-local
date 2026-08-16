package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ai-gateway/internal/point/clientcatalog"
)

// CatalogFileName is the sidecar Codex reads through the root
// model_catalog_json key. It lives next to config.toml and must not collide
// with OpenCodex's opencodex-catalog.json.
const CatalogFileName = "ai-gateway-catalog.json"

// CatalogPath is the absolute sidecar path that belongs to a Codex config file.
func CatalogPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), CatalogFileName)
}

// ModelsCacheName is Codex's on-disk picker cache. Point/sync/restore delete
// it so /model does not keep a stale list.
const ModelsCacheName = "models_cache.json"

// FindNativeTemplate returns a deep copy of the first bundled catalog entry
// that can be cloned into a routed row: a slash-free slug with a non-empty
// base_instructions string. Missing that template is fatal; callers must not
// invent a stub prompt (docs/v1-scheme.md §12.3).
func FindNativeTemplate(raw []byte) (map[string]any, error) {
	catalog, err := parseCatalog(raw)
	if err != nil {
		return nil, err
	}
	for _, entry := range catalog {
		slug, _ := entry["slug"].(string)
		instructions, _ := entry["base_instructions"].(string)
		if slug == "" || containsSlash(slug) || instructions == "" {
			continue
		}
		cloned, err := cloneEntry(entry)
		if err != nil {
			return nil, err
		}
		return cloned, nil
	}
	return nil, fmt.Errorf("codex bundled catalog has no native template with base_instructions")
}

// BuildCatalog clones template once per selectable id and encodes the
// replacement catalog Codex's /model command reads. The official prompt stays
// in base_instructions; model_messages is dropped (2026-08-16 evidence).
func BuildCatalog(template map[string]any, settings clientcatalog.Settings) ([]byte, error) {
	if template == nil {
		return nil, fmt.Errorf("codex catalog template is missing")
	}
	if _, ok := template["base_instructions"].(string); !ok {
		return nil, fmt.Errorf("codex catalog template is missing base_instructions")
	}
	ids := catalogIDs(settings)
	models := make([]map[string]any, 0, len(ids))
	for i, id := range ids {
		entry, err := routedEntry(template, id, i)
		if err != nil {
			return nil, err
		}
		models = append(models, entry)
	}
	return json.Marshal(map[string]any{"models": models})
}

// CatalogMatches reports whether the sidecar on disk lists exactly the
// gateway-enabled slugs, keeps a cloned base_instructions, and has no
// model_messages on those routed rows.
func CatalogMatches(path string, settings clientcatalog.Settings) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	catalog, err := parseCatalog(raw)
	if err != nil {
		return false, nil
	}
	want := catalogIDs(settings)
	if len(catalog) != len(want) {
		return false, nil
	}
	seen := map[string]bool{}
	for _, entry := range catalog {
		slug, _ := entry["slug"].(string)
		display, _ := entry["display_name"].(string)
		instructions, _ := entry["base_instructions"].(string)
		if slug == "" || display != slug || instructions == "" {
			return false, nil
		}
		if _, hasMessages := entry["model_messages"]; hasMessages {
			return false, nil
		}
		seen[slug] = true
	}
	for _, id := range want {
		if !seen[id] {
			return false, nil
		}
	}
	return true, nil
}

func catalogIDs(settings clientcatalog.Settings) []string {
	ids := []string{clientcatalog.ReservedModel}
	seen := map[string]bool{clientcatalog.ReservedModel: true}
	for _, entry := range settings.Catalog {
		if entry.ID == "" || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		ids = append(ids, entry.ID)
	}
	return ids
}

func routedEntry(template map[string]any, id string, priority int) (map[string]any, error) {
	entry, err := cloneEntry(template)
	if err != nil {
		return nil, err
	}
	entry["slug"] = id
	entry["display_name"] = id
	entry["visibility"] = "list"
	entry["supported_in_api"] = true
	entry["priority"] = priority
	entry["upgrade"] = nil
	entry["tool_mode"] = "code_mode_only"
	delete(entry, "model_messages")
	delete(entry, "availability_nux")
	delete(entry, "service_tiers")
	delete(entry, "service_tier")
	delete(entry, "default_service_tier")
	delete(entry, "additional_speed_tiers")
	delete(entry, "use_responses_lite")
	delete(entry, "supports_websockets")
	delete(entry, "prefer_websockets")
	ensureStrictFields(entry)
	if instructions, _ := entry["base_instructions"].(string); instructions == "" {
		return nil, fmt.Errorf("cloned catalog entry %q lost base_instructions", id)
	}
	return entry, nil
}

func ensureStrictFields(entry map[string]any) {
	if _, ok := entry["supported_reasoning_levels"]; !ok {
		entry["supported_reasoning_levels"] = []any{}
	}
	if _, ok := entry["shell_type"]; !ok {
		entry["shell_type"] = "shell_command"
	}
	if _, ok := entry["support_verbosity"].(bool); !ok {
		entry["support_verbosity"] = true
	}
	if _, ok := entry["supports_parallel_tool_calls"].(bool); !ok {
		entry["supports_parallel_tool_calls"] = true
	}
	if _, ok := entry["experimental_supported_tools"]; !ok {
		entry["experimental_supported_tools"] = []any{}
	}
	if _, ok := entry["truncation_policy"].(map[string]any); !ok {
		entry["truncation_policy"] = map[string]any{"mode": "tokens", "limit": 10000}
	}
	window, _ := entry["context_window"].(float64)
	if window <= 0 {
		// Codex's parser requires a number. 128000 is that required placeholder,
		// not a claim about the upstream model's real window; do not infer from
		// the model id (docs/v1-scheme.md §7.5).
		entry["context_window"] = 128000
		window = 128000
	}
	if maxWindow, _ := entry["max_context_window"].(float64); maxWindow <= 0 {
		entry["max_context_window"] = window
	}
	if _, ok := entry["auto_compact_token_limit"]; !ok {
		entry["auto_compact_token_limit"] = int(window * 0.9)
	}
}

func parseCatalog(raw []byte) ([]map[string]any, error) {
	raw = bytes.TrimSpace(raw)
	if i := bytes.IndexByte(raw, '{'); i > 0 {
		raw = raw[i:]
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse Codex catalog: %w", err)
	}
	if doc.Models == nil {
		return nil, fmt.Errorf("Codex catalog is missing models")
	}
	return doc.Models, nil
}

func cloneEntry(entry map[string]any) (map[string]any, error) {
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func containsSlash(s string) bool {
	return strings.Contains(s, "/")
}
