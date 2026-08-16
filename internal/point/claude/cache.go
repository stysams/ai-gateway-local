package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/route"
)

// CacheFileName is the on-disk picker list Claude Code's /model command reads
// when gateway discovery cannot refresh (docs/v1-scheme.md §12.4).
const CacheFileName = "gateway-models.json"

// CachePath is <claude-config-dir>/cache/gateway-models.json. Claude Code
// ignores the file unless baseUrl equals ANTHROPIC_BASE_URL exactly.
func CachePath(settingsPath string) string {
	return filepath.Join(filepath.Dir(settingsPath), "cache", CacheFileName)
}

type cacheModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
}

type cacheDoc struct {
	BaseURL   string       `json:"baseUrl"`
	FetchedAt int64        `json:"fetchedAt"`
	Models    []cacheModel `json:"models"`
}

// BaseURL is the Claude Code inbound prefix written to both settings.env
// and the picker cache. The client only uses the cache when these match.
func BaseURL(gatewayBase string) string {
	return strings.TrimRight(gatewayBase, "/") + "/c/claude"
}

// BuildCache encodes Claude Code's gateway-models.json schema: compact JSON
// with baseUrl, fetchedAt (unix ms), and models[{id, display_name}]. id is the
// reversible picker alias; display_name stays the real selectable id.
func BuildCache(gatewayBase string, settings clientcatalog.Settings, now time.Time) ([]byte, error) {
	doc := cacheDoc{
		BaseURL:   BaseURL(gatewayBase),
		FetchedAt: now.UnixMilli(),
		Models:    cacheModels(settings),
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// CacheMatches reports whether the on-disk picker list has the gateway base
// URL and exactly the enabled selectable ids. fetchedAt and extra JSON fields
// are ignored so a later Claude Code refresh of the same catalog is not drift.
func CacheMatches(path, gatewayBase string, settings clientcatalog.Settings) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	var doc cacheDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false, nil
	}
	if doc.BaseURL != BaseURL(gatewayBase) {
		return false, nil
	}
	want := cacheModels(settings)
	if len(doc.Models) != len(want) {
		return false, nil
	}
	seen := make(map[string]string, len(doc.Models))
	for _, item := range doc.Models {
		if item.ID == "" {
			return false, nil
		}
		if _, dup := seen[item.ID]; dup {
			return false, nil
		}
		seen[item.ID] = item.DisplayName
	}
	for _, item := range want {
		if seen[item.ID] != item.DisplayName {
			return false, nil
		}
	}
	return true, nil
}

// RemoveOwnedCache deletes the picker cache only when it names this
// gateway's Claude base URL. Foreign caches (OpenCodex, official Anthropic)
// are left untouched.
func RemoveOwnedCache(path, gatewayBase string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc cacheDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	if doc.BaseURL != BaseURL(gatewayBase) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func cacheModels(settings clientcatalog.Settings) []cacheModel {
	ids := []string{clientcatalog.ReservedModel}
	seen := map[string]bool{clientcatalog.ReservedModel: true}
	for _, entry := range settings.Catalog {
		if entry.ID == "" || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		ids = append(ids, entry.ID)
	}
	out := make([]cacheModel, 0, len(ids))
	for _, id := range ids {
		picker := route.ClaudePickerID(id)
		if !usablePickerID(picker) {
			continue
		}
		out = append(out, cacheModel{ID: picker, DisplayName: id})
	}
	return out
}

func usablePickerID(id string) bool {
	lower := strings.ToLower(id)
	return strings.Contains(lower, "claude") || strings.Contains(lower, "anthropic")
}
