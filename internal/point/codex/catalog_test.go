package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-gateway/internal/point/clientcatalog"
)

func TestBuildCatalogClonesTemplateAndDropsModelMessages(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "point", "codex-bundled-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	template, err := FindNativeTemplate(raw)
	if err != nil {
		t.Fatal(err)
	}
	data, err := BuildCatalog(template, clientcatalog.Settings{Catalog: []clientcatalog.Entry{
		{ID: "openrouter/anthropic/claude-sonnet-4"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(doc.Models))
	}
	if doc.Models[0]["slug"] != clientcatalog.ReservedModel {
		t.Fatalf("first slug = %v", doc.Models[0]["slug"])
	}
	routed := doc.Models[1]
	if routed["slug"] != "openrouter/anthropic/claude-sonnet-4" || routed["display_name"] != routed["slug"] {
		t.Fatalf("routed identity = %#v", routed)
	}
	if routed["base_instructions"] != "SENTINEL_BUNDLED_BASE_INSTRUCTIONS_FOR_TESTS_ONLY" {
		t.Fatalf("base_instructions = %v", routed["base_instructions"])
	}
	if _, ok := routed["model_messages"]; ok {
		t.Fatal("model_messages survived on a routed entry")
	}
	if strings.Contains(string(data), "MUST_BE_REMOVED_FROM_ROUTED_ENTRIES") {
		t.Fatal("cloned model_messages text leaked")
	}
}

func TestFindNativeTemplateRejectsEmptyCatalog(t *testing.T) {
	if _, err := FindNativeTemplate([]byte(`{"models":[]}`)); err == nil {
		t.Fatal("expected error")
	}
}
