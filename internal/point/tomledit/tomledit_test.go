package tomledit

import (
	"errors"
	"strings"
	"testing"
)

const sample = `# root comment
model = "gpt-5"
approval_policy = "on-request"   # keep me

[history]
persistence = "save-all"
max_bytes = 10485760

# MCP servers
[mcp_servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp@latest"]

[mcp_servers.context7.env]
CONTEXT7_API_KEY = "redacted"

[model_providers.openai]
name = "OpenAI"
base_url = "https://api.openai.com/v1"
wire_api = "responses"

[tools]
web_search = true
`

func TestSetStringRewritesOnlyTheTarget(t *testing.T) {
	doc, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"model"}, "gateway-default"); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"model_providers", "openai", "base_url"}, "http://127.0.0.1:12600"); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"model_providers", "ai-gateway", "wire_api"}, "responses"); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "approval_policy = \"on-request\"   # keep me") {
		t.Errorf("untouched line changed or its comment was lost:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.context7.env]") {
		t.Errorf("mcp_servers block was disturbed:\n%s", got)
	}
	if !strings.Contains(got, "CONTEXT7_API_KEY = \"redacted\"") {
		t.Errorf("mcp env key was disturbed:\n%s", got)
	}
	if strings.Contains(got, "model = \"gpt-5\"") {
		t.Errorf("target value was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "model = 'gateway-default'") {
		t.Errorf("new value missing:\n%s", got)
	}
	if !strings.Contains(got, "[model_providers.openai]\nname = \"OpenAI\"\nbase_url = 'http://127.0.0.1:12600'\nwire_api = \"responses\"") {
		t.Errorf("existing provider table edited beyond its base_url:\n%s", got)
	}
	if !strings.Contains(got, "[model_providers.ai-gateway]\nwire_api = 'responses'") {
		t.Errorf("new provider table wrong:\n%s", got)
	}
}

func TestFreshDocument(t *testing.T) {
	doc, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"model"}, "gateway-default"); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetStrings([]string{"model_providers", "ai-gateway"}, []KV{
		{Key: "name", Value: "ai-gateway"},
		{Key: "base_url", Value: "http://127.0.0.1:12600/c/codex/v1"},
		{Key: "env_key", Value: "AI_GATEWAY_PLACEHOLDER_KEY"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := "model = 'gateway-default'\n[model_providers.ai-gateway]\nname = 'ai-gateway'\nbase_url = 'http://127.0.0.1:12600/c/codex/v1'\nenv_key = 'AI_GATEWAY_PLACEHOLDER_KEY'\n"
	if string(out) != want {
		t.Errorf("fresh document mismatch:\nwant %q\ngot  %q", want, out)
	}
}

func TestNewRootKeyGoesAboveFirstTableHeader(t *testing.T) {
	doc, err := Parse([]byte("[tools]\nweb_search = true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"model"}, "gateway-default"); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	want := "model = 'gateway-default'\n\n[tools]\nweb_search = true\n"
	if string(out) != want {
		t.Errorf("root key landed below the table header:\n%s", out)
	}
}

func TestDeleteTreePrunesOwnedEntries(t *testing.T) {
	input := "[model]\n[model.ai-gateway]\nmodel = 'gateway-default'\n\n[model.'ai-gateway:stale']\nmodel = 'stale'\n\n[model.grok-4]\nmodel = 'grok-4'\n\n[plugin.sample]\nenabled = true\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.DeleteTree([]string{"model", "ai-gateway:stale"}); err != nil {
		t.Fatal(err)
	}
	if err := doc.DeleteTree([]string{"model", "ai-gateway"}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "stale") {
		t.Errorf("owned table not pruned:\n%s", got)
	}
	if !strings.Contains(got, "[model.grok-4]") || !strings.Contains(got, "[plugin.sample]") {
		t.Errorf("user tables were pruned:\n%s", got)
	}
}

func TestUnsupportedShapesAreRejected(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Document) error
	}{
		{"scalar in the path", func(d *Document) error {
			return d.SetString([]string{"a", "b", "c"}, "x")
		}},
		{"key inside an array of tables", func(d *Document) error {
			return d.SetString([]string{"set", "name"}, "x")
		}},
		{"key holds a table", func(d *Document) error {
			return d.SetString([]string{"a"}, "x")
		}},
		{"delete inside an array of tables", func(d *Document) error {
			return d.DeleteTree([]string{"set"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte("[a]\nb = 1\n\n[[set]]\nname = 'n'\n"))
			if err != nil {
				t.Fatal(err)
			}
			err = tc.edit(doc)
			if !errors.Is(err, ErrUnsupportedShape) {
				t.Fatalf("error = %v, want ErrUnsupportedShape", err)
			}
		})
	}
}

func TestImplicitDottedTableIsRejected(t *testing.T) {
	doc, err := Parse([]byte("model_providers.ai-gateway.name = 'old'\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = doc.SetString([]string{"model_providers", "ai-gateway", "base_url"}, "http://127.0.0.1:12600")
	if !errors.Is(err, ErrUnsupportedShape) {
		t.Fatalf("error = %v, want ErrUnsupportedShape", err)
	}
}

// A key that already exists inside a plain table is replaced in place even when
// its current value is not a string: the caller owns that key.
func TestExistingNonStringValueIsReplaced(t *testing.T) {
	doc, err := Parse([]byte("[a]\nb = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"a", "b"}, "x"); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[a]\nb = 'x'\n" {
		t.Errorf("got %q", out)
	}
}

// Windows configurations can use CRLF. Inserted lines must follow the file's
// own line ending instead of leaving it mixed.
func TestCRLFDocumentKeepsItsLineEndings(t *testing.T) {
	doc, err := Parse([]byte("model = 'gpt-5'\r\n\r\n[tools]\r\nweb_search = true\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"model_provider"}, "ai-gateway"); err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"model_providers", "ai-gateway", "wire_api"}, "responses"); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("output mixes line endings: %q", got)
	}
	want := "model = 'gpt-5'\r\nmodel_provider = 'ai-gateway'\r\n\r\n[tools]\r\nweb_search = true\r\n\r\n[model_providers.ai-gateway]\r\nwire_api = 'responses'\r\n"
	if got != want {
		t.Errorf("want %q\ngot  %q", want, got)
	}
}

func TestQuoteStylesSurvive(t *testing.T) {
	// Single-quoted and double-quoted targets must keep their style on rewrite,
	// and unrelated keys must keep theirs.
	input := "model = 'single'\nother = \"double\"\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetString([]string{"model"}, "gateway-default"); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "'single'") || !strings.Contains(got, "model = 'gateway-default'") {
		t.Errorf("single-quoted target not rewritten in place:\n%s", got)
	}
	if !strings.Contains(got, "other = \"double\"") {
		t.Errorf("double-quoted unrelated key was rewritten:\n%s", got)
	}
}
