package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

func TestConcurrentCreateSameProviderSingleWinner(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	const writers = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	created, conflicts := 0, 0
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, body := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
			mu.Lock()
			defer mu.Unlock()
			switch resp.StatusCode {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
				conflicts++
			default:
				t.Errorf("status=%d body=%s", resp.StatusCode, body)
			}
		}()
	}
	close(start)
	wg.Wait()
	if created != 1 || conflicts != writers-1 {
		t.Fatalf("created=%d conflicts=%d", created, conflicts)
	}
}

func TestConcurrentKeyGroupUpdatesRemainWhole(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	const writers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			model := "model-" + string(rune('a'+i))
			key := "key-" + string(rune('a'+i))
			payload := map[string]any{"name": "默认密钥", "enabled": true, "api_key": key, "endpoint": "/chat/completions", "default_model": model, "models": []map[string]any{{"id": model, "endpoint": "/chat/completions"}}}
			resp, body := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/openrouter/keys/default", payload)
			if resp.StatusCode != http.StatusConflict && resp.StatusCode != http.StatusOK {
				t.Errorf("status=%d body=%s", resp.StatusCode, body)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	group := s.cfg.Snapshot().Providers["openrouter"].KeyGroups["default"]
	if group.DefaultModel != "anthropic/claude-sonnet-4" {
		t.Fatalf("referenced group changed despite 409: %+v", group)
	}
}

func TestDecodeJSONStrictnessNewProviderContract(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	validBytes, err := json.Marshal(newDeepSeek())
	if err != nil {
		t.Fatal(err)
	}
	valid := string(validBytes)
	cases := []struct {
		name, body string
		status     int
	}{
		{"valid", valid, http.StatusCreated},
		{"second document", valid + `{"id":"other"}`, http.StatusBadRequest},
		{"unknown legacy field", strings.Replace(valid, `"base_url"`, `"adapter":"openai-chat","base_url"`, 1), http.StatusBadRequest},
		{"empty", "", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := httpRawForProvider(t, addr, tc.body)
			if resp.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d body=%s", resp.StatusCode, tc.status, body)
			}
		})
	}
}

func httpRawForProvider(t *testing.T, addr, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/api/v1/providers", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&raw); err != nil && resp.StatusCode != http.StatusBadRequest {
		t.Fatal(err)
	}
	return resp, raw
}
