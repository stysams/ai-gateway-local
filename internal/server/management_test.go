package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/route"
	"ai-gateway/internal/secret"
	"gopkg.in/yaml.v3"
)

func TestConfigAPIAtomicRoundTripPreservesUnknownTopLevelAndSecrets(t *testing.T) {
	cfg := config.Defaults()
	cfg.Limits.Global = 1
	cfg.Extra = map[string]yaml.Node{"future_feature": {Kind: yaml.ScalarNode, Tag: "!!str", Value: "keep-me"}}
	p := cfg.Providers["openrouter"]
	p.SecretRef = "provider.openrouter"
	p.DisguiseClient = config.DisguiseClientClaude
	p.ExtraHeaders = map[string]string{"User-Agent": "claude-cli/2.1.228 (external, cli)"}
	cfg.Providers["openrouter"] = p
	store := secret.NewMemStore()
	const actualSecret = "sk-config-api-secret"
	if err := store.Put(context.Background(), p.SecretRef, []byte(actualSecret)); err != nil {
		t.Fatal(err)
	}
	s, addr := startWithStore(t, cfg, store)

	resp, body := httpJSON(t, addr, http.MethodGet, "/api/v1/config", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), actualSecret) {
		t.Fatalf("GET config leaked secret: %s", body)
	}
	var payload ConfigPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	payload.Listen.Port = 23456
	payload.Logging.Enabled = false
	payload.UI.Language = "en-US"
	provider := payload.Providers["openrouter"]
	provider.Models = []ProviderModelPayload{{ID: provider.DefaultModel, Name: "Claude Sonnet", ContextWindow: 200000, MaxOutputTokens: 64000}}
	payload.Providers["openrouter"] = provider
	resp, body = httpJSON(t, addr, http.MethodPut, "/api/v1/config", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config: %d %s", resp.StatusCode, body)
	}
	got := s.cfg.Snapshot()
	if got.Listen.PortValue() != 23456 || got.Logging.EnabledValue() || got.UI.Language != "en-US" {
		t.Fatalf("active config not updated: %+v", got)
	}
	if models := got.Providers["openrouter"].Models; len(models) != 1 || models[0].ContextWindow != 200000 || models[0].MaxOutputTokens != 64000 {
		t.Fatalf("provider model metadata not preserved: %+v", models)
	}
	if got.Providers["openrouter"].DisguiseClient != config.DisguiseClientClaude {
		t.Fatalf("disguise_client lost after settings save: %q", got.Providers["openrouter"].DisguiseClient)
	}
	if got.Providers["openrouter"].ExtraHeaders["User-Agent"] != "claude-cli/2.1.228 (external, cli)" {
		t.Fatalf("extra_headers lost after settings save: %v", got.Providers["openrouter"].ExtraHeaders)
	}
	payload.Limits.Global = 0
	resp, body = httpJSON(t, addr, http.MethodPut, "/api/v1/config", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config explicit zero: %d %s", resp.StatusCode, body)
	}
	if got := s.cfg.Snapshot().Limits.Global; got != 0 {
		t.Fatalf("explicit zero did not disable global limit: %d", got)
	}
	disk, err := os.ReadFile(s.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(disk), "future_feature: keep-me") {
		t.Fatalf("unknown top-level field lost:\n%s", disk)
	}
	if strings.Contains(string(disk), actualSecret) {
		t.Fatal("config file leaked secret material")
	}

	payload.Autostart.Enabled = true
	resp, _ = httpJSON(t, addr, http.MethodPut, "/api/v1/config", payload)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("autostart side-effect bypass status = %d, want 409", resp.StatusCode)
	}
}

func TestRouteAPIUpdatesAllRoutesAndNextRequest(t *testing.T) {
	up1 := newFakeUpstream(t, nil)
	up2 := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up1.URL, up2.URL, false)
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	for _, client := range []string{"codex", "claude", "grok", "generic"} {
		resp, body := httpJSON(t, addr, http.MethodPut, "/api/v1/routes/"+client, RouteRequest{Provider: "openrouter", Model: "route-model-" + client})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("route %s: %d %s", client, resp.StatusCode, body)
		}
	}
	resp, data := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("routed request: %d %s", resp.StatusCode, data)
	}
	if len(up1.requests()) != 1 || len(up2.requests()) != 0 || up1.last().Model != "route-model-generic" {
		t.Fatalf("next request did not use updated generic route: up1=%d up2=%d model=%q", len(up1.requests()), len(up2.requests()), up1.last().Model)
	}
	bad, _ := httpJSON(t, addr, http.MethodPut, "/api/v1/routes/codex", RouteRequest{Provider: "missing", Model: "m"})
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown provider status = %d", bad.StatusCode)
	}
}

