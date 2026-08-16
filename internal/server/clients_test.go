package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-gateway/internal/point"
	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/point/codex"
	"ai-gateway/internal/route"
)

func testCodexCatalogLoader() func() ([]byte, error) {
	return func() ([]byte, error) {
		return os.ReadFile(filepath.Join("..", "..", "testdata", "point", "codex-bundled-catalog.json"))
	}
}

func TestClientPointAndRestoreAPI(t *testing.T) {
	tests := []struct {
		client   point.Client
		dir      string
		file     string
		original string
		// Grok holds the catalog in its own file. Codex holds it in the
		// sidecar named by model_catalog_json. Claude reads /v1/models.
		catalogInFile bool
	}{
		{client: point.ClientCodex, dir: ".codex", file: "config.toml", original: "custom = \"kept\"\n"},
		{client: point.ClientClaude, dir: ".claude", file: "settings.json", original: "{\"custom\":\"kept\"}\n"},
		{client: point.ClientGrok, dir: ".grok", file: "config.toml", original: "custom = \"kept\"\n", catalogInFile: true},
	}

	for _, tt := range tests {
		t.Run(string(tt.client), func(t *testing.T) {
			dataRoot := t.TempDir()
			home := filepath.Join(dataRoot, "home")
			target := filepath.Join(home, tt.dir, tt.file)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte(tt.original), 0o600); err != nil {
				t.Fatal(err)
			}

			s := newTestServer(t)
			s.points = point.NewWithOptions(dataRoot, point.Options{
				HomeDir:                 home,
				LookupEnv:               func(string) (string, bool) { return "", false },
				CommandExists:           func(string) bool { return false },
				Environment:             point.NewMemoryEnvironment(),
				LoadCodexBundledCatalog: testCodexCatalogLoader(),
			})
			ts := httptest.NewServer(s.routes())
			defer ts.Close()
			path := "/api/v1/clients/" + string(tt.client)

			resp, body := clientJSON(t, ts, http.MethodGet, path)
			assertPointState(t, resp, body, http.StatusOK, point.StateNotPointed)

			resp, body = clientJSON(t, ts, http.MethodPost, path+"/point")
			assertPointState(t, resp, body, http.StatusOK, point.StatePointed)
			var pointed point.Result
			if err := json.Unmarshal(body, &pointed); err != nil {
				t.Fatal(err)
			}
			if !pointed.Changed || pointed.BackupDir == "" {
				t.Fatalf("point result = %+v", pointed)
			}
			pointedConfig, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(pointedConfig), "gateway-default") {
				t.Fatalf("%s config lost the provider-neutral preferred model:\n%s", tt.client, pointedConfig)
			}
			if got := strings.Contains(string(pointedConfig), "openrouter/anthropic/claude-sonnet-4"); got != tt.catalogInFile {
				t.Fatalf("%s catalog in config = %v, want %v:\n%s", tt.client, got, tt.catalogInFile, pointedConfig)
			}
			if tt.client == point.ClientCodex {
				sidecar, err := os.ReadFile(codex.CatalogPath(target))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(sidecar), "openrouter/anthropic/claude-sonnet-4") {
					t.Fatalf("Codex sidecar missing catalog id:\n%s", sidecar)
				}
			}
			pointedBytes := string(pointedConfig)

			resp, body = clientJSON(t, ts, http.MethodPost, path+"/point")
			assertPointState(t, resp, body, http.StatusOK, point.StatePointed)
			var idempotent point.Result
			if err := json.Unmarshal(body, &idempotent); err != nil {
				t.Fatal(err)
			}
			if idempotent.Changed {
				t.Fatal("second point unexpectedly changed client config")
			}

			resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/routes/"+string(tt.client), RouteRequest{Provider: "ollama", Model: "qwen3"})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("route update = %d, body = %s", resp.StatusCode, body)
			}
			pointedConfig, err = os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(pointedConfig) != pointedBytes {
				t.Fatalf("%s route update rewrote the pointed client config:\n%s", tt.client, pointedConfig)
			}

			resp, body = clientJSON(t, ts, http.MethodGet, "/api/v1/status")
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"point_state":"pointed"`) {
				t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
			}

			resp, body = clientJSON(t, ts, http.MethodGet, "/api/v1/doctor")
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"point_state":"pointed"`) {
				t.Fatalf("doctor = %d, body = %s", resp.StatusCode, body)
			}

			resp, body = clientJSON(t, ts, http.MethodPost, path+"/restore")
			assertPointState(t, resp, body, http.StatusOK, point.StateNotPointed)
			restored, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(restored) != tt.original {
				t.Fatalf("restored bytes = %q, want %q", restored, tt.original)
			}
		})
	}
}

