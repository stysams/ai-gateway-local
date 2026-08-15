package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

// trackingStore wraps any Store and records every slice returned by Get, so
// tests can observe whether the caller zeroed the bytes afterwards: secret
// material returned from the store must not outlive its use
// (docs/v1-scheme.md §6.1).
type trackingStore struct {
	inner secret.Store
	mu    sync.Mutex
	got   [][]byte
}

func (s *trackingStore) Get(ctx context.Context, ref string) ([]byte, error) {
	b, err := s.inner.Get(ctx, ref)
	if err == nil {
		s.mu.Lock()
		s.got = append(s.got, b)
		s.mu.Unlock()
	}
	return b, err
}

func (s *trackingStore) Put(ctx context.Context, ref string, value []byte) error {
	return s.inner.Put(ctx, ref, value)
}

func (s *trackingStore) Delete(ctx context.Context, ref string) error {
	return s.inner.Delete(ctx, ref)
}

func (s *trackingStore) Available(ctx context.Context) error {
	return s.inner.Available(ctx)
}

// allZeroed reports whether every recorded Get slice was zeroed by its
// caller.
func (s *trackingStore) allZeroed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.got {
		for _, v := range b {
			if v != 0 {
				return false
			}
		}
	}
	return true
}

// TestProviderTxZeroesReadKeysOnEveryPath observes that every key slice read
// via Store.Get inside a write transaction is zeroed on all return paths:
// success, key-write failure and partial failure.
func TestProviderTxZeroesReadKeysOnEveryPath(t *testing.T) {
	ctx := context.Background()

	t.Run("successful update", func(t *testing.T) {
		store := &trackingStore{inner: secret.NewMemStore()}
		s, addr := startWithStore(t, config.Defaults(), store)
		resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create: %s", data)
		}
		req := newDeepSeek()
		req["api_key"] = "sk-new-key"
		resp, data = httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", req)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("update: %s", data)
		}
		// The old key read inside the transaction must be zeroed even
		// though the transaction succeeded.
		if !store.allZeroed() {
			t.Error("a key slice returned by Get survived the successful transaction unzeroed")
		}
		// Zeroing must not corrupt the store: the new key is still there.
		got, err := s.secrets.Get(ctx, "provider.deepseek")
		if err != nil {
			t.Fatal(err)
		}
		defer secret.Zero(got)
		if string(got) != "sk-new-key" {
			t.Errorf("stored key = %q, want sk-new-key", got)
		}
	})

	t.Run("new key write fails", func(t *testing.T) {
		inner := &putFailer{MemStore: secret.NewMemStore(), failFrom: 2}
		store := &trackingStore{inner: inner}
		s, addr := startWithStore(t, config.Defaults(), store)
		resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
		if resp.StatusCode != http.StatusCreated {
			t.Fatal("create failed (Put #1 must succeed)")
		}
		// The transaction's Put of the new key (Put #2) fails: the old key
		// read before it must still be zeroed on this return path.
		req := newDeepSeek()
		req["api_key"] = "sk-never-written"
		resp, data := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", req)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("update status = %d, want 500: %s", resp.StatusCode, data)
		}
		if !store.allZeroed() {
			t.Error("old key slice survived the failed key-write path unzeroed")
		}
		got, err := s.secrets.Get(ctx, "provider.deepseek")
		if err != nil {
			t.Fatal(err)
		}
		defer secret.Zero(got)
		if string(got) != "sk-test-secret-1" {
			t.Errorf("stored key = %q, want the original key", got)
		}
	})

	t.Run("partial failure", func(t *testing.T) {
		inner := &putFailer{MemStore: secret.NewMemStore(), failFrom: 1 << 30}
		store := &trackingStore{inner: inner}
		s, addr := startWithStore(t, config.Defaults(), store)
		resp, _ := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
		if resp.StatusCode != http.StatusCreated {
			t.Fatal("create failed")
		}
		breakConfigDir(t, s)
		inner.failFrom = inner.puts + 2 // new-key Put succeeds, restore Put fails
		req := newDeepSeek()
		req["api_key"] = "sk-partial"
		resp, data := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", req)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("update status = %d, want 500: %s", resp.StatusCode, data)
		}
		if !store.allZeroed() {
			t.Error("old key slice survived the partial-failure path unzeroed")
		}
	})
}

