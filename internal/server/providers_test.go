package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// httpJSON performs a JSON request against the test server and returns the
// raw response plus the decoded error envelope (when present).
func httpJSON(t *testing.T, addr, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://"+addr+path, rd)
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
	var p ProviderResponse
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("decode provider response %s: %v", data, err)
	}
	return p
}

func decodeError(t *testing.T, data []byte) errorBody {
	t.Helper()
	var eb errorBody
	if err := json.Unmarshal(data, &eb); err != nil {
		t.Fatalf("decode error response %s: %v", data, err)
	}
	return eb
}

func newDeepSeek() map[string]any {
	return map[string]any{
		"id":            "deepseek",
		"name":          "DeepSeek",
		"adapter":       "openai-chat",
		"base_url":      "https://api.deepseek.com",
		"default_model": "deepseek-chat",
		"api_key":       "sk-test-secret-1",
	}
}

// startWithStore is start() with a custom key store and config.
func startWithStore(t *testing.T, cfg *config.Config, store secret.Store) (*Server, string) {
	t.Helper()
	s := newTestServerWithStore(t, cfg, store)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := s.Addr()
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.Shutdown(ctx)
		<-serveErr
	})
	return s, addr
}

func TestCreateProviderWithSecret(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %s, want 201", resp.StatusCode, data)
	}
	p := decodeProvider(t, data)
	if p.ID != "deepseek" || p.Name != "DeepSeek" || !p.HasSecret {
		t.Errorf("response = %+v, want id/name/has_secret=true", p)
	}

	// The key must be readable from the store...
	got, err := s.secrets.Get(context.Background(), "provider.deepseek")
	if err != nil {
		t.Fatalf("secret not in store: %v", err)
	}
	defer secret.Zero(got)
	if string(got) != "sk-test-secret-1" {
		t.Errorf("stored secret = %q, want sk-test-secret-1", got)
	}

	// ...but must never appear in the config file on disk.
	data2, err := os.ReadFile(s.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data2), "sk-test-secret-1") {
		t.Errorf("config file contains the plaintext key:\n%s", data2)
	}
	cfg, err := config.Parse(data2)
	if err != nil {
		t.Fatal(err)
	}
	dp, ok := cfg.Providers["deepseek"]
	if !ok {
		t.Fatal("provider missing from written config")
	}
	if dp.SecretRef != "provider.deepseek" {
		t.Errorf("secret_ref = %q, want provider.deepseek", dp.SecretRef)
	}
	// The Provider model has no key field at all; ensure the YAML is clean.
	if strings.Contains(string(data2), "api_key") {
		t.Errorf("config file contains an api_key field:\n%s", data2)
	}

	// GET single and list must agree, and never leak the key.
	resp, data = httpJSON(t, addr, http.MethodGet, "/api/v1/providers/deepseek", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET single status = %d, body = %s", resp.StatusCode, data)
	}
	if got := decodeProvider(t, data); !got.HasSecret || strings.Contains(string(data), "sk-test-secret-1") {
		t.Errorf("GET single leaked key or wrong has_secret: %s", data)
	}
	resp, data = httpJSON(t, addr, http.MethodGet, "/api/v1/providers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list status = %d, body = %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), "sk-test-secret-1") {
		t.Errorf("GET list leaked the key: %s", data)
	}
	var list []ProviderResponse
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range list {
		if item.ID == "deepseek" {
			found = true
			if !item.HasSecret {
				t.Error("list item has_secret = false, want true")
			}
		}
	}
	if !found {
		t.Error("deepseek missing from provider list")
	}
}

func TestCreateProviderWithoutSecret(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	req := map[string]any{
		"id":            "local",
		"name":          "Local",
		"adapter":       "openai-chat",
		"base_url":      "http://127.0.0.1:9999/v1",
		"default_model": "m",
	}
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, data)
	}
	if p := decodeProvider(t, data); p.HasSecret {
		t.Error("has_secret = true for a keyless provider")
	}
}

func TestCreateProviderValidation(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	cases := []struct {
		name string
		req  map[string]any
		code string
	}{
		{"bad adapter", map[string]any{"id": "x", "name": "X", "adapter": "bogus", "base_url": "https://x.ai", "default_model": "m"}, "config_invalid"},
		{"relative url", map[string]any{"id": "x", "name": "X", "adapter": "openai-chat", "base_url": "relative", "default_model": "m"}, "config_invalid"},
		{"bad id", map[string]any{"id": "Bad-ID", "name": "X", "adapter": "openai-chat", "base_url": "https://x.ai", "default_model": "m"}, "config_invalid"},
		{"missing name", map[string]any{"id": "x", "name": "", "adapter": "openai-chat", "base_url": "https://x.ai", "default_model": "m"}, "config_invalid"},
	}
	for _, tc := range cases {
		resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", tc.req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, body = %s, want 400", tc.name, resp.StatusCode, data)
			continue
		}
		if eb := decodeError(t, data); eb.Error.Code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, eb.Error.Code, tc.code)
		}
	}
	// A bad body (not JSON) is a 400 invalid_json.
	resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", "not json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("non-JSON body status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateProviderDuplicateConflict(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("first create failed")
	}
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409, body = %s", resp.StatusCode, data)
	}
	if eb := decodeError(t, data); eb.Error.Code != "provider_exists" {
		t.Errorf("code = %q, want provider_exists", eb.Error.Code)
	}
}

