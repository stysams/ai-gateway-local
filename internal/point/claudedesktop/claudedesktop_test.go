package claudedesktop

import (
	"bytes"
	"encoding/json"
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

func TestRestoreControlSeparatesDeploymentModeFromFullMCPRestore(t *testing.T) {
	original := []byte(`{"mcpServers":{"demo":{"command":"node"}},"custom":1}`)
	pointed, err := TransformControl(original)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CheckControl(pointed, true); err != nil || !ok {
		t.Fatalf("CheckControl = %v, %v", ok, err)
	}

	profileRestore, changed, err := RestoreControl(pointed, true, original, true, false)
	if err != nil || !changed {
		t.Fatalf("pointed=%s; profile restore = %v, %v, %v", pointed, profileRestore, changed, err)
	}
	if matches, err := MCPMatches(profileRestore, true, original, true); err != nil || !matches {
		t.Fatalf("MCPMatches after profile restore = %v, %v", matches, err)
	}
	fullRestore, changed, err := RestoreControl(pointed, true, original, true, true)
	if err != nil || !changed || !bytes.Equal(fullRestore, original) {
		t.Fatalf("full restore = %s, %v, %v", fullRestore, changed, err)
	}
}

const routeModel = "gateway-default"