// TestConcurrentProviderUpdatesRemainConsistent races full-field PUTs that
// touch disjoint fields. Under full-replace semantics the final state must
// equal one complete request body — never a torn mix of two requests — and
// every request must answer 200. The sharper secret/config consistency
// check lives in TestConcurrentDeleteAndUpdateSameProvider.
func TestConcurrentProviderUpdatesRemainConsistent(t *testing.T) {
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %s", data)
	}

	const n = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	failures := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := map[string]any{
				"id":            "deepseek",
				"name":          "DeepSeek",
				"adapter":       "openai-chat",
				"base_url":      "https://api.deepseek.com",
				"default_model": "deepseek-chat",
			}
			if i%2 == 0 {
				req["name"] = "Renamed-" + strconv.Itoa(i)
			} else {
				req["base_url"] = "https://api.example.com/" + strconv.Itoa(i)
			}
			<-start
			resp, body := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", req)
			if resp.StatusCode != http.StatusOK {
				mu.Lock()
				failures++
				mu.Unlock()
				t.Errorf("PUT %d: status = %d, body = %s", i, resp.StatusCode, body)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if failures > 0 {
		t.Fatalf("%d concurrent updates failed", failures)
	}

	// The final provider must equal one complete request body: name and
	// base_url always come from the same request, never from a torn mix.
	disk, err := os.ReadFile(s.cfg.Path())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(disk)
	if err != nil {
		t.Fatalf("final config unparseable: %v", err)
	}
	p, ok := cfg.Providers["deepseek"]
	if !ok {
		t.Fatal("provider vanished")
	}
	fromEven := strings.HasPrefix(p.Name, "Renamed-") && p.BaseURL == "https://api.deepseek.com"
	fromOdd := p.Name == "DeepSeek" && strings.HasPrefix(p.BaseURL, "https://api.example.com/")
	if !fromEven && !fromOdd {
		t.Errorf("torn provider state (fields from different requests): %+v", p)
	}
}

// TestConcurrentCreateSameProviderSingleWinner races creates of the same
// provider id: exactly one may win, the rest must be 409 conflicts. Without
// serialization several writers could all pass the existence check against
// the same stale snapshot.
func TestConcurrentCreateSameProviderSingleWinner(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	const n = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for i := 0; i < n; i++ {
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
				// expected for every loser
			default:
				t.Errorf("status = %d, body = %s", resp.StatusCode, body)
			}
		}()
	}
	close(start)
	wg.Wait()
	if created != 1 {
		t.Errorf("created = %d, want exactly 1 winner (the rest must be 409)", created)
	}
}

// TestConcurrentDeleteAndUpdateSameProvider races a DELETE against a PUT
// that carries a new key. Without transaction serialization the delete's
// config removal and key deletion can interleave with the update's
// snapshot→key write→config write and leave a torn state: a provider that
// exists in config but whose secret was deleted, or an orphan secret after
// the provider is gone. With txMu the final state is always consistent:
// provider and secret either both exist (PUT rebuilt it with its own key) or
// both are gone.
func TestConcurrentDeleteAndUpdateSameProvider(t *testing.T) {
	ctx := context.Background()
	s, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())

	for round := 0; round < 12; round++ {
		resp, data := httpJSON(t, addr, http.MethodPost, "/api/v1/providers", newDeepSeek())
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("round %d create: %s", round, data)
		}

		putReq := newDeepSeek()
		putReq["api_key"] = "sk-race-new"
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			resp, _ := httpJSON(t, addr, http.MethodPut, "/api/v1/providers/deepseek", putReq)
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
				t.Errorf("round %d PUT status = %d", round, resp.StatusCode)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			resp, _ := httpJSON(t, addr, http.MethodDelete, "/api/v1/providers/deepseek", nil)
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
				t.Errorf("round %d DELETE status = %d", round, resp.StatusCode)
			}
		}()
		close(start)
		wg.Wait()

		// Consistency invariant: config and key store must agree on whether
		// the provider/secret pair exists.
		disk, err := os.ReadFile(s.cfg.Path())
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Parse(disk)
		if err != nil {
			t.Fatalf("round %d: final config unparseable: %v", round, err)
		}
		if _, ok := cfg.Providers["deepseek"]; ok {
			// Provider present: its secret must exist too (the PUT that
			// rebuilt it wrote its own key).
			got, err := s.secrets.Get(ctx, "provider.deepseek")
			if err != nil {
				t.Fatalf("round %d: provider exists but secret missing (torn state): %v", round, err)
			}
			if string(got) != "sk-race-new" {
				t.Errorf("round %d: secret = %q, want the rebuilding PUT's key", round, got)
			}
			secret.Zero(got)
		} else {
			// Provider gone: no orphan secret may remain.
			if _, err := s.secrets.Get(ctx, "provider.deepseek"); !errors.Is(err, secret.ErrNotFound) {
				t.Fatalf("round %d: provider gone but secret left behind: %v", round, err)
			}
		}
	}
}

