package server

import (
	"bytes"
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
			if got := strings.Contains(string(pointedConfig), "openrouter/default/anthropic/claude-sonnet-4"); got != tt.catalogInFile {
				t.Fatalf("%s catalog in config = %v, want %v:\n%s", tt.client, got, tt.catalogInFile, pointedConfig)
			}
			if tt.client == point.ClientCodex {
				sidecar, err := os.ReadFile(codex.CatalogPath(target))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(sidecar), "openrouter/default/anthropic/claude-sonnet-4") {
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

			resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/routes/"+string(tt.client), RouteRequest{Provider: "ollama", KeyID: "default", Model: "qwen3"})
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

func TestClaudeDesktopPointRouteAndRestoreLeavesMCPUntouched(t *testing.T) {
	dataRoot := t.TempDir()
	localAppData := filepath.Join(dataRoot, "localappdata")
	desktopRoot := filepath.Join(localAppData, "Claude")
	profileDir := filepath.Join(desktopRoot, "configLibrary")
	profileID := "existing-profile"
	profilePath := filepath.Join(profileDir, profileID+".json")
	metaPath := filepath.Join(profileDir, "_meta.json")
	controlPath := filepath.Join(desktopRoot, "claude_desktop_config.json")
	profileOriginal := []byte(`{"custom":{"keep":true},"modelDiscoveryEnabled":true}`)
	metaOriginal := []byte(`{"appliedId":"existing-profile","entries":[{"id":"existing-profile","name":"Personal"}],"custom":true}`)
	controlOriginal := []byte(`{"mcpServers":{"demo":{"command":"node"}},"custom":1}`)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{profilePath: profileOriginal, metaPath: metaOriginal, controlPath: controlOriginal} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	s := newTestServer(t)
	s.points = point.NewWithOptions(dataRoot, point.Options{
		HomeDir: filepath.Join(dataRoot, "home"),
		LookupEnv: func(name string) (string, bool) {
			if name == "LOCALAPPDATA" {
				return localAppData, true
			}
			return "", false
		},
		CommandExists:           func(string) bool { return false },
		Environment:             point.NewMemoryEnvironment(),
		LoadCodexBundledCatalog: testCodexCatalogLoader(),
	})
	ts := httptest.NewServer(s.routes())
	defer ts.Close()
	clientPath := "/api/v1/clients/claude-desktop"

	resp, body := clientJSON(t, ts, http.MethodGet, clientPath)
	assertPointState(t, resp, body, http.StatusOK, point.StateNotPointed)
	var initial point.Status
	if err := json.Unmarshal(body, &initial); err != nil {
		t.Fatal(err)
	}
	resp, body = clientJSON(t, ts, http.MethodPost, clientPath+"/point")
	assertPointState(t, resp, body, http.StatusOK, point.StatePointed)
	var pointed point.Result
	if err := json.Unmarshal(body, &pointed); err != nil {
		t.Fatal(err)
	}
	if !pointed.Changed || pointed.BackupDir == "" {
		t.Fatalf("point result = %+v", pointed)
	}
	pointedProfile, err := os.ReadFile(pointed.Target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pointedProfile), `"inferenceProvider": "gateway"`) {
		t.Fatalf("profile was not transformed: %s", pointedProfile)
	}
	pointedControl, err := os.ReadFile(controlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pointedControl, controlOriginal) {
		t.Fatalf("MCP configuration changed during point: %q, want %q", pointedControl, controlOriginal)
	}
	backupsBefore, _ := filepath.Glob(filepath.Join(dataRoot, "backups", string(point.ClientClaudeDesktop), "*", "manifest.json"))
	if len(backupsBefore) != 1 {
		t.Fatalf("backup count after point = %d, want 1", len(backupsBefore))
	}
	manifest, err := os.ReadFile(backupsBefore[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "claude_desktop_config.json") {
		t.Fatalf("MCP configuration was included in the backup manifest: %s", manifest)
	}

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/routes/claude-desktop", RouteRequest{Provider: "ollama", KeyID: "default", Model: "qwen3"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Claude Desktop route update = %d, body = %s", resp.StatusCode, body)
	}
	updatedProfile, err := os.ReadFile(pointed.Target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updatedProfile), `"labelOverride":"ollama/default/qwen3"`) {
		t.Fatalf("route update did not sync profile: %s", updatedProfile)
	}
	backupsAfter, _ := filepath.Glob(filepath.Join(dataRoot, "backups", string(point.ClientClaudeDesktop), "*", "manifest.json"))
	if len(backupsAfter) != len(backupsBefore) {
		t.Fatalf("route update created a new restore point: before=%d after=%d", len(backupsBefore), len(backupsAfter))
	}
	if got := s.cfg.View().Routes.ClaudeDesktop; got.Provider != "ollama" || got.Model != "qwen3" {
		t.Fatalf("persisted Claude Desktop route = %+v", got)
	}

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPost, clientPath+"/restore", nil)
	assertPointState(t, resp, body, http.StatusOK, point.StateNotPointed)
	if restored, err := os.ReadFile(profilePath); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(restored, profileOriginal) {
		t.Fatalf("original Claude Desktop profile bytes = %q, want %q", restored, profileOriginal)
	}
	if restored, err := os.ReadFile(controlPath); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(restored, controlOriginal) {
		t.Fatalf("MCP configuration changed during restore: %q, want %q", restored, controlOriginal)
	}
}

