package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/logstore"
	"ai-gateway/internal/secret"
)

func TestRequestLogsQueriesUsageAndSecretSafety(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-log","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"logged answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":{"reasoning_tokens":3}}}`)
	})
	store := secret.NewMemStore()
	const upstreamSecret = "sk-real-upstream-secret"
	if err := store.Put(context.Background(), "provider.openrouter", []byte(upstreamSecret)); err != nil {
		t.Fatal(err)
	}
	cfg := dataPlaneConfig(up.URL, up.URL, true)
	s, addr := startWithStore(t, cfg, store)

	resp, body := chatPost(t, addr, "/c/codex/v1/chat/completions?debug=true&api_key=query-secret",
		[]byte(`{"model":"gateway-default","messages":[{"role":"user","content":"private prompt text"}]}`),
		map[string]string{"Authorization": "Bearer inbound-secret", "Cookie": "session=secret", "X-Api-Key": "inbound-api-key", "X-Debug-Trace": "trace-value"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	requestID := resp.Header.Get("X-Request-Id")
	if requestID == "" {
		t.Fatal("response omitted X-Request-Id")
	}

	listResp, listBody := httpJSON(t, addr, http.MethodGet, "/api/v1/logs?client=codex&provider=openrouter", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("logs: %d %s", listResp.StatusCode, listBody)
	}
	var page logstore.Page
	if err := json.Unmarshal(listBody, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].RequestID != requestID || page.Items[0].Status != "success" {
		t.Fatalf("summaries = %+v", page.Items)
	}

	detailResp, detail := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/"+requestID, nil)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("detail: %d %s", detailResp.StatusCode, detail)
	}
	text := string(detail)
	for _, required := range []string{`"type":"request"`, `"type":"route"`, `"type":"upstream_request"`, `"type":"upstream_event"`, `"type":"client_event"`, `"type":"result"`, `"path":"/c/codex/v1/chat/completions"`, `"request_uri":"/c/codex/v1/chat/completions?debug=true"`, `"inbound_protocol":"chat"`, `"outbound_protocol":"chat"`, `"converted":false`, `"X-Debug-Trace":["trace-value"]`, `"Accept":["application/json"]`, `"User-Agent":["ai-gateway"]`, `"omitted_sensitive_header_count":3`, `"omitted_sensitive_header_count":1`, `"omitted_sensitive_query_count":1`, "private prompt text"} {
		if !strings.Contains(text, required) {
			t.Errorf("detail missing %q: %s", required, detail)
		}
	}
	for _, forbidden := range []string{upstreamSecret, "inbound-secret", "inbound-api-key", "session=secret", "query-secret", "Authorization", "x-api-key", "Cookie"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("detail leaked %q: %s", forbidden, detail)
		}
	}

	usageResp, usageBody := httpJSON(t, addr, http.MethodGet, "/api/v1/usage?client=codex", nil)
	if usageResp.StatusCode != http.StatusOK {
		t.Fatalf("usage: %d %s", usageResp.StatusCode, usageBody)
	}
	var usage logstore.UsageReport
	if err := json.Unmarshal(usageBody, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Total.Requests != 1 || usage.Total.Usage == nil || usage.Total.Usage.InputTokens != 11 || usage.Total.Usage.OutputTokens != 7 || usage.Total.Usage.ReasoningTokens != 3 || usage.Total.Usage.CacheReadInputTokens != 5 || usage.Total.Usage.CacheInputTokens != 11 || usage.Total.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", usage.Total)
	}
	if usage.Total.UsageRequests != 1 {
		t.Fatalf("usage request coverage = %+v", usage.Total)
	}
	if usage.Total.Incomplete {
		t.Fatal("usage unexpectedly marked incomplete")
	}
	filteredResp, filteredBody := httpJSON(t, addr, http.MethodGet, "/api/v1/usage?model=missing-model", nil)
	if filteredResp.StatusCode != http.StatusOK {
		t.Fatalf("model-filtered usage: %d %s", filteredResp.StatusCode, filteredBody)
	}
	var filtered logstore.UsageReport
	if err := json.Unmarshal(filteredBody, &filtered); err != nil {
		t.Fatal(err)
	}
	if filtered.Total.Requests != 0 || len(filtered.ByModel) != 0 {
		t.Fatalf("model filter was ignored: %+v", filtered.Total)
	}

	logs, err := filepath.Glob(filepath.Join(filepath.Dir(s.cfg.Path()), cfg.Logging.Dir, "*", "*.jsonl"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("log files = %v, err = %v", logs, err)
	}
	onDisk, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(onDisk), upstreamSecret) {
		t.Fatal("on-disk log leaked the upstream secret")
	}
}

func TestLoggingSwitchStopsNewFilesAndMissingUsageIsNotEstimated(t *testing.T) {
	up := newFakeUpstream(t, nil) // default response intentionally has no usage
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	s, addr := startWithStore(t, cfg, secret.NewMemStore())
	resp, _ := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d", resp.StatusCode)
	}

	usageResp, usageBody := httpJSON(t, addr, http.MethodGet, "/api/v1/usage", nil)
	if usageResp.StatusCode != http.StatusOK {
		t.Fatalf("usage = %d, %s", usageResp.StatusCode, usageBody)
	}
	var usage logstore.UsageReport
	if err := json.Unmarshal(usageBody, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Total.Usage != nil || !usage.Total.Incomplete {
		t.Fatalf("missing usage was estimated: %+v", usage.Total)
	}

	root := filepath.Join(filepath.Dir(s.cfg.Path()), cfg.Logging.Dir)
	before, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	putResp, putBody := httpJSON(t, addr, http.MethodPut, "/api/v1/logging", map[string]bool{"enabled": false})
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("disable logging: %d %s", putResp.StatusCode, putBody)
	}
	resp, _ = chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second request status = %d", resp.StatusCode)
	}
	after, _ := filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
	if len(after) != len(before) {
		t.Fatalf("disabled logging created a file: before=%d after=%d", len(before), len(after))
	}
	if s.cfg.Snapshot().Logging.EnabledValue() {
		t.Fatal("logging switch was not persisted in the active config")
	}
}

func TestLogPrivacyManagementEndpoints(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	s, addr := startWithStore(t, cfg, secret.NewMemStore())

	createLog := func(secretValue string) string {
		t.Helper()
		resp, body := chatPost(t, addr, "/v1/chat/completions", []byte(fmt.Sprintf(`{"model":"gateway-default","messages":[{"role":"user","content":"hello"}],"metadata":{"api_key":%q}}`, secretValue)), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create log: status = %d, body = %s", resp.StatusCode, body)
		}
		return resp.Header.Get("X-Request-Id")
	}

	requestID := createLog("sk-export-secret")
	exportResp, exportBody := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/"+requestID+"/export", nil)
	if exportResp.StatusCode != http.StatusOK {
		t.Fatalf("export: %d %s", exportResp.StatusCode, exportBody)
	}
	if got := exportResp.Header.Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("export content type = %q", got)
	}
	if got := exportResp.Header.Get("Content-Disposition"); !strings.Contains(got, requestID+".redacted.jsonl") {
		t.Fatalf("export disposition = %q", got)
	}
	if strings.Contains(string(exportBody), "sk-export-secret") || !strings.Contains(string(exportBody), logstore.RedactionMarker) {
		t.Fatalf("export was not redacted: %s", exportBody)
	}

	deleteResp, deleteBody := httpJSON(t, addr, http.MethodDelete, "/api/v1/logs/"+requestID, nil)
	if deleteResp.StatusCode != http.StatusOK || !strings.Contains(string(deleteBody), `"deleted":true`) {
		t.Fatalf("delete: %d %s", deleteResp.StatusCode, deleteBody)
	}
	missingResp, _ := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/"+requestID, nil)
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted detail status = %d", missingResp.StatusCode)
	}

	clearID := createLog("sk-clear-secret")
	active, err := s.warnings.OpenWithRedaction(cfg.Logging.Dir, "req_active_privacy", time.Now(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	if err := active.Append("request", map[string]any{"client": "generic", "api_key": "sk-active-secret"}); err != nil {
		t.Fatal(err)
	}
	activeDeleteResp, activeDeleteBody := httpJSON(t, addr, http.MethodDelete, "/api/v1/logs/req_active_privacy", nil)
	if activeDeleteResp.StatusCode != http.StatusConflict || !strings.Contains(string(activeDeleteBody), `"code":"log_active"`) {
		t.Fatalf("active delete: %d %s", activeDeleteResp.StatusCode, activeDeleteBody)
	}

	clearResp, clearBody := httpJSON(t, addr, http.MethodDelete, "/api/v1/logs", nil)
	if clearResp.StatusCode != http.StatusOK || !strings.Contains(string(clearBody), `"removed":1`) {
		t.Fatalf("clear: %d %s", clearResp.StatusCode, clearBody)
	}
	clearedResp, _ := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/"+clearID, nil)
	if clearedResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cleared detail status = %d", clearedResp.StatusCode)
	}
	activeResp, activeBody := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/req_active_privacy", nil)
	if activeResp.StatusCode != http.StatusOK || strings.Contains(string(activeBody), "sk-active-secret") {
		t.Fatalf("active log handling: %d %s", activeResp.StatusCode, activeBody)
	}
}

func TestLoggingBodySwitchOmitsPromptButKeepsRequestRecord(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-log","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"logged answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	cfg.Logging.Body = config.BoolPtr(false)
	s, addr := startWithStore(t, cfg, secret.NewMemStore())
	resp, body := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[{"role":"user","content":"private prompt text"}]}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	requestID := resp.Header.Get("X-Request-Id")
	if requestID == "" {
		t.Fatal("response omitted X-Request-Id")
	}
	detailResp, detail := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/"+requestID, nil)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("detail: %d %s", detailResp.StatusCode, detail)
	}
	text := string(detail)
	for _, required := range []string{`"type":"request"`, `"type":"route"`, `"type":"upstream_request"`, `"type":"result"`, `"body_omitted":true`} {
		if !strings.Contains(text, required) {
			t.Errorf("detail missing %q: %s", required, detail)
		}
	}
	for _, forbidden := range []string{"private prompt text", "logged answer", `"type":"upstream_event"`, `"type":"client_event"`} {
		if strings.Contains(text, forbidden) {
			t.Errorf("detail unexpectedly contains %q: %s", forbidden, detail)
		}
	}
	_, usageBody := httpJSON(t, addr, http.MethodGet, "/api/v1/usage", nil)
	var usage logstore.UsageReport
	if err := json.Unmarshal(usageBody, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Total.Usage == nil || usage.Total.Usage.TotalTokens != 18 {
		t.Fatalf("usage without body logging = %+v", usage.Total)
	}
	putResp, putBody := httpJSON(t, addr, http.MethodPut, "/api/v1/logging", map[string]bool{"body": true})
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("enable body: %d %s", putResp.StatusCode, putBody)
	}
	if !s.cfg.Snapshot().Logging.BodyValue() {
		t.Fatal("body switch was not persisted in the active config")
	}
	if !s.cfg.Snapshot().Logging.EnabledValue() {
		t.Fatal("enabling body logging disabled request logging")
	}
}

func TestStreamingLogsAreSplitBySSEEventAndCaptureExplicitUsage(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	resp, body := chatPost(t, addr, "/v1/chat/completions", []byte(`{"model":"gateway-default","messages":[],"stream":true}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, body = %s", resp.StatusCode, body)
	}
	id := resp.Header.Get("X-Request-Id")
	_, detail := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/"+id, nil)
	var decoded struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(detail, &decoded); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range decoded.Events {
		counts[event["type"].(string)]++
	}
	if counts["upstream_event"] != 3 || counts["client_event"] != 3 {
		t.Fatalf("stream event counts = %v, detail = %s", counts, detail)
	}
	_, usageBody := httpJSON(t, addr, http.MethodGet, "/api/v1/usage", nil)
	var usage logstore.UsageReport
	if err := json.Unmarshal(usageBody, &usage); err != nil {
		t.Fatal(err)
	}
	if usage.Total.Usage == nil || usage.Total.Usage.TotalTokens != 7 {
		t.Fatalf("stream usage = %+v", usage.Total)
	}
}

