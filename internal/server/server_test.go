package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/point"
	"ai-gateway/internal/secret"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	mgr := config.NewManager(filepath.Join(dir, "config.yaml"))
	if err := mgr.Write(configWithRouteKeys()); err != nil {
		t.Fatalf("write config: %v", err)
	}
	s := New(mgr, secret.NewMemStore(), "test-version", 4242)
	s.points = testPointManager(dir)
	return s
}

// newTestServerWithStore builds a server with a custom key store, writing a
// config derived from cfg. It is used by the secret-focused readyz/doctor
// tests.
func newTestServerWithStore(t *testing.T, cfg *config.Config, store secret.Store) *Server {
	t.Helper()
	dir := t.TempDir()
	mgr := config.NewManager(filepath.Join(dir, "config.yaml"))
	if err := mgr.Write(cfg); err != nil {
		t.Fatalf("write config: %v", err)
	}
	s := New(mgr, store, "test-version", 4242)
	s.points = testPointManager(dir)
	return s
}

func testPointManager(dataRoot string) *point.Manager {
	home := filepath.Join(dataRoot, "test-home")
	_ = os.MkdirAll(home, 0o700)
	return point.NewWithOptions(dataRoot, point.Options{
		HomeDir:       home,
		LookupEnv:     func(string) (string, bool) { return "", false },
		CommandExists: func(string) bool { return false },
		Environment:   point.NewMemoryEnvironment(),
		LoadCodexBundledCatalog: func() ([]byte, error) {
			return os.ReadFile(filepath.Join("..", "..", "testdata", "point", "codex-bundled-catalog.json"))
		},
	})
}

// start binds 127.0.0.1:0 (kernel-assigned port, no race on port choice) and
// serves in a goroutine. It returns the server, its loopback address and the
// port, and registers a cleanup that shuts it down.
func start(t *testing.T) (*Server, string, int) {
	t.Helper()
	s := newTestServer(t)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := s.Addr()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("listener address %q is not loopback", addr)
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve() }()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Shutdown: %v", err)
		}
		select {
		case err := <-serveErr:
			if err != nil {
				t.Errorf("Serve returned: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("Serve did not return after Shutdown")
		}
	})
	return s, addr, port
}

func httpGet(t *testing.T, addr, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, body
}

func httpPost(t *testing.T, addr, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post("http://"+addr+path, "application/json", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp, body
}

func waitDialFail(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %s still accepting connections", addr)
}

func TestHealthz(t *testing.T) {
	_, addr, _ := start(t)
	resp, body := httpGet(t, addr, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "ok" {
		t.Errorf("body = %s", body)
	}
}

func TestReadyzOK(t *testing.T) {
	_, addr, _ := start(t)
	resp, body := httpGet(t, addr, "/readyz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "ok" {
		t.Errorf("body = %s", body)
	}
}

func TestReadyzNotReady(t *testing.T) {
	// A manager whose snapshot was never loaded must answer 503.
	mgr := config.NewManager(filepath.Join(t.TempDir(), "config.yaml"))
	s := New(mgr, secret.NewMemStore(), "test-version", 1)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
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

	resp, body := httpGet(t, addr, "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "not_ready") {
		t.Errorf("body = %s", body)
	}
}

func configWithRouteKeys() *config.Config {
	c := config.Defaults()
	for providerID, provider := range c.Providers {
		for keyID, group := range provider.KeyGroups {
			group.APIKey = "sk-test-" + providerID + "-" + keyID
			provider.KeyGroups[keyID] = group
		}
		c.Providers[providerID] = provider
	}
	return c
}

// Retained only for the doctor unit tests that exercise the historical store
// report independently from the current routed key-group readiness contract.
func configWithSecretRef() *config.Config {
	c := configWithRouteKeys()
	p := c.Providers["openrouter"]
	p.SecretRef = "provider.openrouter"
	c.Providers["openrouter"] = p
	return c
}

func TestReadyzMissingSecret(t *testing.T) {
	// A key group used by a route has no API key: not ready.
	cfg := config.Defaults()
	store := secret.NewMemStore()
	s := newTestServerWithStore(t, cfg, store)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	go s.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})

	resp, body := httpGet(t, addr, "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "has no api_key") {
		t.Errorf("body should name the missing key-group value:\n%s", body)
	}
}

func TestReadyzSecretPresent(t *testing.T) {
	cfg := configWithRouteKeys()
	store := secret.NewMemStore()
	s := newTestServerWithStore(t, cfg, store)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	go s.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})

	resp, body := httpGet(t, addr, "/readyz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, body)
	}
}

func TestReadyzIgnoresLegacyStoreAvailability(t *testing.T) {
	cfg := configWithRouteKeys()
	s := newTestServerWithStore(t, cfg, brokenStore{})
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	go s.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})

	resp, body := httpGet(t, addr, "/readyz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", resp.StatusCode, body)
	}
}

func TestReadyzKeylessConfigIgnoresStore(t *testing.T) {
	// Store availability is irrelevant, but a routed key group without a key
	// still makes the gateway unready.
	cfg := config.Defaults()
	s := newTestServerWithStore(t, cfg, brokenStore{})
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	go s.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})

	resp, body := httpGet(t, addr, "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", resp.StatusCode, body)
	}
}