func TestGetProviderNotFound(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, data := httpJSON(t, addr, http.MethodGet, "/api/v1/providers/nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", resp.StatusCode, data)
	}
}

func TestUpdateProviderReplacesSecret(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("create failed")
	}

	req := newDeepSeek()
	req["api_key"] = "sk-test-secret-2"
	resp, data := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %s, want 200", resp.StatusCode, data)
	}
	got, err := s.secrets.Get(context.Background(), "provider.deepseek")
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Zero(got)
	if string(got) != "sk-test-secret-2" {
		t.Errorf("secret after update = %q, want sk-test-secret-2", got)
	}
	// The new key must not hit the config file either.
	disk, _ := os.ReadFile(s.cfg.Path())
	if strings.Contains(string(disk), "sk-test-secret-2") {
		t.Errorf("config contains updated plaintext key:\n%s", disk)
	}
}

func TestUpdateProviderKeepsSecretWhenNoKey(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("create failed")
	}

	// Update without api_key: the stored key and ref must survive.
	req := newDeepSeek()
	delete(req, "api_key")
	resp, data := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", resp.StatusCode, data)
	}
	if p := decodeProvider(t, data); !p.HasSecret {
		t.Error("has_secret = false after keyless update, want true")
	}
	got, err := s.secrets.Get(context.Background(), "provider.deepseek")
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Zero(got)
	if string(got) != "sk-test-secret-1" {
		t.Errorf("secret = %q, want sk-test-secret-1 (unchanged)", got)
	}
}

func TestUpdateProviderNotFound(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, data := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/nope", newDeepSeek())
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", resp.StatusCode, data)
	}
}

// breakConfigDir makes the next config write fail by replacing the config
// directory with a plain file (CreateTemp inside it then fails).
func breakConfigDir(t *testing.T, s *Server) {
	t.Helper()
	dir := filepath.Dir(s.cfg.Path())
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("now a file"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateProviderRestoresOldSecretOnConfigFailure(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("create failed")
	}

	breakConfigDir(t, s)

	req := newDeepSeek()
	req["api_key"] = "sk-test-secret-3"
	resp, data := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", req)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", resp.StatusCode, data)
	}
	// The transaction must have restored the old key: the store still holds
	// sk-test-secret-1, and the failed new key is gone.
	got, err := s.secrets.Get(context.Background(), "provider.deepseek")
	if err != nil {
		t.Fatalf("old secret lost: %v", err)
	}
	defer secret.Zero(got)
	if string(got) != "sk-test-secret-1" {
		t.Errorf("secret after failed update = %q, want sk-test-secret-1 (restored)", got)
	}
	// No partial_failure: restoration succeeded, so the error is a plain
	// internal error, and no secret material appears in it.
	if eb := decodeError(t, data); eb.Error.Code == "partial_failure" {
		t.Errorf("unexpected partial_failure after successful restoration: %s", data)
	}
	if strings.Contains(string(data), "sk-test") {
		t.Errorf("error response leaked key material: %s", data)
	}
}

// putFailer fails every Put from failFrom onward. It is used to force key
// restoration to fail inside a transaction.
type putFailer struct {
	*secret.MemStore
	puts     int
	failFrom int
}

func (p *putFailer) Put(ctx context.Context, ref string, value []byte) error {
	p.puts++
	if p.puts >= p.failFrom {
		return errors.New("simulated key store write failure")
	}
	return p.MemStore.Put(ctx, ref, value)
}

func TestUpdateProviderPartialFailure(t *testing.T) {
	store := &putFailer{MemStore: secret.NewMemStore(), failFrom: 1 << 30}
	s, addr := startWithStore(t, config.Defaults(), store)
	resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("create failed (first Put succeeded)")
	}

	breakConfigDir(t, s)
	// The next Put (the new key write) succeeds, the one after (restore of
	// the old key) fails: config write failed AND restoration failed.
	store.failFrom = store.puts + 2

	req := newDeepSeek()
	req["api_key"] = "sk-test-secret-4"
	resp, data := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", req)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", resp.StatusCode, data)
	}
	eb := decodeError(t, data)
	if eb.Error.Code != "partial_failure" {
		t.Fatalf("code = %q, want partial_failure, body = %s", eb.Error.Code, data)
	}
	if eb.Error.Details["ref"] != "provider.deepseek" {
		t.Errorf("details.ref = %q, want provider.deepseek", eb.Error.Details["ref"])
	}
	if !strings.Contains(eb.Error.Message, "doctor") {
		t.Errorf("message should point at doctor: %q", eb.Error.Message)
	}
	if strings.Contains(string(data), "sk-test") {
		t.Errorf("error response leaked key material: %s", data)
	}
}