func TestLocalAccessReportsLoopbackEndpointsAndEnabledModels(t *testing.T) {
	cfg := config.Defaults()
	provider := cfg.Providers["openrouter"]
	provider.Models = []config.ProviderModel{
		{ID: provider.DefaultModel, Name: "Default"},
		{ID: "enabled-model", Name: "Enabled"},
		{ID: "disabled-model", Name: "Disabled", Enabled: config.BoolPtr(false)},
	}
	cfg.Providers["openrouter"] = provider
	disabledProvider := cfg.Providers["ollama"]
	disabledProvider.Enabled = config.BoolPtr(false)
	cfg.Providers["ollama"] = disabledProvider

	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	resp, body := httpJSON(t, addr, http.MethodGet, "/api/v1/local-access", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("local access: %d %s", resp.StatusCode, body)
	}
	var got LocalAccessResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	wantBaseURL := "http://" + addr + "/v1"
	if got.BaseURL != wantBaseURL || got.Endpoints.Models != wantBaseURL+"/models" || got.Endpoints.ChatCompletions != wantBaseURL+"/chat/completions" {
		t.Fatalf("local endpoints = %+v, base URL = %q", got.Endpoints, got.BaseURL)
	}
	if got.APIKey != localAccessAPIKeyPlaceholder || got.AuthRequired || got.DefaultModel != route.ReservedModel {
		t.Fatalf("local access defaults = %+v", got)
	}
	if got.DefaultRoute.Provider != cfg.Routes.Generic.Provider || got.DefaultRoute.Model != cfg.Routes.Generic.Model {
		t.Fatalf("default route = %+v", got.DefaultRoute)
	}
	modelIDs := make([]string, 0, len(got.Models))
	for _, model := range got.Models {
		modelIDs = append(modelIDs, model.ID)
	}
	joined := strings.Join(modelIDs, ",")
	for _, want := range []string{route.ReservedModel, "openrouter/" + provider.DefaultModel, "openrouter/enabled-model"} {
		if !strings.Contains(joined, want) {
			t.Errorf("enabled model %q missing from %q", want, joined)
		}
	}
	for _, excluded := range []string{"disabled-model", "ollama/"} {
		if strings.Contains(joined, excluded) {
			t.Errorf("disabled model/provider leaked into %q", joined)
		}
	}
}

func TestOpenAIProviderProbeModelsAndDataPlaneCache(t *testing.T) {
	const key = "sk-probe-openai"
	var mu sync.Mutex
	var paths, auth []string
	var probePromptBody string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			requestBody, _ := io.ReadAll(r.Body)
			probePromptBody = string(requestBody)
			fmt.Fprint(w, `{"id":"probe","choices":[{"message":{"role":"assistant","content":"identity response"}}]}`)
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"z-model","owned_by":"vendor"},{"id":"a-model","owned_by":"vendor"},{"id":"a-model"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	cfg := dataPlaneConfig(up.URL+"/v1", up.URL+"/v1", true)
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.openrouter", []byte(key)); err != nil {
		t.Fatal(err)
	}
	s, addr := startWithStore(t, cfg, store)
	before, _ := os.ReadFile(s.cfg.Path())

	resp, body := httpJSON(t, addr, http.MethodPost, "/api/v1/providers/openrouter/probe", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("probe: %d %s", resp.StatusCode, body)
	}
	var probe ProbeResponse
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatal(err)
	}
	if !probe.OK || probe.Status != http.StatusOK || probe.Models != 1 || !strings.Contains(probe.Response, "identity response") || !strings.Contains(probePromptBody, probePrompt) {
		t.Fatalf("probe = %+v", probe)
	}
	resp, body = httpJSON(t, addr, http.MethodGet, "/api/v1/providers/openrouter/models", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("models: %d %s", resp.StatusCode, body)
	}
	var models ProviderModelsResponse
	if err := json.Unmarshal(body, &models); err != nil {
		t.Fatal(err)
	}
	if len(models.Data) != 2 || models.Data[0].ID != "openrouter/a-model" || models.Data[1].ID != "openrouter/z-model" {
		t.Fatalf("models = %+v", models.Data)
	}
	mu.Lock()
	for i, expectedPath := range []string{"/v1/chat/completions", "/v1/models"} {
		if paths[i] != expectedPath || auth[i] != "Bearer "+key {
			t.Errorf("request %d path=%q auth=%q", i, paths[i], auth[i])
		}
	}
	mu.Unlock()
	after, _ := os.ReadFile(s.cfg.Path())
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("probe or model listing modified config")
	}

	resp, body = chatGet(t, addr, "/v1/models")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "openrouter/a-model") || !strings.Contains(string(body), "openrouter/z-model") {
		t.Fatalf("data-plane model cache missing: %d %s", resp.StatusCode, body)
	}

	provider := s.cfg.Snapshot().Providers["openrouter"]
	update := ProviderRequest{Name: provider.Name, Adapter: provider.Adapter, BaseURL: provider.BaseURL, DefaultModel: provider.DefaultModel, Capabilities: &CapabilitiesPayload{}}
	resp, body = httpJSON(t, addr, http.MethodPut, "/api/v1/providers/openrouter", update)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("provider update: %d %s", resp.StatusCode, body)
	}
	_, body = chatGet(t, addr, "/v1/models")
	if strings.Contains(string(body), "openrouter/a-model") {
		t.Fatalf("provider update did not invalidate model cache: %s", body)
	}
}

