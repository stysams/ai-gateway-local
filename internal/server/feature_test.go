package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

func TestProbeUsesCompletionAndModelDiscoveryUsesCustomEndpoint(t *testing.T) {
	var gotProbePrompt string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/chat/completions":
			var request struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &request)
			if len(request.Messages) > 0 {
				gotProbePrompt = request.Messages[0].Content
			}
			fmt.Fprint(w, `{"id":"probe","choices":[{"message":{"role":"assistant","content":"我是测试模型"}}]}`)
		case "/catalog":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"custom-model"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	cfg := config.Defaults()
	p := cfg.Providers["ollama"]
	p.BaseURL = up.URL + "/v1"
	p.ModelsURL = up.URL + "/catalog"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	resp, body := httpJSON(t, addr, http.MethodPost, "/api/v1/providers/ollama/probe", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `我是测试模型`) || gotProbePrompt != probePrompt {
		t.Fatalf("probe status=%d prompt=%q body=%s", resp.StatusCode, gotProbePrompt, body)
	}
	resp, body = httpJSON(t, addr, http.MethodGet, "/api/v1/providers/ollama/models", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `custom-model`) {
		t.Fatalf("models status=%d body=%s", resp.StatusCode, body)
	}
}

func TestAvailabilityFiltersModelsAndProvider(t *testing.T) {
	cfg := config.Defaults()
	p := cfg.Providers["ollama"]
	disabled := false
	p.Models = []config.ProviderModel{{ID: "enabled-model"}, {ID: "disabled-model", Enabled: &disabled}}
	p.DefaultModel = "enabled-model"
	cfg.Providers["ollama"] = p
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	resp, body := chatGet(t, addr, "/v1/models")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), "disabled-model") || !strings.Contains(string(body), "enabled-model") {
		t.Fatalf("models status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = httpJSON(t, addr, http.MethodPut, "/api/v1/providers/ollama/availability", ProviderAvailabilityRequest{Enabled: &disabled})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable provider status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = chatGet(t, addr, "/v1/models")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), "ollama/") {
		t.Fatalf("disabled provider still listed: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestConfiguredAllInterfacesListener(t *testing.T) {
	cfg := config.Defaults()
	cfg.Listen.Host = "0.0.0.0"
	s := newTestServerWithStore(t, cfg, secret.NewMemStore())
	if err := s.Listen("0.0.0.0:0"); err != nil {
		t.Fatalf("listen on all interfaces: %v", err)
	}
	t.Cleanup(func() { _ = s.ln.Close() })
	host, _, err := net.SplitHostPort(s.Addr())
	if err != nil || (host != "0.0.0.0" && host != "::") {
		t.Fatalf("listener address = %q, host=%q err=%v", s.Addr(), host, err)
	}
	if got := s.ClientBaseURL(cfg); !strings.HasPrefix(got, "http://127.0.0.1:") {
		t.Fatalf("local client base URL = %q", got)
	}
}
