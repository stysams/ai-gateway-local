package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/route"
)

func TestBuildCacheUsesPickerAliasesAndRealDisplayNames(t *testing.T) {
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	raw, err := BuildCache("http://127.0.0.1:12600", clientcatalog.Settings{
		Catalog: []clientcatalog.Entry{
			{ID: "openrouter/anthropic/claude-sonnet-4", DisplayName: "Claude Sonnet 4"},
			{ID: "deepseek/deepseek-chat"},
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	var doc cacheDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.BaseURL != "http://127.0.0.1:12600/c/claude" {
		t.Fatalf("baseUrl = %q", doc.BaseURL)
	}
	if doc.FetchedAt != now.UnixMilli() {
		t.Fatalf("fetchedAt = %d, want %d", doc.FetchedAt, now.UnixMilli())
	}
	want := []cacheModel{
		{ID: route.ClaudePickerDefault, DisplayName: clientcatalog.ReservedModel},
		{ID: route.ClaudePickerID("openrouter/anthropic/claude-sonnet-4"), DisplayName: "openrouter/anthropic/claude-sonnet-4"},
		{ID: route.ClaudePickerID("deepseek/deepseek-chat"), DisplayName: "deepseek/deepseek-chat"},
	}
	if len(doc.Models) != len(want) {
		t.Fatalf("models = %#v", doc.Models)
	}
	for i, item := range want {
		if doc.Models[i] != item {
			t.Fatalf("models[%d] = %#v, want %#v", i, doc.Models[i], item)
		}
		if !usablePickerID(item.ID) {
			t.Fatalf("picker id %q would be dropped by Claude Code", item.ID)
		}
	}
	if string(raw) != `{"baseUrl":"http://127.0.0.1:12600/c/claude","fetchedAt":1786881600000,"models":[{"id":"claude-gw-default","display_name":"gateway-default"},{"id":"claude-gw2-openrouter--anthropic~sclaude-sonnet-4","display_name":"openrouter/anthropic/claude-sonnet-4"},{"id":"claude-gw-deepseek--deepseek-chat","display_name":"deepseek/deepseek-chat"}]}` {
		t.Fatalf("wire schema drifted:\n%s", raw)
	}
}

func TestCacheMatchesIgnoresFetchedAtAndRejectsForeignBaseURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache", CacheFileName)
	settings := clientcatalog.Settings{Catalog: []clientcatalog.Entry{{ID: "zhipu/glm-5"}}}
	raw, err := BuildCache("http://127.0.0.1:12600", settings, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := CacheMatches(path, "http://127.0.0.1:12600", settings)
	if err != nil || !ok {
		t.Fatalf("matching catalog rejected: ok=%v err=%v", ok, err)
	}
	stale, err := BuildCache("http://127.0.0.1:12600", settings, time.Unix(99, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err = CacheMatches(path, "http://127.0.0.1:12600", settings)
	if err != nil || !ok {
		t.Fatalf("refreshed fetchedAt reported as drift: ok=%v err=%v", ok, err)
	}
	foreign := []byte(`{"baseUrl":"http://127.0.0.1:10100","fetchedAt":1,"models":[{"id":"claude-gw-default","display_name":"gateway-default"},{"id":"claude-gw-zhipu--glm-5","display_name":"zhipu/glm-5"}]}`)
	if err := os.WriteFile(path, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err = CacheMatches(path, "http://127.0.0.1:12600", settings)
	if err != nil || ok {
		t.Fatalf("foreign OpenCodex cache accepted: ok=%v err=%v", ok, err)
	}
}

func TestRemoveOwnedCacheLeavesForeignPickerList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CacheFileName)
	foreign := []byte(`{"baseUrl":"http://127.0.0.1:10100","fetchedAt":1,"models":[]}`)
	if err := os.WriteFile(path, foreign, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOwnedCache(path, "http://127.0.0.1:12600"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign cache was removed: %v", err)
	}
	owned, err := BuildCache("http://127.0.0.1:12600", clientcatalog.Settings{}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, owned, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOwnedCache(path, "http://127.0.0.1:12600"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errorsIsNotExist(err) {
		t.Fatalf("owned cache still present: %v", err)
	}
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