func TestAnthropicModelsHeadersAndProbeFailureDoesNotLeakBody(t *testing.T) {
	const key = "sk-anthropic-probe"
	var gotPath, gotKey, gotVersion, gotAuth, probeBody string
	fail := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey, gotVersion, gotAuth = r.URL.RequestURI(), r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"), r.Header.Get("Authorization")
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			probeBody = string(body)
		}
		if fail {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"`+key+`"}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"claude-test","display_name":"Claude Test"}],"has_more":false}`)
	}))
	t.Cleanup(up.Close)
	cfg := config.Defaults()
	p := cfg.Providers["ollama"]
	p.Adapter, p.BaseURL, p.SecretRef = "anthropic", up.URL, "provider.ollama"
	cfg.Providers["ollama"] = p
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), p.SecretRef, []byte(key)); err != nil {
		t.Fatal(err)
	}
	_, addr := startWithStore(t, cfg, store)
	resp, body := httpJSON(t, addr, http.MethodGet, "/api/v1/providers/ollama/models", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anthropic models: %d %s", resp.StatusCode, body)
	}
	if gotPath != "/v1/models?limit=1000" || gotKey != key || gotVersion != "2023-06-01" || gotAuth != "" {
		t.Fatalf("anthropic request path=%q key=%q version=%q auth=%q", gotPath, gotKey, gotVersion, gotAuth)
	}

	fail = true
	resp, body = httpJSON(t, addr, http.MethodPost, "/api/v1/providers/ollama/probe", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok":false`) || strings.Contains(string(body), key) || gotPath != "/v1/messages" || !strings.Contains(probeBody, probePrompt) {
		t.Fatalf("failed probe response = %d %s", resp.StatusCode, body)
	}
	resp, body = httpJSON(t, addr, http.MethodGet, "/api/v1/providers/ollama/models", nil)
	if resp.StatusCode != http.StatusBadGateway || strings.Contains(string(body), key) {
		t.Fatalf("failed models response = %d %s", resp.StatusCode, body)
	}
}

func TestDiscoverProviderModelsUsesDraftMetadataAndDoesNotPolluteCache(t *testing.T) {
	const draftKey = "sk-draft-models"
	var gotAuthorization string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"model-b","name":"Model B","context_length":131072,"top_provider":{"context_length":200000,"max_completion_tokens":32000}},{"id":"model-a"}]}`)
	}))
	t.Cleanup(up.Close)
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, body := httpJSON(t, addr, http.MethodPost, "/api/v1/provider-models/discover", DiscoverProviderModelsRequest{ProviderID: "draft", Adapter: "openai-chat", BaseURL: up.URL + "/v1", APIKey: stringPtr(draftKey)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover models: %d %s", resp.StatusCode, body)
	}
	var result ProviderModelsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if gotAuthorization != "Bearer "+draftKey {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
	if len(result.Data) != 2 || result.Data[0].RawID != "model-b" || result.Data[0].DisplayName != "Model B" || result.Data[0].ContextWindow != 200000 || result.Data[0].MaxOutputTokens != 32000 {
		t.Fatalf("discovered models = %+v", result.Data)
	}
	if result.Data[1].ContextWindow != 0 || result.Data[1].MaxOutputTokens != 0 {
		t.Fatalf("missing metadata was not kept unknown: %+v", result.Data[1])
	}
	if _, ok := s.cachedModels()["draft"]; ok {
		t.Fatal("draft discovery polluted the data-plane model cache")
	}
	if _, ok := s.cfg.Snapshot().Providers["draft"]; ok {
		t.Fatal("draft discovery persisted a provider")
	}
}

func TestDiscoverProviderModelsReusesSavedSecretWhenDraftKeyIsEmpty(t *testing.T) {
	const savedKey = "sk-saved-models"
	var gotAuthorization string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"data":[{"id":"model"}]}`)
	}))
	t.Cleanup(up.Close)
	cfg := config.Defaults()
	p := cfg.Providers["ollama"]
	p.BaseURL, p.SecretRef = up.URL+"/v1", "provider.ollama"
	cfg.Providers["ollama"] = p
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), p.SecretRef, []byte(savedKey)); err != nil {
		t.Fatal(err)
	}
	_, addr := startWithStore(t, cfg, store)
	empty := ""
	resp, body := httpJSON(t, addr, http.MethodPost, "/api/v1/provider-models/discover", DiscoverProviderModelsRequest{ProviderID: "ollama", Adapter: p.Adapter, BaseURL: p.BaseURL, APIKey: &empty})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discover models: %d %s", resp.StatusCode, body)
	}
	if gotAuthorization != "Bearer "+savedKey {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
}

func stringPtr(value string) *string { return &value }