// route resolves the reserved model, internal/point writes it into client
// configurations, and the two constants live in different packages because
// internal/point must not import the router. If they ever drift, every pointed
// client would send a model the router treats as a passthrough model id
// (docs/v1-scheme.md §7.3, §7.4).
func TestReservedModelNamesAgree(t *testing.T) {
	if clientcatalog.ReservedModel != route.ReservedModel {
		t.Fatalf("clientcatalog.ReservedModel = %q, route.ReservedModel = %q",
			clientcatalog.ReservedModel, route.ReservedModel)
	}
	if clientcatalog.ReservedDisplayName == "" {
		t.Fatal("reserved model has no display name; §7.5 requires every catalog entry to carry one")
	}
}

func TestClientAPIErrorCodes(t *testing.T) {
	s := httptest.NewServer(newTestServer(t).routes())
	defer s.Close()

	resp, body := clientJSON(t, s, http.MethodGet, "/api/v1/clients/unknown")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"code":"client_not_found"`) {
		t.Fatalf("unknown client = %d, body = %s", resp.StatusCode, body)
	}
	resp, body = clientJSON(t, s, http.MethodPost, "/api/v1/clients/codex/point")
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), `"code":"client_not_installed"`) {
		t.Fatalf("missing client = %d, body = %s", resp.StatusCode, body)
	}
	resp, body = clientJSON(t, s, http.MethodPost, "/api/v1/clients/codex/restore")
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), `"code":"no_restore_available"`) {
		t.Fatalf("missing restore = %d, body = %s", resp.StatusCode, body)
	}
}

func TestConfigAPIRouteUpdateKeepsPointedClientProviderNeutral(t *testing.T) {
	dataRoot := t.TempDir()
	home := filepath.Join(dataRoot, "home")
	target := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("custom = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t)
	s.points = point.NewWithOptions(dataRoot, point.Options{
		HomeDir:                 home,
		LookupEnv:               func(string) (string, bool) { return "", false },
		CommandExists:           func(string) bool { return false },
		Environment:             point.NewMemoryEnvironment(),
		LoadCodexBundledCatalog: testCodexCatalogLoader(),
	})
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	resp, body := clientJSON(t, ts, http.MethodPost, "/api/v1/clients/codex/point")
	assertPointState(t, resp, body, http.StatusOK, point.StatePointed)
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	resp, body = clientJSON(t, ts, http.MethodGet, "/api/v1/config")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config = %d, body = %s", resp.StatusCode, body)
	}
	var payload ConfigPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	payload.Routes.Codex = RouteStatus{Provider: "ollama", Model: "qwen3"}
	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/config", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config = %d, body = %s", resp.StatusCode, body)
	}
	updated, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != string(before) || !strings.Contains(string(updated), "gateway-default") {
		t.Fatalf("Codex provider-neutral config changed after route update:\n%s", updated)
	}
}

func TestAvailabilityUpdateRewritesCodexCatalog(t *testing.T) {
	dataRoot := t.TempDir()
	home := filepath.Join(dataRoot, "home")
	target := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t)
	s.points = point.NewWithOptions(dataRoot, point.Options{
		HomeDir:                 home,
		LookupEnv:               func(string) (string, bool) { return "", false },
		CommandExists:           func(string) bool { return false },
		Environment:             point.NewMemoryEnvironment(),
		LoadCodexBundledCatalog: testCodexCatalogLoader(),
	})
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	resp, body := clientJSON(t, ts, http.MethodPost, "/api/v1/clients/codex/point")
	assertPointState(t, resp, body, http.StatusOK, point.StatePointed)
	sidecar, err := os.ReadFile(codex.CatalogPath(target))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sidecar), "openrouter/anthropic/claude-sonnet-4") {
		t.Fatalf("sidecar missing enabled model:\n%s", sidecar)
	}
	backupsBefore, _ := filepath.Glob(filepath.Join(dataRoot, "backups", "codex", "*", "manifest.json"))

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/providers/openrouter/availability",
		map[string]any{"models": map[string]bool{"anthropic/claude-sonnet-4": false}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("availability = %d %s", resp.StatusCode, body)
	}
	sidecar, err = os.ReadFile(codex.CatalogPath(target))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sidecar), "openrouter/anthropic/claude-sonnet-4") {
		t.Fatalf("disabled model still in Codex sidecar:\n%s", sidecar)
	}
	if !strings.Contains(string(sidecar), "gateway-default") {
		t.Fatal("sidecar lost gateway-default")
	}
	backupsAfter, _ := filepath.Glob(filepath.Join(dataRoot, "backups", "codex", "*", "manifest.json"))
	if len(backupsAfter) != len(backupsBefore) {
		t.Fatal("availability change created a new restore point")
	}
}

func clientJSON(t *testing.T, server *httptest.Server, method, path string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp, body
}

func assertPointState(t *testing.T, resp *http.Response, body []byte, wantCode int, wantState point.State) {
	t.Helper()
	if resp.StatusCode != wantCode {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var status point.Status
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	if status.PointState != wantState {
		t.Fatalf("point_state = %q, want %q; body = %s", status.PointState, wantState, body)
	}
}
