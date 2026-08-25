package claudedesktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ai-gateway/internal/point/clientcatalog"
)

func TestTransformProfilePreservesUnknownRootValues(t *testing.T) {
	original := []byte(`{"custom":{"keep":true},"inferenceModels":[],"modelDiscoveryEnabled":true}`)
	settings := clientcatalog.Settings{
		PreferredModel:   routeModel,
		Catalog:          []clientcatalog.Entry{{ID: "openrouter/claude-sonnet-4", DisplayName: "Claude Sonnet 4"}},
		RouteDisplayName: "openrouter/claude-sonnet-4",
	}

	updated, err := TransformProfile(original, "http://127.0.0.1:12600", settings)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatal(err)
	}
	if string(document["custom"]) != `{"keep":true}` {
		t.Fatalf("custom root value changed: %s", document["custom"])
	}
	if string(document["modelDiscoveryEnabled"]) != "false" {
		t.Fatalf("model discovery = %s, want false", document["modelDiscoveryEnabled"])
	}
	if len(Models(settings)) != 5 {
		t.Fatalf("model count = %d, want 5", len(Models(settings)))
	}
	if ok, err := CheckProfile(updated, "http://127.0.0.1:12600", settings); err != nil || !ok {
		t.Fatalf("CheckProfile = %v, %v", ok, err)
	}
}

func TestSelectPrefersProfileRootOverRuntimeDirectories(t *testing.T) {
	localAppData := t.TempDir()
	appData := t.TempDir()
	for _, base := range []string{
		filepath.Join(localAppData, "Claude"),
		filepath.Join(appData, "Claude"),
	} {
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	profileRoot := filepath.Join(localAppData, "Claude-3p")
	profileDir := filepath.Join(profileRoot, ProfileDirName)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, MetaFileName), []byte(`{"appliedId":"profile"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "profile.json"), []byte(`{"inferenceProvider":"other"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	roots, err := Discover(nil, localAppData, appData)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Select(roots, "http://127.0.0.1:12600")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Base != profileRoot {
		t.Fatalf("selected root = %q, want %q", selected.Base, profileRoot)
	}
}

const routeModel = "gateway-default"
