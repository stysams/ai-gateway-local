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