// route resolves the reserved model, internal/point writes it into client
// configurations, and the two constants live in different packages because
// internal/point must not import the router. If they ever drift, every pointed
// client would send a model the router cannot attribute to a provider
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
	payload.Routes.Codex = RouteStatus{Provider: "ollama", KeyID: "default", Model: "qwen3"}
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
	if !strings.Contains(string(sidecar), "openrouter/default/anthropic/claude-sonnet-4") {
		t.Fatalf("sidecar missing enabled model:\n%s", sidecar)
	}
	backupsBefore, _ := filepath.Glob(filepath.Join(dataRoot, "backups", "codex", "*", "manifest.json"))

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/providers/openrouter/availability",
		map[string]any{"key_groups": map[string]any{"default": map[string]any{"models": map[string]bool{"enabled-model": false}}}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("availability = %d %s", resp.StatusCode, body)
	}
	sidecar, err = os.ReadFile(codex.CatalogPath(target))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sidecar), "openrouter/default/enabled-model") {
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

func TestCodexRemoteCompactionAPI(t *testing.T) {
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

	resp, body := clientJSON(t, ts, http.MethodGet, "/api/v1/clients/codex")
	assertPointState(t, resp, body, http.StatusOK, point.StateNotPointed)
	var before point.Status
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if before.RemoteCompaction == nil || *before.RemoteCompaction {
		t.Fatalf("default remote_compaction = %+v", before.RemoteCompaction)
	}

	resp, body = clientJSON(t, ts, http.MethodPost, "/api/v1/clients/codex/point")
	assertPointState(t, resp, body, http.StatusOK, point.StatePointed)
	backupsBefore, _ := filepath.Glob(filepath.Join(dataRoot, "backups", "codex", "*", "manifest.json"))

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/clients/codex/remote-compaction", map[string]any{"enabled": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable remote compaction = %d %s", resp.StatusCode, body)
	}
	var enabled point.Status
	if err := json.Unmarshal(body, &enabled); err != nil {
		t.Fatal(err)
	}
	if enabled.RemoteCompaction == nil || !*enabled.RemoteCompaction {
		t.Fatalf("enabled remote_compaction = %+v", enabled.RemoteCompaction)
	}
	if enabled.PointState != point.StatePointed {
		t.Fatalf("state after enable = %s", enabled.PointState)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "OpenAI") {
		t.Fatalf("pointed Codex config missing OpenAI name:\n%s", data)
	}
	backupsAfter, _ := filepath.Glob(filepath.Join(dataRoot, "backups", "codex", "*", "manifest.json"))
	if len(backupsAfter) != len(backupsBefore) {
		t.Fatal("remote compaction toggle created a new restore point")
	}

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/clients/claude/remote-compaction", map[string]any{"enabled": true})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("claude remote compaction = %d %s", resp.StatusCode, body)
	}
}

func TestClientHelperModelsAPI(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.routes())
	defer ts.Close()

	selected := "openrouter/default/anthropic/claude-sonnet-4"
	resp, body := httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/clients/codex/helper-models", map[string]any{
		"subagent_model": selected,
		"title_model":    "ollama/default/qwen3",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set Codex helper models = %d %s", resp.StatusCode, body)
	}
	var status point.Status
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	if status.SubagentModel != selected || status.TitleModel != "ollama/default/qwen3" {
		t.Fatalf("Codex helper models = %+v", status)
	}
	cfg := s.cfg.View()
	if cfg.Clients.Codex.SubagentModel != selected || cfg.Clients.Codex.TitleModel != "ollama/default/qwen3" {
		t.Fatalf("persisted Codex settings = %+v", cfg.Clients.Codex)
	}

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/clients/claude/helper-models", map[string]any{
		"subagent_model": "gateway-default",
		"title_model":    "",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set Claude helper models = %d %s", resp.StatusCode, body)
	}
	if cfg = s.cfg.View(); cfg.Clients.Claude.SubagentModel != "" || cfg.Clients.Claude.TitleModel != "" {
		t.Fatalf("gateway-default was not normalized: %+v", cfg.Clients.Claude)
	}

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/clients/codex/helper-models", map[string]any{
		"subagent_model": "missing/model",
		"title_model":    "ollama/default/qwen3",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid helper model = %d %s", resp.StatusCode, body)
	}

	resp, body = httpJSON(t, ts.Listener.Addr().String(), http.MethodPut, "/api/v1/clients/grok/helper-models", map[string]any{
		"subagent_model": "",
		"title_model":    "",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Grok helper models = %d %s", resp.StatusCode, body)
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
