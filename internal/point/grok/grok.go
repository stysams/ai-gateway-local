package grok

import (
	"errors"
	"fmt"
	"strings"

	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/point/tomledit"
	"github.com/pelletier/go-toml/v2"
)

const apiKeyPlaceholder = "sk-ai-gateway-local"

// modelTable holds one table per selectable model; modelsTable holds the picker
// state, whose defaultKey names the startup entry (docs/v1-scheme.md §12.5).
const (
	modelTable  = "model"
	modelsTable = "models"
	defaultKey  = "default"
)

// preferredKey holds the startup model; `[models] default` always points at it,
// so changing a route only rewrites this one entry.
const preferredKey = "ai-gateway"

// catalogPrefix marks the entries the gateway owns. User-declared models never
// carry it, so a catalog refresh can drop stale rows without touching them.
const catalogPrefix = "ai-gateway:"

// Transform points Grok Build at the gateway and lands the full enabled catalog
// in its configuration. Grok is the only first-class client whose config file
// can express many models at once: `[model."<id>"]` tables are additive to the
// built-in models rather than replacing them, and ids containing `/` and `:`
// both parse correctly (docs/v1-scheme.md §12.5, evidence in §20).
//
// Only `[models] default` and the gateway's own `[model."ai-gateway*"]` tables
// are touched. §12.5 requires the user's other models, MCP, plugin, permission
// and UI configuration to be preserved, so the rest of the document keeps its
// original bytes (2026-08-21 evidence in §20). A shape tomledit cannot splice
// falls back to re-encoding the whole document.
func Transform(original []byte, baseURL string, settings clientcatalog.Settings) ([]byte, error) {
	if out, err := transformInPlace(original, baseURL, settings); err == nil {
		return out, nil
	} else if !errors.Is(err, tomledit.ErrUnsupportedShape) {
		return nil, err
	}
	return transformWhole(original, baseURL, settings)
}

func transformInPlace(original []byte, baseURL string, settings clientcatalog.Settings) ([]byte, error) {
	doc, err := tomledit.Parse(original)
	if err != nil {
		return nil, err
	}
	want := entries(settings)
	for _, key := range doc.ChildNames([]string{modelTable}) {
		if !ownedKey(key) || containsKey(want, key) {
			continue
		}
		if err := doc.DeleteTree([]string{modelTable, key}); err != nil {
			return nil, err
		}
	}
	for _, item := range want {
		if err := doc.SetStrings([]string{modelTable, item.key}, entryKeys(baseURL, item)); err != nil {
			return nil, err
		}
	}
	if err := doc.SetString([]string{modelsTable, defaultKey}, preferredKey); err != nil {
		return nil, err
	}
	return doc.Bytes()
}

// transformWhole is the fallback for a config.toml whose model tables live in an
// inline table or an array of tables. It re-encodes the document, which keeps
// every unknown field and its semantics but not the original layout (§12.1).
func transformWhole(original []byte, baseURL string, settings clientcatalog.Settings) ([]byte, error) {
	doc, err := parse(original)
	if err != nil {
		return nil, err
	}
	models, err := object(doc, modelsTable)
	if err != nil {
		return nil, err
	}
	modelSet, err := object(doc, modelTable)
	if err != nil {
		return nil, err
	}
	want := entries(settings)
	for key := range modelSet {
		if ownedKey(key) && !containsKey(want, key) {
			delete(modelSet, key)
		}
	}
	for _, item := range want {
		value, exists := modelSet[item.key]
		if !exists {
			value = map[string]any{}
			modelSet[item.key] = value
		}
		table, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Grok config field %q must be a table", "model."+item.key)
		}
		for _, kv := range entryKeys(baseURL, item) {
			table[kv.Key] = kv.Value
		}
	}
	models[defaultKey] = preferredKey
	out, err := toml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode Grok config: %w", err)
	}
	return out, nil
}

// entryKeys is the ordered set of keys inside one `[model."<key>"]` table. The
// order matches the §12.5 example.
func entryKeys(baseURL string, item entry) []tomledit.KV {
	return []tomledit.KV{
		{Key: "model", Value: item.model},
		{Key: "base_url", Value: baseURL + "/c/grok/v1"},
		{Key: "name", Value: item.model},
		{Key: "api_backend", Value: "responses"},
		{Key: "api_key", Value: apiKeyPlaceholder},
	}
}

func Check(data []byte, baseURL string, settings clientcatalog.Settings) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	models, ok := doc[modelsTable].(map[string]any)
	if !ok || models[defaultKey] != preferredKey {
		return false, nil
	}
	modelSet, ok := doc[modelTable].(map[string]any)
	if !ok {
		return false, nil
	}
	want := entries(settings)
	for _, item := range want {
		p, ok := modelSet[item.key].(map[string]any)
		if !ok {
			return false, nil
		}
		for _, kv := range entryKeys(baseURL, item) {
			if p[kv.Key] != kv.Value {
				return false, nil
			}
		}
	}
	// A gateway-owned entry that is no longer part of the catalog means the file
	// is stale rather than pointed.
	for key := range modelSet {
		if ownedKey(key) && !containsKey(want, key) {
			return false, nil
		}
	}
	return true, nil
}

// Managed reports whether ai-gateway owns this configuration, independent of
// which catalog generation it currently holds.
func Managed(data []byte, baseURL string) (bool, error) {
	doc, err := parse(data)
	if err != nil {
		return false, err
	}
	modelSet, ok := doc[modelTable].(map[string]any)
	if !ok {
		return false, nil
	}
	table, ok := modelSet[preferredKey].(map[string]any)
	if !ok {
		return false, nil
	}
	return table["base_url"] == baseURL+"/c/grok/v1", nil
}

type entry struct {
	key   string
	model string
}

// entries is the exact set of tables the gateway owns: the preferred model that
// `[models] default` points at, then every other selectable model. The reserved
// model is always offered so the user can fall back to following the route.
// The picker label is the selectable id itself (`gateway-default` or
// `<provider-id>/<model-id>`), never a friendly catalog name.
func entries(settings clientcatalog.Settings) []entry {
	preferred := settings.Model()
	out := []entry{{key: preferredKey, model: preferred}}
	seen := map[string]bool{preferred: true}
	candidates := make([]string, 0, len(settings.Catalog)+1)
	candidates = append(candidates, clientcatalog.ReservedModel)
	for _, item := range settings.Catalog {
		candidates = append(candidates, item.ID)
	}
	for _, id := range candidates {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, entry{key: catalogPrefix + id, model: id})
	}
	return out
}

func ownedKey(key string) bool {
	return key == preferredKey || strings.HasPrefix(key, catalogPrefix)
}

func containsKey(entries []entry, key string) bool {
	for _, e := range entries {
		if e.key == key {
			return true
		}
	}
	return false
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