func TestDeleteProviderRemovesSecret(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("create failed")
	}

	resp, data := httpJSON(t, addr, http.MethodDelete, "/api/v1/providers/deepseek", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", resp.StatusCode, data)
	}
	if strings.Contains(string(data), "warning") {
		t.Errorf("unexpected warning on clean delete: %s", data)
	}
	if _, err := s.secrets.Get(context.Background(), "provider.deepseek"); !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("secret still present after delete: %v", err)
	}
	disk, err := os.ReadFile(s.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(disk)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Providers["deepseek"]; ok {
		t.Error("provider still in config after delete")
	}
}

func TestDeleteProviderReferencedByRoute(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, data := httpJSON(t, addr, http.MethodDelete, "/api/v1/providers/openrouter", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", resp.StatusCode, data)
	}
	if eb := decodeError(t, data); eb.Error.Code != "provider_in_use" {
		t.Errorf("code = %q, want provider_in_use", eb.Error.Code)
	}
	// The config must be untouched on disk.
	disk, err := os.ReadFile(s.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(disk)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Providers["openrouter"]; !ok {
		t.Error("provider removed despite route reference")
	}
}

// deleteFailer fails Delete calls.
type deleteFailer struct {
	*secret.MemStore
}

func (d *deleteFailer) Delete(ctx context.Context, ref string) error {
	return errors.New("simulated key store delete failure")
}

func TestDeleteProviderSecretFailureWarns(t *testing.T) {
	store := &deleteFailer{MemStore: secret.NewMemStore()}
	s, addr := startWithStore(t, config.Defaults(), store)
	resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatal("create failed")
	}

	resp, data := httpJSON(t, addr, http.MethodDelete, "/api/v1/providers/deepseek", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200, body = %s", resp.StatusCode, data)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	warning, _ := body["warning"].(string)
	if !strings.Contains(warning, "doctor") {
		t.Errorf("warning should point at doctor, got %q", warning)
	}
	// The provider must NOT be restored even though the key delete failed.
	disk, err := os.ReadFile(s.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(disk)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Providers["deepseek"]; ok {
		t.Error("provider restored despite key deletion failure")
	}
}

func TestListProvidersDeterministic(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, data := httpJSON(t, addr, http.MethodGet, "/api/v1/providers", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var list []ProviderResponse
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %v, want the two default providers", list)
	}
	for i, p := range list {
		if i > 0 && list[i-1].ID > p.ID {
			t.Errorf("list not sorted: %v", list)
		}
	}
}

func TestCreateProviderStoreUnavailable(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), brokenStore{})
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", resp.StatusCode, data)
	}
	if eb := decodeError(t, data); eb.Error.Code != "secret_store_unavailable" {
		t.Errorf("code = %q, want secret_store_unavailable", eb.Error.Code)
	}
}

func TestProviderCapabilitiesRoundtrip(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	req := map[string]any{
		"id":            "vision",
		"name":          "Vision",
		"adapter":       "openai-chat",
		"base_url":      "https://x.ai",
		"default_model": "m",
		"capabilities":  map[string]any{"image_input": true, "reasoning": true, "context_management": true},
	}
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, data)
	}
	p := decodeProvider(t, data)
	if !p.Capabilities.ImageInput || !p.Capabilities.Reasoning || !p.Capabilities.ContextManagement {
		t.Errorf("capabilities = %+v, want both true", p.Capabilities)
	}
}

func TestProviderExtraHeadersRoundTrip(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	req := map[string]any{
		"id": "custom", "name": "Custom", "adapter": "openai-responses",
		"base_url": "https://example.com/v1", "default_model": "model",
		"extra_headers": map[string]string{"User-Agent": "codex_cli_rs/0.147.0", "Originator": "codex_cli_rs"},
	}
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, data)
	}
	p := decodeProvider(t, data)
	if p.ExtraHeaders["Originator"] != "codex_cli_rs" || p.ExtraHeaders["User-Agent"] != "codex_cli_rs/0.147.0" {
		t.Fatalf("response extra_headers = %v", p.ExtraHeaders)
	}
	if got := s.cfg.Snapshot().Providers["custom"].ExtraHeaders["Originator"]; got != "codex_cli_rs" {
		t.Fatalf("persisted Originator = %q", got)
	}
}