func TestReadyzIgnoresUnusedKeylessGroup(t *testing.T) {
	cfg := configWithRouteKeys()
	provider := cfg.Providers["openrouter"]
	provider.KeyGroups["unused"] = config.KeyGroup{Name: "Unused", Endpoint: "/v1/chat/completions", DefaultModel: "unused", Models: []config.ProviderModel{{ID: "unused"}}}
	cfg.Providers["openrouter"] = provider
	s := newTestServerWithStore(t, cfg, brokenStore{})
	if errs := CheckRequiredKeyGroups(s.cfg.View()); len(errs) != 0 {
		t.Fatalf("unused keyless group blocked readiness: %v", errs)
	}
}

// brokenStore simulates a platform whose system key store is unavailable.
type brokenStore struct{ secret.Store }

func (brokenStore) Available(context.Context) error             { return secret.ErrUnavailable }
func (brokenStore) Get(context.Context, string) ([]byte, error) { return nil, secret.ErrUnavailable }
func (brokenStore) Put(context.Context, string, []byte) error   { return secret.ErrUnavailable }
func (brokenStore) Delete(context.Context, string) error        { return secret.ErrUnavailable }

func TestStatusFields(t *testing.T) {
	_, addr, port := start(t)
	resp, body := httpGet(t, addr, "/api/v1/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var st StatusResponse
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatal(err)
	}
	if st.Version != "test-version" {
		t.Errorf("Version = %q", st.Version)
	}
	if st.PID != 4242 {
		t.Errorf("PID = %d, want 4242", st.PID)
	}
	if want := fmt.Sprintf("127.0.0.1:%d", port); st.Listen != want {
		t.Errorf("Listen = %q, want %q", st.Listen, want)
	}
	if !st.LoggingEnabled {
		t.Error("LoggingEnabled = false, want true (default)")
	}
	if st.AutostartEnabled {
		t.Error("AutostartEnabled = true, want false (default)")
	}
	for _, name := range []string{"codex", "claude", "claude-desktop", "grok", "generic"} {
		cs, ok := st.Clients[name]
		if !ok {
			t.Errorf("client %q missing", name)
			continue
		}
		wantState := "client_not_installed"
		if name == "generic" {
			wantState = "unknown"
		}
		if cs.PointState != wantState {
			t.Errorf("client %q point_state = %q, want %q", name, cs.PointState, wantState)
		}
		rs, ok := st.Routes[name]
		if !ok || rs.Provider == "" || rs.Model == "" {
			t.Errorf("route %q missing or empty: %+v", name, rs)
		}
	}
}

func TestShutdownReleasesPort(t *testing.T) {
	s, addr, _ := start(t)
	// Wire the app-level behavior: when shutdown is requested via the API,
	// the owning process calls http.Server.Shutdown.
	done := make(chan struct{})
	go func() {
		<-s.ShutdownRequested()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		close(done)
	}()

	resp, body := httpPost(t, addr, "/api/v1/shutdown")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s, want 202", resp.StatusCode, body)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ShutdownRequested never fired")
	}
	waitDialFail(t, addr, 5*time.Second)
}

func TestLoopbackOnly(t *testing.T) {
	_, addr, _ := start(t)
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("listener address = %q, want 127.0.0.1", addr)
	}
	// Confirm the address is not a wildcard bind.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Errorf("bound host = %q, want 127.0.0.1", host)
	}
}

func TestListenRejectsNonLoopback(t *testing.T) {
	s := newTestServer(t)
	for _, addr := range []string{
		"0.0.0.0:12600",   // wildcard
		"localhost:12600", // resolved hostname
		"[::1]:12600",     // IPv6 loopback (out of contract)
		"192.168.1.23:12600",
		"127.0.0.2:12600", // loopback range but not the contract address
	} {
		if err := s.Listen(addr); err == nil {
			t.Errorf("Listen(%q) succeeded, want refusal", addr)
		} else if !strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("Listen(%q) error %q does not mention 127.0.0.1", addr, err)
		}
	}
	// The loopback address must still be accepted after the rejections.
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatalf("Listen(127.0.0.1:0) after rejections: %v", err)
	}
}

func TestBindConflictFails(t *testing.T) {
	// Occupy a port, then verify the server fails with a clear error instead
	// of picking another port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	s := newTestServer(t)
	if err := s.ListenAndServe(addr); err == nil {
		t.Fatal("ListenAndServe on occupied port succeeded, want error")
	} else if !strings.Contains(err.Error(), addr) {
		t.Errorf("error does not mention the address %q: %v", addr, err)
	}
}

func TestUnknownPath404(t *testing.T) {
	_, addr, _ := start(t)
	resp, _ := httpGet(t, addr, "/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestAdminClientShutdownAndHealthz(t *testing.T) {
	_, _, port := start(t)
	client := NewAdminClient(port)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Healthz(ctx); err != nil {
		t.Errorf("Healthz: %v", err)
	}
	st, err := client.Status(ctx)
	if err != nil {
		t.Errorf("Status: %v", err)
	}
	if st.Version != "test-version" {
		t.Errorf("Status.Version = %q", st.Version)
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestAdminClientUnreachable(t *testing.T) {
	// Use a port that is almost certainly free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	client := NewAdminClient(port)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Healthz(ctx); err == nil {
		t.Error("Healthz against dead port succeeded")
	}
	if err := client.Shutdown(ctx); err == nil {
		t.Error("Shutdown against dead port succeeded")
	}
}
