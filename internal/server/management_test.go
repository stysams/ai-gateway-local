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
	"ai-gateway/internal/secret"
	"gopkg.in/yaml.v3"
)

func TestConfigAPIAtomicRoundTripPreservesUnknownTopLevelAndSecrets(t *testing.T) {
	cfg := config.Defaults()
	cfg.Extra = map[string]yaml.Node{"future_feature": {Kind: yaml.ScalarNode, Tag: "!!str", Value: "keep-me"}}
	p := cfg.Providers["openrouter"]
	p.SecretRef = "provider.openrouter"
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
	resp, body = httpJSON(t, addr, http.MethodPut, "/api/v1/config", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config: %d %s", resp.StatusCode, body)
	}
	got := s.cfg.Snapshot()
	if got.Listen.PortValue() != 23456 || got.Logging.EnabledValue() || got.UI.Language != "en-US" {
		t.Fatalf("active config not updated: %+v", got)
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

func TestOpenAIProviderProbeModelsAndDataPlaneCache(t *testing.T) {
	const key = "sk-probe-openai"
	var mu sync.Mutex
	var paths, auth []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.RequestURI())
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"z-model","owned_by":"vendor"},{"id":"a-model","owned_by":"vendor"},{"id":"a-model"}]}`)
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
	if !probe.OK || probe.Status != http.StatusOK || probe.Models != 2 {
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
	for i := range paths {
		if paths[i] != "/v1/models" || auth[i] != "Bearer "+key {
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
	var gotPath, gotKey, gotVersion, gotAuth string
	fail := false
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey, gotVersion, gotAuth = r.URL.RequestURI(), r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"), r.Header.Get("Authorization")
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
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok":false`) || strings.Contains(string(body), key) {
		t.Fatalf("failed probe response = %d %s", resp.StatusCode, body)
	}
	resp, body = httpJSON(t, addr, http.MethodGet, "/api/v1/providers/ollama/models", nil)
	if resp.StatusCode != http.StatusBadGateway || strings.Contains(string(body), key) {
		t.Fatalf("failed models response = %d %s", resp.StatusCode, body)
	}
}
