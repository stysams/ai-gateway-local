package point

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-gateway/internal/point/claude"
	"ai-gateway/internal/point/clientcatalog"
	"ai-gateway/internal/point/codex"
	"ai-gateway/internal/point/grok"
)

const testBaseURL = "http://127.0.0.1:12600"

// The reserved model name is owned by clientcatalog; tests must not re-declare
// it (docs/v1-scheme.md §7.3).
const reservedModel = clientcatalog.ReservedModel

func testBundledCatalog(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "point", "codex-bundled-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testManager(t *testing.T, home string, env UserEnvironment, lookup map[string]string, after func() error) *Manager {
	t.Helper()
	return NewWithOptions(t.TempDir(), Options{
		HomeDir:        home,
		LookupEnv:      func(name string) (string, bool) { v, ok := lookup[name]; return v, ok },
		CommandExists:  func(string) bool { return false },
		Environment:    env,
		Now:            func() time.Time { return time.Date(2026, 8, 15, 6, 0, 0, 123, time.UTC) },
		AfterFileWrite: after,
		LoadCodexBundledCatalog: func() ([]byte, error) {
			return testBundledCatalog(t), nil
		},
	})
}

func clientTarget(home string, client Client, lookup map[string]string) string {
	switch client {
	case ClientCodex:
		if lookup["CODEX_HOME"] != "" {
			return filepath.Join(lookup["CODEX_HOME"], "config.toml")
		}
		return filepath.Join(home, ".codex", "config.toml")
	case ClientClaude:
		dir := filepath.Join(home, ".claude")
		if lookup["CLAUDE_CONFIG_DIR"] != "" {
			dir = lookup["CLAUDE_CONFIG_DIR"]
		}
		return filepath.Join(dir, "settings.json")
	default:
		dir := filepath.Join(home, ".grok")
		if lookup["GROK_HOME"] != "" {
			dir = lookup["GROK_HOME"]
		}
		return filepath.Join(dir, "config.toml")
	}
}

