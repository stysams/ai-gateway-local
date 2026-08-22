package server

import (
	"net/http"
	"strings"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

func TestProviderCircuitOpensAfterRepeatedUpstreamFailures(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream failed"}}`))
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	cfg.Limits.StreamIdleSeconds = config.DefaultStreamIdleSeconds
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	for i := 0; i < circuitFailureThreshold; i++ {
		resp, body := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
		if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "upstream") {
			t.Fatalf("failure %d status=%d body=%s", i+1, resp.StatusCode, body)
		}
	}
	resp, body := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "provider_circuit_open") {
		t.Fatalf("open circuit status=%d body=%s", resp.StatusCode, body)
	}
	if got := len(up.requests()); got != circuitFailureThreshold {
		t.Fatalf("open circuit reached upstream: %d requests", got)
	}
}
