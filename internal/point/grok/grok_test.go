package grok

import (
	"strings"
	"testing"

	"ai-gateway/internal/point/clientcatalog"
	"github.com/pelletier/go-toml/v2"
)

const baseURL = "http://127.0.0.1:12600"

// A catalog generation that drops a model must remove exactly that gateway
// table and nothing the user wrote (docs/v1-scheme.md §12.5).
func TestTransformPrunesRetiredEntriesAndKeepsUserConfiguration(t *testing.T) {
	original := `theme = "dark"

# MCP servers
[mcp.servers.example-files]
command = "npx"
args = ["-y", "@example/files-mcp"]

[models]
default = "ai-gateway"

[model."my-own"]
model = "my-own"
name = "My Own"

[model.ai-gateway]
model = 'gateway-default'
base_url = 'http://127.0.0.1:12600/c/grok/v1'
name = 'gateway-default'
api_backend = 'responses'
api_key = 'sk-ai-gateway-local'

[model.'ai-gateway:example/retired']
model = 'example/retired'
base_url = 'http://127.0.0.1:12600/c/grok/v1'
name = 'example/retired'
api_backend = 'responses'
api_key = 'sk-ai-gateway-local'

[plugin.sample]
enabled = true
`
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel, Catalog: []clientcatalog.Entry{
		{ID: "example/kept"},
	}}
	out, err := Transform([]byte(original), baseURL, settings)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "example/retired") {
		t.Errorf("retired gateway entry survived:\n%s", got)
	}
	for _, keep := range []string{
		"theme = \"dark\"",
		"# MCP servers",
		"[mcp.servers.example-files]",
		"args = [\"-y\", \"@example/files-mcp\"]",
		"[model.\"my-own\"]\nmodel = \"my-own\"\nname = \"My Own\"",
		"[plugin.sample]\nenabled = true",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("user configuration %q was disturbed:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, "[model.'ai-gateway:example/kept']") {
		t.Errorf("new catalog entry missing:\n%s", got)
	}
	ok, err := Check(out, baseURL, settings)
	if err != nil || !ok {
		t.Fatalf("Check after transform: ok=%v err=%v\n%s", ok, err, got)
	}
}

// An in-place splice cannot reach a model declared inside an inline table. The
// documented fallback re-encodes the whole document, which still lands the
// contract even though the layout is lost (§12.1).
func TestTransformFallsBackForInlineModelTable(t *testing.T) {
	original := "theme = 'dark'\nmodel = { \"my-own\" = { model = \"my-own\" } }\n"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	out, err := Transform([]byte(original), baseURL, settings)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Check(out, baseURL, settings)
	if err != nil || !ok {
		t.Fatalf("fallback output is not pointed: ok=%v err=%v\n%s", ok, err, out)
	}
	doc := map[string]any{}
	if err := toml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	models, _ := doc["model"].(map[string]any)
	if _, ok := models["my-own"]; !ok {
		t.Errorf("user model lost in the fallback path:\n%s", out)
	}
	if doc["theme"] != "dark" {
		t.Errorf("unrelated field lost in the fallback path:\n%s", out)
	}
}

func TestTransformFallsBackForDottedModelKeys(t *testing.T) {
	original := "theme = 'dark'\nmodel.ai-gateway.model = 'old'\n"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	out, err := Transform([]byte(original), baseURL, settings)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Check(out, baseURL, settings)
	if err != nil || !ok {
		t.Fatalf("dotted-key fallback is not pointed: ok=%v err=%v\n%s", ok, err, out)
	}
	if !strings.Contains(string(out), "theme = 'dark'") {
		t.Fatalf("unrelated field lost in dotted-key fallback:\n%s", out)
	}
}

func TestTransformFallbackPreservesUnknownGatewayModelFields(t *testing.T) {
	original := "model = { \"ai-gateway\" = { model = \"old\", custom_option = \"keep-me\" } }\n"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	out, err := Transform([]byte(original), baseURL, settings)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	modelSet, ok := doc[modelTable].(map[string]any)
	if !ok {
		t.Fatalf("model table missing: %v", doc)
	}
	entry, ok := modelSet[preferredKey].(map[string]any)
	if !ok || entry["custom_option"] != "keep-me" {
		t.Fatalf("fallback dropped unknown model field: %v\n%s", entry, out)
	}
}

func TestTransformDoesNotHideParseErrors(t *testing.T) {
	_, err := Transform([]byte("model = ["), baseURL, clientcatalog.Settings{})
	if err == nil {
		t.Fatal("invalid TOML was accepted")
	}
}

// Pointing an already pointed configuration must not change a single byte, so a
// repeated sync cannot churn the file.
func TestTransformIsIdempotent(t *testing.T) {
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel, Catalog: []clientcatalog.Entry{
		{ID: "example/model-a"},
	}}
	first, err := Transform([]byte("theme = \"dark\"\n"), baseURL, settings)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Transform(first, baseURL, settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("repeated transform changed the file:\nfirst  %q\nsecond %q", first, second)
	}
}