// TestConcurrentDeleteAndUpdateDirect races the provider write transactions
// by calling them directly (no HTTP round trip), forcing far more
// interleavings than the HTTP-level test. Callers mimic the handlers by
// holding s.txMu; after every transaction the config and the key store must
// agree (provider present ⇔ secret present). A temporary variant without
// the lock reproduced torn states within seconds, so this test pins the
// serialization itself.
func TestConcurrentDeleteAndUpdateDirect(t *testing.T) {
	ctx := context.Background()
	s := newTestServerWithStore(t, config.Defaults(), secret.NewMemStore())
	base := config.Provider{
		Name: "DeepSeek", Adapter: "openai-chat",
		BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat",
		SecretRef: "provider.deepseek",
	}
	if _, err := s.upsertProvider(ctx, "deepseek", base, []byte("sk-init")); err != nil {
		t.Fatal(err)
	}

	// check runs inside the transaction lock right after a transaction
	// completes: the config snapshot and the key store must agree on the
	// provider/secret pair.
	check := func() {
		cfg := s.cfg.Snapshot()
		_, inConfig := cfg.Providers["deepseek"]
		b, err := s.secrets.Get(ctx, "provider.deepseek")
		if b != nil {
			secret.Zero(b)
		}
		inStore := err == nil
		if inConfig != inStore {
			t.Errorf("torn state: provider in config = %v, secret in store = %v", inConfig, inStore)
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // writer
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.txMu.Lock()
			_, _ = s.upsertProvider(ctx, "deepseek", base, []byte("sk-writer"))
			check()
			s.txMu.Unlock()
		}
	}()
	go func() { // deleter
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			s.txMu.Lock()
			_, _ = s.deleteProvider(ctx, "deepseek")
			check()
			s.txMu.Unlock()
		}
	}()
	time.Sleep(700 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Final state must also be consistent.
	cfg := s.cfg.Snapshot()
	_, inConfig := cfg.Providers["deepseek"]
	b, err := s.secrets.Get(ctx, "provider.deepseek")
	if b != nil {
		secret.Zero(b)
	}
	inStore := err == nil
	if inConfig != inStore {
		t.Errorf("final torn state: provider in config = %v, secret in store = %v", inConfig, inStore)
	}
}

// httpRaw sends an exact raw body without JSON-marshalling, for decode
// strictness tests.
func httpRaw(t *testing.T, addr, method, path, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
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

// TestDecodeJSONStrictness pins the management API request contract: exactly
// one JSON document, no unknown fields, no unparsable body
// (docs/v1-scheme.md §9.3). A misspelled api_key must fail loudly instead of
// silently losing the key.
func TestDecodeJSONStrictness(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	valid := `{"id":"x","name":"X","adapter":"openai-chat","base_url":"https://x.ai","default_model":"m"}`
	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"valid single document", valid, http.StatusCreated, ""},
		{"trailing second document", valid + `{"id":"y"}`, http.StatusBadRequest, "invalid_json"},
		{"trailing garbage", valid + ` garbage`, http.StatusBadRequest, "invalid_json"},
		{"unknown field", `{"id":"x","name":"X","adapter":"openai-chat","base_url":"https://x.ai","default_model":"m","apiKey":"sk-typo"}`, http.StatusBadRequest, "invalid_json"},
		{"empty body", "", http.StatusBadRequest, "invalid_json"},
		{"not json at all", "definitely not json", http.StatusBadRequest, "invalid_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, data := httpRaw(t, addr, http.MethodPost, "/api/v1/providers", tc.body)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d, body = %s", resp.StatusCode, tc.status, data)
			}
			if tc.code == "" {
				return
			}
			var eb errorBody
			if err := json.Unmarshal(data, &eb); err != nil {
				t.Fatalf("error response not in the unified shape: %s", data)
			}
			if eb.Error.Code != tc.code {
				t.Errorf("code = %q, want %q", eb.Error.Code, tc.code)
			}
			// Error messages must never echo the request body.
			if strings.Contains(string(data), "sk-typo") {
				t.Errorf("error response leaked body content: %s", data)
			}
		})
	}
}

// TestCreateProviderBodyTooLarge verifies the 128 MiB limit
// (docs/v1-scheme.md §9.3) answers 413 with the unified error shape.
func TestCreateProviderBodyTooLarge(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	// ~129 MiB of JSON: exceeds maxAdminBody (128 MiB).
	big := `{"id":"big","name":"` + strings.Repeat("x", (129<<20)) + `"}`
	resp, data := httpRaw(t, addr, http.MethodPost, "/api/v1/providers", big)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body = %s", resp.StatusCode, data)
	}
	var eb errorBody
	if err := json.Unmarshal(data, &eb); err != nil {
		t.Fatalf("error response not in the unified shape: %s", data)
	}
	if eb.Error.Code != "request_too_large" {
		t.Errorf("code = %q, want request_too_large", eb.Error.Code)
	}
}