func TestConcurrentRequestsUseIndependentLogFiles(t *testing.T) {
	up := newFakeUpstream(t, nil)
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	s, addr := startWithStore(t, cfg, secret.NewMemStore())
	const count = 16
	ids := make(chan string, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, _ := chatPost(t, addr, "/v1/chat/completions", []byte(fmt.Sprintf(`{"model":"gateway-default","messages":[{"role":"user","content":"request-%d"}]}`, i)), nil)
			if resp.StatusCode == http.StatusOK {
				ids <- resp.Header.Get("X-Request-Id")
			}
		}(i)
	}
	wg.Wait()
	close(ids)
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != count {
		t.Fatalf("unique request ids = %d, want %d", len(unique), count)
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(s.cfg.Path()), cfg.Logging.Dir, "*", "*.jsonl"))
	if err != nil || len(files) != count {
		t.Fatalf("log files = %d, err = %v", len(files), err)
	}
}

func TestDoctorReportsLogSizeAndInterruptedFile(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	session, err := s.warnings.Open(config.DefaultLogDir, "req_interrupted", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Append("request", map[string]any{"client": "generic", "protocol": "chat", "body": map[string]any{"model": "m"}}); err != nil {
		t.Fatal(err)
	}
	report, _ := getDoctor(t, addr)
	if !report.Logs.Writable || report.Logs.SizeBytes == 0 || report.Logs.InterruptedFiles != 1 {
		t.Fatalf("logs doctor = %+v", report.Logs)
	}
	if report.Logs.LastParseable == nil || !*report.Logs.LastParseable {
		t.Fatalf("last_parseable = %v", report.Logs.LastParseable)
	}
}

func TestSameProtocolHTMLStreamIsLoggedFailed(t *testing.T) {
	const page = "<!doctype html><html lang=\"zh\"><body>marketing site</body></html>"
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, page)
	})
	cfg := dataPlaneConfig(up.URL, up.URL, false)
	cfg.Providers["openrouter"] = config.Provider{
		Name: "OpenRouter", Adapter: "openai-responses",
		BaseURL: up.URL, DefaultModel: "gpt-test",
	}
	cfg.Routes.Codex = config.Route{Provider: "openrouter", Model: "gpt-test"}
	_, addr := startWithStore(t, cfg, secret.NewMemStore())

	resp, body := chatPost(t, addr, "/c/codex/v1/responses",
		[]byte(`{"model":"gateway-default","input":[{"role":"user","content":"ping"}],"stream":true}`), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want forwarded 200, body %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "marketing site") {
		t.Fatalf("same-protocol must forward HTML bytes verbatim: %s", body)
	}
	id := resp.Header.Get("X-Request-Id")
	if id == "" {
		t.Fatal("missing X-Request-Id")
	}
	_, detail := httpJSON(t, addr, http.MethodGet, "/api/v1/logs/"+id, nil)
	text := string(detail)
	if !strings.Contains(text, `"code":"upstream_not_event_stream"`) {
		t.Fatalf("detail missing HTML-stream warning: %s", detail)
	}
	if !strings.Contains(text, `"status":"failed"`) {
		t.Fatalf("HTML stream must not be logged as success: %s", detail)
	}
	if !strings.Contains(text, "upstream returned HTML instead of an event stream") {
		t.Fatalf("detail missing HTML-stream error: %s", detail)
	}
}
