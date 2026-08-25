package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

func httpJSON(t *testing.T, addr, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://"+addr+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

func decodeProvider(t *testing.T, data []byte) ProviderResponse {
	t.Helper()
	var provider ProviderResponse
	if err := json.Unmarshal(data, &provider); err != nil {
		t.Fatalf("decode provider response %s: %v", data, err)
	}
	return provider
}

func decodeError(t *testing.T, data []byte) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode error response %s: %v", data, err)
	}
	return body
}

func newDeepSeek() map[string]any {
	return map[string]any{
		"id": "deepseek", "name": "DeepSeek", "base_url": "https://api.deepseek.com",
		"key_groups": map[string]any{"main": map[string]any{
			"name": "Main", "enabled": true, "api_key": "sk-test-secret-1", "endpoint": "/v1/chat/completions", "adapter": "openai-chat",
			"default_model": "deepseek-chat", "models": []map[string]any{{"id": "deepseek-chat", "enabled": true}},
		}},
	}
}

func startWithStore(t *testing.T, cfg *config.Config, store secret.Store) (*Server, string) {
	t.Helper()
	s := newTestServerWithStore(t, cfg, store)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		<-serveErr
	})
	return s, addr
}

func TestProviderKeyGroupsPersistPlaintextOnlyInExplicitScope(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
	provider := decodeProvider(t, data)
	if len(provider.KeyGroups) != 1 || provider.KeyGroups[0].KeyID != "main" || !provider.KeyGroups[0].HasAPIKey {
		t.Fatalf("provider=%+v", provider)
	}
	if strings.Contains(string(data), "sk-test-secret-1") {
		t.Fatalf("provider summary leaked api key: %s", data)
	}

	disk, err := os.ReadFile(s.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(disk), "api_key: sk-test-secret-1") {
		t.Fatalf("config did not persist plaintext key:\n%s", disk)
	}

	resp, data = httpJSON(t, addr, http.MethodGet, "/api/v1/providers/deepseek/keys/main", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(data), `"api_key":"sk-test-secret-1"`) {
		t.Fatalf("key detail=%d %s", resp.StatusCode, data)
	}
	resp, data = httpJSON(t, addr, http.MethodGet, "/api/v1/providers", nil)
	if strings.Contains(string(data), "sk-test-secret-1") {
		t.Fatalf("provider list leaked api key: %s", data)
	}
}

func TestProviderRejectsLegacyShapeAndDuplicateCreate(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	legacy := map[string]any{"id": "legacy", "name": "Legacy", "adapter": "openai-chat", "base_url": "https://x.ai", "default_model": "m"}
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", legacy)
	if resp.StatusCode != http.StatusBadRequest || decodeError(t, data).Error.Code != "invalid_json" {
		t.Fatalf("legacy=%d %s", resp.StatusCode, data)
	}
	resp, data = httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create=%d %s", resp.StatusCode, data)
	}
	resp, data = httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusConflict || decodeError(t, data).Error.Code != "provider_exists" {
		t.Fatalf("duplicate=%d %s", resp.StatusCode, data)
	}
}

func TestKeyGroupDuplicateWarningAndDeleteConflicts(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create=%d %s", resp.StatusCode, data)
	}
	payload := map[string]any{"key_id": "backup", "name": "Backup", "enabled": true, "api_key": "sk-test-secret-1", "endpoint": "/v1/messages", "adapter": "anthropic", "default_model": "claude", "models": []map[string]any{{"id": "claude", "enabled": true}}}
	resp, data = httpJSON(t, addr, http.MethodPost, "/api/v1/providers/deepseek/keys", payload)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(data), `"duplicate_key_groups":["main"]`) {
		t.Fatalf("duplicate warning=%d %s", resp.StatusCode, data)
	}

	resp, data = httpJSON(t, addr, http.MethodDelete, "/api/v1/providers/openrouter/keys/default", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("referenced key delete=%d %s", resp.StatusCode, data)
	}

	update := map[string]any{"name": "默认密钥", "enabled": true, "endpoint": "/chat/completions", "default_model": "replacement", "models": []map[string]any{{"id": "replacement", "enabled": true}}}
	resp, data = httpJSON(t, addr, http.MethodPut, "/api/v1/providers/openrouter/keys/default", update)
	if resp.StatusCode != http.StatusConflict || decodeError(t, data).Error.Code != "model_in_use" {
		t.Fatalf("referenced model update=%d %s", resp.StatusCode, data)
	}
}

func TestProviderSharedFieldsAndCapabilitiesRoundTrip(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	req := newDeepSeek()
	req["extra_headers"] = map[string]string{"User-Agent": "gateway-test"}
	req["disguise_client"] = "pi"
	req["capabilities"] = map[string]bool{"image_input": true, "reasoning": true, "context_management": true}
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", resp.StatusCode, data)
	}
	p := decodeProvider(t, data)
	if p.ExtraHeaders["User-Agent"] != "gateway-test" || p.DisguiseClient != config.DisguiseClientPi || !p.Capabilities.ContextManagement {
		t.Fatalf("provider=%+v", p)
	}
	if got := s.cfg.Snapshot().Providers["deepseek"].BaseURL; got != "https://api.deepseek.com" {
		t.Fatalf("base_url=%q", got)
	}
}

func TestKeyGroupWriteFailureKeepsPublishedSnapshot(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	before := s.cfg.Snapshot().Providers["openrouter"].KeyGroups["default"].APIKey
	dir := filepath.Dir(s.cfg.Path())
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := "new-key"
	payload := map[string]any{"name": "默认密钥", "enabled": true, "api_key": key, "endpoint": "/chat/completions", "default_model": "anthropic/claude-sonnet-4", "models": []map[string]any{{"id": "anthropic/claude-sonnet-4", "endpoint": "/chat/completions"}}}
	resp, _ := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/openrouter/keys/default", payload)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got := s.cfg.Snapshot().Providers["openrouter"].KeyGroups["default"].APIKey; got != before {
		t.Fatalf("published key changed to %q", got)
	}
}
