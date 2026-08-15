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
	"ai-gateway/internal/point/codex"
	"ai-gateway/internal/point/grok"
)

const testBaseURL = "http://127.0.0.1:12600"

func testManager(t *testing.T, home string, env UserEnvironment, lookup map[string]string, after func() error) *Manager {
	t.Helper()
	return NewWithOptions(t.TempDir(), Options{
		HomeDir:        home,
		LookupEnv:      func(name string) (string, bool) { v, ok := lookup[name]; return v, ok },
		CommandExists:  func(string) bool { return false },
		Environment:    env,
		Now:            func() time.Time { return time.Date(2026, 8, 15, 6, 0, 0, 123, time.UTC) },
		AfterFileWrite: after,
	})
}

func clientTarget(home string, client Client, lookup map[string]string) string {
	switch client {
	case ClientCodex:
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
		{ClientCodex, "codex-existing.toml", nil, func(b []byte) (bool, error) { return codex.Check(b, testBaseURL) }, "approval_policy"},
		{ClientClaude, "claude-existing.json", func(home string) map[string]string {
			return map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(home, "claude-custom")}
		}, func(b []byte) (bool, error) { return claude.Check(b, testBaseURL) }, "KEEP_ME"},
		{ClientGrok, "grok-existing.toml", func(home string) map[string]string {
			return map[string]string{"GROK_HOME": filepath.Join(home, "grok-custom")}
		}, func(b []byte) (bool, error) { return grok.Check(b, testBaseURL) }, "plugin"},
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
			result, err := m.Point(tc.client, testBaseURL)
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
			second, err := m.Point(tc.client, testBaseURL)
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
			if state := m.Check(tc.client, testBaseURL).PointState; state != StateDrifted {
				t.Fatalf("manual edit state = %s", state)
			}
			restored, err := m.Restore(tc.client, testBaseURL)
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
			if _, err := m.Restore(tc.client, testBaseURL); !errors.Is(err, ErrNoRestore) {
				t.Fatalf("second restore error = %v", err)
			}
		})
	}
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
			result, err := m.Point(client, testBaseURL)
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
			if _, err := m.Restore(client, testBaseURL); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("restore did not remove created file: %v", err)
			}
		})
	}
}

func TestPointMigratesLegacyDisplayModelWithoutReplacingOriginalBackup(t *testing.T) {
	const displayModel = "openrouter/anthropic/claude-sonnet-4"
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
			env := NewMemoryEnvironment()
			m := testManager(t, home, env, map[string]string{}, nil)
			legacy, err := m.Point(client, testBaseURL)
			if err != nil {
				t.Fatal(err)
			}
			manifestsBefore, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", string(client), "*", "manifest.json"))
			migrated, err := m.Point(client, testBaseURL, displayModel)
			if err != nil {
				t.Fatal(err)
			}
			manifestsAfter, _ := filepath.Glob(filepath.Join(m.dataRoot, "backups", string(client), "*", "manifest.json"))
			if !migrated.Changed || migrated.BackupDir != "" || len(manifestsAfter) != len(manifestsBefore) {
				t.Fatalf("legacy migration replaced the restore point: legacy=%+v migrated=%+v before=%d after=%d", legacy, migrated, len(manifestsBefore), len(manifestsAfter))
			}
			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), displayModel) {
				t.Fatalf("display model was not migrated:\n%s", data)
			}
			if _, err := m.Restore(client, testBaseURL, displayModel); err != nil {
				t.Fatal(err)
			}
			restored, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(restored) != string(original) {
				t.Fatalf("restore after migration = %q, want %q", restored, original)
			}
		})
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
	result, err := m.Point(ClientCodex, testBaseURL)
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
	result, err := m.Point(ClientGrok, testBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pointed, _ := os.ReadFile(target)
	if err := os.WriteFile(filepath.Join(result.BackupDir, "config.toml"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Restore(ClientGrok, testBaseURL); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("restore error = %v", err)
	}
	after, _ := os.ReadFile(target)
	if string(after) != string(pointed) {
		t.Fatal("corrupt backup restore changed target")
	}
}