func TestPointRestorePreservesOriginalBytesUnknownFieldsAndEnvironment(t *testing.T) {
	cases := []struct {
		client  Client
		fixture string
		lookup  func(string) map[string]string
		check   func([]byte) (bool, error)
		unknown string
	}{
		{ClientCodex, "codex-existing.toml", nil, func(b []byte) (bool, error) { return codex.Check(b, testBaseURL, Settings{}) }, "approval_policy"},
		{ClientClaude, "claude-existing.json", func(home string) map[string]string {
			return map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, "claude-custom")}
		}, func(b []byte) (bool, error) { return claude.Check(b, testBaseURL, Settings{}) }, "KEEP_ME"},
		{ClientGrok, "grok-existing.toml", func(home string) map[string]string {
			return map[string]string{"GROK_HOME": filepath.Join(home, "grok-custom")}
		}, func(b []byte) (bool, error) { return grok.Check(b, testBaseURL, Settings{}) }, "plugin"},
	}
	for _, tc := range cases {
		t.Run(string(tc.client), func(t *testing.T) {
			home := t.TempDir()
			lookup := map[string]string{}
			if tc.lookup != nil {
				lookup = tc.lookup(home)
			}
			target := clientTarget(home, tc.client, lookup)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(filepath.Join("..", "..", "testdata", "point", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, original, 0o600); err != nil {
				t.Fatal(err)
			}
			env := NewMemoryEnvironment()
			if tc.client == ClientCodex {
				env.Values[codex.PlaceholderEnvironment] = "original-placeholder"
			}
			m := testManager(t, home, env, lookup, nil)
			result, err := m.Point(tc.client, testBaseURL, Settings{})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Changed || result.PointState != StatePointed || result.BackupDir == "" {
				t.Fatalf("point result = %+v", result)
			}
			modified, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			ok, err := tc.check(modified)
			if err != nil || !ok {
				t.Fatalf("pointed content invalid: ok=%v err=%v\n%s", ok, err, modified)
			}
			if !strings.Contains(string(modified), tc.unknown) {
				t.Fatalf("unknown setting lost:\n%s", modified)
			}
			manifest, err := readManifest(filepath.Join(result.BackupDir, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(original)
			if !manifest.Completed || manifest.Files[0].OriginalSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("manifest = %+v", manifest)
			}
			backupsBefore, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", string(tc.client), "*", "manifest.json"))
			second, err := m.Point(tc.client, testBaseURL, Settings{})
			if err != nil {
				t.Fatal(err)
			}
			backupsAfter, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", string(tc.client), "*", "manifest.json"))
			if second.Changed || len(backupsAfter) != len(backupsBefore) {
				t.Fatalf("idempotent point created backup: result=%+v before=%d after=%d", second, len(backupsBefore), len(backupsAfter))
			}
			modified = []byte(strings.Replace(string(modified), testBaseURL, "http://127.0.0.1:19999", 1))
			if err := os.WriteFile(target, modified, 0o600); err != nil {
				t.Fatal(err)
			}
			if state := m.Check(tc.client, testBaseURL, Settings{}).PointState; state != StateDrifted {
				t.Fatalf("manual edit state = %s", state)
			}
			restored, err := m.Restore(tc.client, testBaseURL, Settings{})
			if err != nil {
				t.Fatal(err)
			}
			if !restored.Changed {
				t.Fatalf("restore result = %+v", restored)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(original) {
				t.Fatalf("restore did not recover exact bytes:\nwant %q\ngot  %q", original, got)
			}
			if tc.client == ClientCodex && env.Values[codex.PlaceholderEnvironment] != "original-placeholder" {
				t.Fatalf("environment not restored: %v", env.Values)
			}
			manifest, _ = readManifest(filepath.Join(result.BackupDir, "manifest.json"))
			if manifest.RestoredAt == nil {
				t.Fatal("manifest not marked restored")
			}
			if _, err := m.Restore(tc.client, testBaseURL, Settings{}); !errors.Is(err, ErrNoRestore) {
				t.Fatalf("second restore error = %v", err)
			}
		})
	}
}

// A point owns the model and routing keys and nothing else. MCP servers, tool
// tables, hooks, permissions, plugin and profile blocks — and the comments,
// key order and quoting style around them — must come back out of the
// transaction byte-for-byte (docs/v1-scheme.md §12.1 and §12.5, 2026-08-21
// evidence in §20).
func TestPointRewritesOnlyModelAndRoutingLines(t *testing.T) {
	settings := Settings{PreferredModel: reservedModel, Catalog: []clientcatalog.Entry{
		{ID: "example/model-a", DisplayName: "Model A"},
	}}
	cases := []struct {
		client  Client
		fixture string
		// rewritten is the exact set of fixture lines a point is allowed to
		// change. Every other line must survive verbatim and in order.
		rewritten []string
	}{
		{ClientCodex, "codex-tooling.toml", []string{
			`model = "gpt-5-codex"`,
			`model_provider = "openai"`,
		}},
		{ClientGrok, "grok-tooling.toml", []string{
			`default = "grok-4"`,
		}},
		{ClientClaude, "claude-tooling.json", []string{
			`    "ANTHROPIC_MODEL": "example-old-model"`,
		}},
	}
	for _, tc := range cases {
		t.Run(string(tc.client), func(t *testing.T) {
			home := t.TempDir()
			target := clientTarget(home, tc.client, nil)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(filepath.Join("..", "..", "testdata", "point", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, original, 0o600); err != nil {
				t.Fatal(err)
			}
			m := testManager(t, home, NewMemoryEnvironment(), map[string]string{}, nil)
			result, err := m.Point(tc.client, testBaseURL, settings)
			if err != nil {
				t.Fatal(err)
			}
			if result.PointState != StatePointed {
				t.Fatalf("point result = %+v", result)
			}
			modified, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			kept, dropped := diffLines(string(original), string(modified))
			if !equalStrings(dropped, tc.rewritten) {
				t.Errorf("rewritten lines = %q, want %q\n%s", dropped, tc.rewritten, modified)
			}
			if len(kept)+len(dropped) != len(strings.Split(string(original), "\n")) {
				t.Fatalf("diff lost lines: kept=%d dropped=%d", len(kept), len(dropped))
			}
		})
	}
}

// diffLines aligns original against modified on their longest common
// subsequence of whole lines. kept is what survived with its exact bytes and
// its position; dropped is every original line the alignment could not match,
// which is precisely the set a point was allowed to rewrite.
func diffLines(original, modified string) (kept, dropped []string) {
	src := splitLinesForComparison(original)
	dst := splitLinesForComparison(modified)
	// lengths[i][j] is the LCS length of src[i:] and dst[j:].
	lengths := make([][]int, len(src)+1)
	for i := range lengths {
		lengths[i] = make([]int, len(dst)+1)
	}
	for i := len(src) - 1; i >= 0; i-- {
		for j := len(dst) - 1; j >= 0; j-- {
			if src[i] == dst[j] {
				lengths[i][j] = lengths[i+1][j+1] + 1
				continue
			}
			lengths[i][j] = max(lengths[i+1][j], lengths[i][j+1])
		}
	}
	i, j := 0, 0
	for i < len(src) {
		switch {
		case j < len(dst) && src[i] == dst[j]:
			kept = append(kept, src[i])
			i++
			j++
		case j < len(dst) && lengths[i][j+1] > lengths[i+1][j]:
			j++
		default:
			dropped = append(dropped, src[i])
			i++
		}
	}
	return kept, dropped
}

// splitLinesForComparison removes only the line-ending marker. The point
// contract intentionally preserves LF versus CRLF, while this test compares
// which logical lines were rewritten and must therefore run identically on
// either checkout style.
func splitLinesForComparison(data string) []string {
	lines := strings.Split(data, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPointAndRestoreWhenOriginalFileDoesNotExist(t *testing.T) {
	for _, client := range []Client{ClientCodex, ClientClaude, ClientGrok} {
		t.Run(string(client), func(t *testing.T) {
			home := t.TempDir()
			lookup := map[string]string{}
			target := clientTarget(home, client, lookup)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			m := testManager(t, home, NewMemoryEnvironment(), lookup, nil)
			result, err := m.Point(client, testBaseURL, Settings{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("point did not create target: %v", err)
			}
			manifest, _ := readManifest(filepath.Join(result.BackupDir, "manifest.json"))
			if manifest.Files[0].OriginalExists {
				t.Fatal("manifest claims nonexistent original existed")
			}
			if _, err := m.Restore(client, testBaseURL, Settings{}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("restore did not remove created file: %v", err)
			}
			if client == ClientCodex {
				if _, err := os.Stat(codex.CatalogPath(target)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("restore left Codex catalog sidecar")
				}
			}
			if client == ClientClaude {
				if _, err := os.Stat(claude.CachePath(target)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("restore left Claude gateway-models cache")
				}
			}
		})
	}
}

// A catalog change must be applied to an already pointed configuration in
// place. Re-pointing would create a second restore point whose "original" is
// the gateway's own output, permanently losing the user's real configuration
// (docs/v1-scheme.md §12.1).
func TestPointAppliesCatalogChangeWithoutReplacingOriginalBackup(t *testing.T) {
	catalog := Settings{PreferredModel: reservedModel, Catalog: []clientcatalog.Entry{
		{ID: "openrouter/anthropic/claude-sonnet-4", DisplayName: "Claude Sonnet 4"},
		{ID: "deepseek/deepseek-chat"},
	}}
	for _, client := range []Client{ClientCodex, ClientClaude, ClientGrok} {
		t.Run(string(client), func(t *testing.T) {
			home := t.TempDir()
			target := clientTarget(home, client, nil)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			original := []byte("custom = true\n")
			if client == ClientClaude {
				original = []byte("{\"custom\":true}\n")
			}
			if err := os.WriteFile(target, original, 0o600); err != nil {
				t.Fatal(err)
			}
			m := testManager(t, home, NewMemoryEnvironment(), map[string]string{}, nil)
			first, err := m.Point(client, testBaseURL, Settings{PreferredModel: reservedModel})
			if err != nil {
				t.Fatal(err)
			}
			manifestsBefore, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", string(client), "*", "manifest.json"))
			updated, err := m.Point(client, testBaseURL, catalog)
			if err != nil {
				t.Fatal(err)
			}
			manifestsAfter, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", string(client), "*", "manifest.json"))
			if updated.BackupDir != "" || len(manifestsAfter) != len(manifestsBefore) {
				t.Fatalf("catalog change replaced the restore point: first=%+v updated=%+v before=%d after=%d", first, updated, len(manifestsBefore), len(manifestsAfter))
			}
			if updated.PointState != StatePointed {
				t.Fatalf("state after catalog change = %s", updated.PointState)
			}
			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			// Grok holds the catalog in config.toml. Codex holds it in the
			// sidecar named by model_catalog_json. Claude holds it in
			// cache/gateway-models.json, not settings.json.
			switch client {
			case ClientGrok:
				if !strings.Contains(string(data), "deepseek/deepseek-chat") {
					t.Fatalf("Grok catalog missing from file:\n%s", data)
				}
				if !updated.Changed {
					t.Fatalf("catalog change was not applied: %+v", updated)
				}
			case ClientCodex:
				if !strings.Contains(string(data), "model_catalog_json") {
					t.Fatalf("Codex config missing model_catalog_json:\n%s", data)
				}
				sidecar, err := os.ReadFile(codex.CatalogPath(target))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(sidecar), "deepseek/deepseek-chat") {
					t.Fatalf("Codex sidecar missing catalog id:\n%s", sidecar)
				}
				if !updated.Changed {
					t.Fatalf("Codex catalog change was not applied: %+v", updated)
				}
			default:
				if strings.Contains(string(data), "deepseek/deepseek-chat") {
					t.Fatalf("Claude config unexpectedly contains catalog id:\n%s", data)
				}
				cache, err := os.ReadFile(claude.CachePath(target))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(cache), "deepseek/deepseek-chat") {
					t.Fatalf("Claude picker cache missing catalog id:\n%s", cache)
				}
				if !updated.Changed {
					t.Fatalf("Claude catalog change was not applied: %+v", updated)
				}
			}
			if _, err := m.Restore(client, testBaseURL, catalog); err != nil {
				t.Fatal(err)
			}
			restored, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(restored) != string(original) {
				t.Fatalf("restore after catalog change = %q, want %q", restored, original)
			}
		})
	}
}

// A catalog that shrinks must not leave stale rows in the Grok picker, and
// models the user declared themselves must survive both point and restore.
func TestGrokCatalogShrinksAndPreservesUserModels(t *testing.T) {
	home := t.TempDir()
	target := clientTarget(home, ClientGrok, nil)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("[model.\"my-own\"]\nmodel = \"my-own\"\nbase_url = \"http://example.invalid/v1\"\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, home, NewMemoryEnvironment(), map[string]string{}, nil)
	wide := Settings{PreferredModel: reservedModel, Catalog: []clientcatalog.Entry{
		{ID: "openrouter/anthropic/claude-sonnet-4"},
		{ID: "deepseek/deepseek-chat"},
	}}
	if _, err := m.Point(ClientGrok, testBaseURL, wide); err != nil {
		t.Fatal(err)
	}
	narrow := Settings{PreferredModel: reservedModel, Catalog: []clientcatalog.Entry{{ID: "deepseek/deepseek-chat"}}}
	changed, err := m.SyncSettings(ClientGrok, testBaseURL, narrow)
	if err != nil || !changed {
		t.Fatalf("shrink sync changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "openrouter/anthropic/claude-sonnet-4") {
		t.Fatalf("removed model still present in picker:\n%s", data)
	}
	if !strings.Contains(string(data), "my-own") {
		t.Fatalf("user declared model was dropped:\n%s", data)
	}
	if state := m.Check(ClientGrok, testBaseURL, narrow).PointState; state != StatePointed {
		t.Fatalf("state after shrink = %s", state)
	}
	// The wider catalog must now read as drifted, otherwise a stale picker would
	// silently pass verification.
	if state := m.Check(ClientGrok, testBaseURL, wide).PointState; state == StatePointed {
		t.Fatal("stale catalog reported as pointed")
	}
	if _, err := m.Restore(ClientGrok, testBaseURL, narrow); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != string(original) {
		t.Fatalf("restore = %q, want %q", restored, original)
	}
}

// Each client expresses the catalog differently. Codex writes a sidecar
// cloned from the bundled template; Claude only enables discovery; Grok
// writes native [model] tables (docs/v1-scheme.md §12.3–§12.5).
func TestPointWritesPerClientCatalogContract(t *testing.T) {
	settings := Settings{PreferredModel: reservedModel, Catalog: []clientcatalog.Entry{
		{ID: "openrouter/anthropic/claude-sonnet-4", DisplayName: "Claude Sonnet 4"},
	}}
	cases := []struct {
		client Client
		want   []string
		reject []string
	}{
		{
			client: ClientCodex,
			want:   []string{"model = 'gateway-default'", "model_provider = 'ai-gateway'", "model_catalog_json", "ai-gateway-catalog.json"},
			reject: []string{"base_instructions"},
		},
		{
			client: ClientClaude,
			want: []string{
				"\"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY\": \"1\"",
				"\"ANTHROPIC_MODEL\": \"gateway-default\"",
				"\"ANTHROPIC_DEFAULT_OPUS_MODEL\": \"gateway-default\"",
				"\"ANTHROPIC_DEFAULT_SONNET_MODEL\": \"gateway-default\"",
				"\"ANTHROPIC_DEFAULT_HAIKU_MODEL\": \"gateway-default\"",
			},
			reject: []string{"claude-gw-", "openrouter/anthropic/claude-sonnet-4"},
		},
		{
			client: ClientGrok,
			want: []string{
				"[model.'ai-gateway:openrouter/anthropic/claude-sonnet-4']",
				"model = 'openrouter/anthropic/claude-sonnet-4'",
				"name = 'openrouter/anthropic/claude-sonnet-4'",
				"default = 'ai-gateway'",
			},
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.client), func(t *testing.T) {
			home := t.TempDir()
			target := clientTarget(home, tc.client, nil)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			m := testManager(t, home, NewMemoryEnvironment(), map[string]string{}, nil)
			if _, err := m.Point(tc.client, testBaseURL, settings); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(data), want) {
					t.Fatalf("%s config missing %q:\n%s", tc.client, want, data)
				}
			}
			for _, reject := range tc.reject {
				if strings.Contains(string(data), reject) {
					t.Fatalf("%s config unexpectedly contains %q:\n%s", tc.client, reject, data)
				}
			}
			if tc.client == ClientCodex {
				sidecar, err := os.ReadFile(codex.CatalogPath(target))
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{
					`"slug":"gateway-default"`,
					`"slug":"openrouter/anthropic/claude-sonnet-4"`,
					`"display_name":"openrouter/anthropic/claude-sonnet-4"`,
					"SENTINEL_BUNDLED_BASE_INSTRUCTIONS_FOR_TESTS_ONLY",
				} {
					if !strings.Contains(string(sidecar), want) {
						t.Fatalf("Codex sidecar missing %q:\n%s", want, sidecar)
					}
				}
				if strings.Contains(string(sidecar), "MUST_BE_REMOVED_FROM_ROUTED_ENTRIES") {
					t.Fatalf("Codex sidecar kept model_messages:\n%s", sidecar)
				}
			}
			if tc.client == ClientClaude {
				cache, err := os.ReadFile(claude.CachePath(target))
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{
					`"baseUrl":"http://127.0.0.1:12600/c/claude"`,
					`"id":"claude-gw-default"`,
					`"display_name":"gateway-default"`,
					`"id":"claude-gw2-openrouter--anthropic~sclaude-sonnet-4"`,
					`"display_name":"openrouter/anthropic/claude-sonnet-4"`,
				} {
					if !strings.Contains(string(cache), want) {
						t.Fatalf("Claude picker cache missing %q:\n%s", want, cache)
					}
				}
			}
		})
	}
}

// OpenCodex pre-writes ~/.claude/cache/gateway-models.json. Point must
// replace that picker list with this gateway's aliases, and restore must
// give the original bytes back. A later catalog shrink must rewrite the
// cache in place without creating a second restore point.
func TestClaudeGatewayModelCachePointSyncRestore(t *testing.T) {
	home := t.TempDir()
	target := clientTarget(home, ClientClaude, nil)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	originalSettings := []byte("{\"custom\":true,\"env\":{\"KEEP_ME\":\"yes\"}}\n")
	if err := os.WriteFile(target, originalSettings, 0o600); err != nil {
		t.Fatal(err)
	}
	originalCache := []byte(`{"baseUrl":"http://127.0.0.1:10100","fetchedAt":1,"models":[{"id":"claude-ocx-old--model","display_name":"old (opencodex)"}]}`)
	cachePath := claude.CachePath(target)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, originalCache, 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, home, NewMemoryEnvironment(), map[string]string{}, nil)
	wide := Settings{PreferredModel: reservedModel, Catalog: []clientcatalog.Entry{
		{ID: "openrouter/anthropic/claude-sonnet-4"},
		{ID: "deepseek/deepseek-chat"},
	}}
	first, err := m.Point(ClientClaude, testBaseURL, wide)
	if err != nil {
		t.Fatal(err)
	}
	if first.PointState != StatePointed {
		t.Fatalf("state after point = %s", first.PointState)
	}
	cache, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"baseUrl":"http://127.0.0.1:12600/c/claude"`,
		`"id":"claude-gw-default"`,
		`"display_name":"deepseek/deepseek-chat"`,
		`"id":"claude-gw2-openrouter--anthropic~sclaude-sonnet-4"`,
	} {
		if !strings.Contains(string(cache), want) {
			t.Fatalf("pointed cache missing %q:\n%s", want, cache)
		}
	}
	if strings.Contains(string(cache), "claude-ocx-") {
		t.Fatalf("OpenCodex aliases survived point:\n%s", cache)
	}
	settings, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settings), "claude-gw-") {
		t.Fatalf("picker alias leaked into settings.json:\n%s", settings)
	}
	narrow := Settings{PreferredModel: reservedModel, Catalog: []clientcatalog.Entry{{ID: "deepseek/deepseek-chat"}}}
	manifestsBefore, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", "claude", "*", "manifest.json"))
	changed, err := m.SyncSettings(ClientClaude, testBaseURL, narrow)
	if err != nil || !changed {
		t.Fatalf("shrink sync changed=%v err=%v", changed, err)
	}
	manifestsAfter, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", "claude", "*", "manifest.json"))
	if len(manifestsAfter) != len(manifestsBefore) {
		t.Fatal("cache shrink created a new restore point")
	}
	cache, err = os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cache), "openrouter/anthropic/claude-sonnet-4") {
		t.Fatalf("removed model still in picker cache:\n%s", cache)
	}
	if !strings.Contains(string(cache), "deepseek/deepseek-chat") {
		t.Fatalf("remaining model missing from picker cache:\n%s", cache)
	}
	if state := m.Check(ClientClaude, testBaseURL, narrow).PointState; state != StatePointed {
		t.Fatalf("state after shrink = %s", state)
	}
	if state := m.Check(ClientClaude, testBaseURL, wide).PointState; state == StatePointed {
		t.Fatal("stale Claude picker cache reported as pointed")
	}
	if _, err := m.Restore(ClientClaude, testBaseURL, narrow); err != nil {
		t.Fatal(err)
	}
	restoredSettings, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredSettings) != string(originalSettings) {
		t.Fatalf("settings restore = %q, want %q", restoredSettings, originalSettings)
	}
	restoredCache, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredCache) != string(originalCache) {
		t.Fatalf("cache restore = %q, want %q", restoredCache, originalCache)
	}
}

func TestClaudeMissingCacheIsRepairedWithoutNewBackup(t *testing.T) {
	home := t.TempDir()
	target := clientTarget(home, ClientClaude, nil)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, home, NewMemoryEnvironment(), map[string]string{}, nil)
	settings := Settings{PreferredModel: reservedModel, Catalog: []clientcatalog.Entry{{ID: "zhipu/glm-5"}}}
	if _, err := m.Point(ClientClaude, testBaseURL, settings); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(claude.CachePath(target)); err != nil {
		t.Fatal(err)
	}
	if state := m.Check(ClientClaude, testBaseURL, settings).PointState; state != StateDrifted {
		t.Fatalf("missing cache state = %s", state)
	}
	manifestsBefore, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", "claude", "*", "manifest.json"))
	result, err := m.Point(ClientClaude, testBaseURL, settings)
	if err != nil {
		t.Fatal(err)
	}
	manifestsAfter, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", "claude", "*", "manifest.json"))
	if result.BackupDir != "" || len(manifestsAfter) != len(manifestsBefore) {
		t.Fatalf("repairing the picker cache replaced the restore point: %+v", result)
	}
	if result.PointState != StatePointed || !result.Changed {
		t.Fatalf("repair result = %+v", result)
	}
	ok, err := claude.CacheMatches(claude.CachePath(target), testBaseURL, settings)
	if err != nil || !ok {
		t.Fatalf("repaired cache still mismatches: ok=%v err=%v", ok, err)
	}
}

func TestCodexPointFailsWithoutTemplate(t *testing.T) {
	home := t.TempDir()
	target := clientTarget(home, ClientCodex, nil)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	m := NewWithOptions(t.TempDir(), Options{
		HomeDir:                 home,
		LookupEnv:               func(string) (string, bool) { return "", false },
		CommandExists:           func(string) bool { return false },
		Environment:             NewMemoryEnvironment(),
		Now:                     func() time.Time { return time.Date(2026, 8, 15, 6, 0, 0, 123, time.UTC) },
		LoadCodexBundledCatalog: func() ([]byte, error) { return []byte(`{"models":[]}`), nil },
	})
	if _, err := m.Point(ClientCodex, testBaseURL, Settings{}); err == nil {
		t.Fatal("point succeeded without a native template")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed point left config: %v", err)
	}
	if _, err := os.Stat(codex.CatalogPath(target)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed point left sidecar")
	}
}

func TestCodexRemoteCompactionSyncDoesNotReplaceRestorePoint(t *testing.T) {
	home := t.TempDir()
	target := clientTarget(home, ClientCodex, nil)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("approval_policy = 'untrusted'\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, home, NewMemoryEnvironment(), map[string]string{}, nil)
	off := Settings{PreferredModel: reservedModel}
	if _, err := m.Point(ClientCodex, testBaseURL, off); err != nil {
		t.Fatal(err)
	}
	manifestsBefore, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", "codex", "*", "manifest.json"))
	on := Settings{PreferredModel: reservedModel, RemoteCompaction: true}
	changed, err := m.SyncSettings(ClientCodex, testBaseURL, on)
	if err != nil || !changed {
		t.Fatalf("sync remote compaction changed=%v err=%v", changed, err)
	}
	manifestsAfter, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", "codex", "*", "manifest.json"))
	if len(manifestsAfter) != len(manifestsBefore) {
		t.Fatalf("remote compaction sync replaced the restore point: before=%d after=%d", len(manifestsBefore), len(manifestsAfter))
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "OpenAI") {
		t.Fatalf("sync did not write OpenAI name:\n%s", data)
	}
	if state := m.Check(ClientCodex, testBaseURL, on).PointState; state != StatePointed {
		t.Fatalf("state after enabling remote compaction = %s", state)
	}
	if state := m.Check(ClientCodex, testBaseURL, off).PointState; state == StatePointed {
		t.Fatal("default name setting still reported pointed after OpenAI rewrite")
	}
}

func TestCodexHomeOverride(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "custom-codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	lookup := map[string]string{"CODEX_HOME": codexHome}
	m := testManager(t, home, NewMemoryEnvironment(), lookup, nil)
	if _, err := m.Point(ClientCodex, testBaseURL, Settings{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "config.toml")); err != nil {
		t.Fatalf("CODEX_HOME config missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(codexHome, "ai-gateway-catalog.json")); err != nil {
		t.Fatalf("CODEX_HOME sidecar missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrote default ~/.codex despite CODEX_HOME")
	}
}

func TestPointFailureAfterFileWriteRollsBack(t *testing.T) {
	home := t.TempDir()
	target := clientTarget(home, ClientCodex, nil)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("model = \"original\"\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	env := NewMemoryEnvironment()
	env.Values[codex.PlaceholderEnvironment] = "old"
	m := testManager(t, home, env, map[string]string{}, func() error { return errors.New("injected failure") })
	result, err := m.Point(ClientCodex, testBaseURL, Settings{})
	if err == nil || result.BackupDir == "" {
		t.Fatalf("point error=%v result=%+v", err, result)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(original) || env.Values[codex.PlaceholderEnvironment] != "old" {
		t.Fatalf("rollback failed: file=%q env=%v", got, env.Values)
	}
	manifest, readErr := readManifest(filepath.Join(result.BackupDir, "manifest.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if manifest.Completed {
		t.Fatal("failed point manifest marked completed")
	}
}

func TestRestoreRejectsCorruptBackupWithoutChangingPointedFile(t *testing.T) {
	home := t.TempDir()
	target := clientTarget(home, ClientGrok, nil)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("theme = \"old\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := testManager(t, home, NewMemoryEnvironment(), map[string]string{}, nil)
	result, err := m.Point(ClientGrok, testBaseURL, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	pointed, _ := os.ReadFile(target)
	if err := os.WriteFile(filepath.Join(result.BackupDir, "config.toml"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Restore(ClientGrok, testBaseURL, Settings{}); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("restore error = %v", err)
	}
	after, _ := os.ReadFile(target)
	if string(after) != string(pointed) {
		t.Fatal("corrupt backup restore changed target")
	}
}
