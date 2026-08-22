package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ai-gateway/internal/secret"
)

func TestMetricsEndpointReportsRequestLatencyWithoutBodies(t *testing.T) {
	up := newFakeUpstream(t, nil)
	_, addr := startWithStore(t, dataPlaneConfig(up.URL, up.URL, false), secret.NewMemStore())
	resp, body := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("request status=%d body=%s", resp.StatusCode, body)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions", strings.NewReader(`{"model":"gateway-default","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	metricsResp, metricsBody := httpJSON(t, addr, http.MethodGet, "/api/v1/metrics", nil)
	if metricsResp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", metricsResp.StatusCode, metricsBody)
	}
	var metrics MetricsResponse
	if err := json.Unmarshal(metricsBody, &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.Requests < 2 || metrics.Success < 1 || metrics.Failed < 1 {
		t.Fatalf("metrics=%+v, want success and failed data-plane requests", metrics)
	}
	if metrics.Latency.P50 <= 0 || metrics.Latency.P95 <= 0 || metrics.FirstByte.P50 <= 0 {
		t.Fatalf("metrics latency not populated: %+v", metrics)
	}
	if strings.Contains(string(metricsBody), "gateway-default") || strings.Contains(string(metricsBody), "pong") {
		t.Fatalf("metrics endpoint leaked request content: %s", metricsBody)
	}
}
