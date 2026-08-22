package codex

import (
	"strings"
	"testing"

	"ai-gateway/internal/point/clientcatalog"
)

func TestTransformWritesOpenAINameWhenRemoteCompactionEnabled(t *testing.T) {
	base := "http://127.0.0.1:12600"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel, RemoteCompaction: true}
	out, err := Transform(nil, base, settings, `C:\Users\test\.codex\ai-gateway-catalog.json`)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "name = 'OpenAI'") && !strings.Contains(text, `name = "OpenAI"`) {
		t.Fatalf("expected OpenAI provider name:\n%s", text)
	}
	doc, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := providerBlock(doc)
	if !ok || p["name"] != RemoteCompactionDisplayName {
		t.Fatalf("provider name = %v", p["name"])
	}
}

func TestTransformWritesDefaultNameWhenRemoteCompactionDisabled(t *testing.T) {
	base := "http://127.0.0.1:12600"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	out, err := Transform(nil, base, settings, `C:\Users\test\.codex\ai-gateway-catalog.json`)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, "name = 'ai-gateway'") && !strings.Contains(text, `name = "ai-gateway"`) {
		t.Fatalf("expected default provider name:\n%s", text)
	}
	if strings.Contains(text, "OpenAI") {
		t.Fatalf("unexpected OpenAI name:\n%s", text)
	}
}

// Switching remote compaction on and back off must only rewrite the provider
// name; the rest of config.toml keeps its bytes (docs/v1-scheme.md §12.3).
func TestRemoteCompactionToggleRewritesOnlyTheProviderName(t *testing.T) {
	base := "http://127.0.0.1:12600"
	catalog := `C:\Users\test\.codex\ai-gateway-catalog.json`
	off := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	on := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel, RemoteCompaction: true}
	original := "# banner\nmodel = 'gateway-default'\nmodel_provider = 'ai-gateway'\nmodel_catalog_json = 'C:\\Users\\test\\.codex\\ai-gateway-catalog.json'\n\n[mcp_servers.example]\ncommand = 'npx'\n\n[model_providers.ai-gateway]\nname = 'ai-gateway'\nbase_url = 'http://127.0.0.1:12600/c/codex/v1'\nwire_api = 'responses'\nenv_key = 'AI_GATEWAY_PLACEHOLDER_KEY'\n"
	enabled, err := Transform([]byte(original), base, on, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if string(enabled) != strings.Replace(original, "name = 'ai-gateway'", "name = 'OpenAI'", 1) {
		t.Fatalf("enabling remote compaction changed more than the name:\n%s", enabled)
	}
	disabled, err := Transform(enabled, base, off, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if string(disabled) != original {
		t.Fatalf("disabling remote compaction did not restore the file:\nwant %q\ngot  %q", original, disabled)
	}
}

// An in-place splice cannot reach a provider declared inside an inline table.
// The documented fallback re-encodes the whole document, which still lands the
// contract and keeps every unknown field (§12.1).
func TestTransformFallsBackForInlineProviderTable(t *testing.T) {
	base := "http://127.0.0.1:12600"
	original := "approval_policy = 'never'\nmodel_providers = { openai = { name = \"OpenAI\", wire_api = \"responses\" } }\n"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	out, err := Transform([]byte(original), base, settings, `C:\Users\test\.codex\ai-gateway-catalog.json`)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := providerBlock(doc)
	if !ok || p["base_url"] != base+"/c/codex/v1" {
		t.Fatalf("fallback did not write the gateway provider:\n%s", out)
	}
	providers, _ := doc["model_providers"].(map[string]any)
	if _, ok := providers["openai"]; !ok {
		t.Errorf("user provider lost in the fallback path:\n%s", out)
	}
	if doc["approval_policy"] != "never" {
		t.Errorf("unrelated field lost in the fallback path:\n%s", out)
	}
}

func TestTransformFallsBackForDottedProviderKeys(t *testing.T) {
	base := "http://127.0.0.1:12600"
	original := "approval_policy = 'never'\nmodel_providers.ai-gateway.name = 'old'\n"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	out, err := Transform([]byte(original), base, settings, `C:\Users\test\.codex\ai-gateway-catalog.json`)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parse(out)
	if err != nil {
		t.Fatalf("dotted-key fallback produced invalid TOML: %v\n%s", err, out)
	}
	provider, ok := providerBlock(doc)
	if !ok || provider["base_url"] != base+"/c/codex/v1" {
		t.Fatalf("dotted-key fallback did not write the gateway provider:\n%s", out)
	}
	if !strings.Contains(string(out), "approval_policy = 'never'") {
		t.Fatalf("unrelated field lost in dotted-key fallback:\n%s", out)
	}
}

func TestTransformFallbackPreservesUnknownGatewayProviderFields(t *testing.T) {
	base := "http://127.0.0.1:12600"
	original := "model_providers = { \"ai-gateway\" = { name = \"old\", custom_option = \"keep-me\" } }\n"
	settings := clientcatalog.Settings{PreferredModel: clientcatalog.ReservedModel}
	out, err := Transform([]byte(original), base, settings, `C:\Users\test\.codex\ai-gateway-catalog.json`)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parse(out)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := providerBlock(doc)
	if !ok || p["custom_option"] != "keep-me" {
		t.Fatalf("fallback dropped unknown provider field: %v\n%s", p, out)
	}
}

func TestTransformDoesNotHideParseErrors(t *testing.T) {
	_, err := Transform([]byte("model_providers = ["), "http://127.0.0.1:12600", clientcatalog.Settings{}, `C:\Users\test\.codex\catalog.json`)
	if err == nil {
		t.Fatal("invalid TOML was accepted")
	}
}
