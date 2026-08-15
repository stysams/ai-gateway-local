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
)

func TestClientPointAndRestoreAPI(t *testing.T) {
	tests := []struct {
		client   point.Client
		dir      string
		file     string
		original string
	}{
		{client: point.ClientCodex, dir: ".codex", file: "config.toml", original: "custom = \"kept\"\n"},
		{client: point.ClientClaude, dir: ".claude", file: "settings.json", original: "{\"custom\":\"kept\"}\n"},
		{client: point.ClientGrok, dir: ".grok", file: "config.toml", original: "custom = \"kept\"\n"},
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
				HomeDir:       home,
				LookupEnv:     func(string) (string, bool) { return "", false },
				CommandExists: func(string) bool { return false },
				Environment:   point.NewMemoryEnvironment(),
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

			resp, body = clientJSON(t, ts, http.MethodPost, path+"/point")
			assertPointState(t, resp, body, http.StatusOK, point.StatePointed)
			var idempotent point.Result
			if err := json.Unmarshal(body, &idempotent); err != nil {
				t.Fatal(err)
			}
			if idempotent.Changed {
				t.Fatal("second point unexpectedly changed client config")
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
