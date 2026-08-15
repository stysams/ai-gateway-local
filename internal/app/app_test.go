package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/process"
	"ai-gateway/internal/secret"
	"ai-gateway/internal/server"
)

// freePort reserves an ephemeral port and releases it immediately. The small
// race window is acceptable for tests; a failed bind makes serve exit 1 and
// the test fails with a clear message.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

func healthzOK(port int) bool {
	client := server.NewAdminClient(port)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	return client.Healthz(ctx) == nil
}

func portReleased(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond)
	if err != nil {
		return true
	}
	conn.Close()
	return false
}

// runServeInTest starts `serve` in a goroutine against a temp data dir and
// returns the exit code channel plus captured stdout/stderr.
func runServeInTest(t *testing.T, dataDir string, port int) (codeCh chan int, out, errOut *bytes.Buffer) {
	t.Helper()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	out, errOut = &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	codeCh = make(chan int, 1)
	go func() {
		codeCh <- Main([]string{"serve", "--port", strconv.Itoa(port)})
	}()
	return codeCh, out, errOut
}

func stopAndWait(t *testing.T, port int, codeCh chan int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.NewAdminClient(port).Shutdown(ctx)
	select {
	case code := <-codeCh:
		if code != ExitOK {
			t.Errorf("serve exited with %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit after shutdown")
	}
}

func TestServeEndToEnd(t *testing.T) {
	dataDir := t.TempDir()
	port := freePort(t)
	codeCh, _, errOut := runServeInTest(t, dataDir, port)

	waitFor(t, 10*time.Second, func() bool { return healthzOK(port) }, "serve to become healthy")

	// Default config must have been generated on disk.
	cfgPath := filepath.Join(dataDir, config.ConfigFileName)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("default config not generated: %v", err)
	}
	if _, err := config.Parse(data); err != nil {
		t.Fatalf("generated config invalid: %v", err)
	}
	// PID file must exist while running.
	if _, err := os.Stat(filepath.Join(dataDir, "gateway.pid.json")); err != nil {
		t.Errorf("pid file missing: %v", err)
	}

	// Management API status.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := server.NewAdminClient(port).Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Version != "0.1.0-dev" {
		t.Errorf("status version = %q", st.Version)
	}
	if st.PID != os.Getpid() {
		t.Errorf("status pid = %d, want %d", st.PID, os.Getpid())
	}
	if want := "127.0.0.1:" + strconv.Itoa(port); st.Listen != want {
		t.Errorf("status listen = %q, want %q", st.Listen, want)
	}

	// Graceful shutdown releases the port and the process exits 0.
	stopAndWait(t, port, codeCh)
	if !portReleased(port) {
		t.Error("port still bound after shutdown")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "gateway.pid.json")); !os.IsNotExist(err) {
		t.Error("pid file not cleaned up on exit")
	}
	if !strings.Contains(errOut.String(), "serving on http://127.0.0.1:"+strconv.Itoa(port)) {
		t.Errorf("stderr missing serving line:\n%s", errOut.String())
	}
}

func TestServeGeneratesDefaultConfig(t *testing.T) {
	dataDir := t.TempDir()
	port := freePort(t)
	codeCh, _, _ := runServeInTest(t, dataDir, port)
	waitFor(t, 10*time.Second, func() bool { return healthzOK(port) }, "serve to become healthy")
	stopAndWait(t, port, codeCh)

	data, err := os.ReadFile(filepath.Join(dataDir, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("generated config not valid: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("generated config version = %d", cfg.Version)
	}
	for _, key := range []struct {
		name string
		r    config.Route
	}{
		{"codex", cfg.Routes.Codex},
		{"claude", cfg.Routes.Claude},
		{"grok", cfg.Routes.Grok},
		{"generic", cfg.Routes.Generic},
	} {
		if key.r.Provider == "" || key.r.Model == "" {
			t.Errorf("generated config route %q incomplete: %+v", key.name, key.r)
		}
	}
	for _, id := range []string{"openrouter", "ollama"} {
		if _, ok := cfg.Providers[id]; !ok {
			t.Errorf("generated config missing provider %q", id)
		}
	}
}

func TestSecondInstanceFails(t *testing.T) {
	dataDir := t.TempDir()
	port1 := freePort(t)
	codeCh1, _, _ := runServeInTest(t, dataDir, port1)
	waitFor(t, 10*time.Second, func() bool { return healthzOK(port1) }, "first serve to become healthy")

	// Second instance on the same data dir must fail with the existing
	// listener address reported.
	out2 := &bytes.Buffer{}
	stdout, stderr = out2, out2
	code := Main([]string{"serve", "--port", strconv.Itoa(freePort(t))})
	if code != ExitError {
		t.Fatalf("second serve exit = %d, want %d (stderr: %s)", code, ExitError, out2.String())
	}
	if !strings.Contains(out2.String(), "already running") {
		t.Errorf("stderr missing 'already running':\n%s", out2.String())
	}
	if !strings.Contains(out2.String(), "127.0.0.1:"+strconv.Itoa(port1)) {
		t.Errorf("stderr missing existing listener address %d:\n%s", port1, out2.String())
	}

	stopAndWait(t, port1, codeCh1)
}

func TestServeBadConfigExits3AndDoesNotOverwrite(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	cfgPath := filepath.Join(dataDir, config.ConfigFileName)
	original := `version: 1
listen:
  port: 99999
providers: {}
routes:
  codex: {provider: x, model: y}
  claude: {provider: x, model: y}
  grok: {provider: x, model: y}
  generic: {provider: x, model: y}
`
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	code := Main([]string{"serve"})
	if code != ExitConfig {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitConfig, errOut.String())
	}
	if !strings.Contains(errOut.String(), cfgPath) {
		t.Errorf("stderr missing config path:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "listen.port") {
		t.Errorf("stderr missing locatable field:\n%s", errOut.String())
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Error("config file was overwritten on validation failure")
	}
}

func TestServeInvalidYAMLExits3AndDoesNotOverwrite(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	cfgPath := filepath.Join(dataDir, config.ConfigFileName)
	original := "version: [unclosed\nlisten:\n  port: 12600\n"
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	code := Main([]string{"serve"})
	if code != ExitConfig {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitConfig, errOut.String())
	}
	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Error("config file was overwritten on parse failure")
	}
}

func TestServeBadPortFlag(t *testing.T) {
	t.Setenv("AI_GATEWAY_DATA_DIR", t.TempDir())
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"serve", "--port", "80"}); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if code := Main([]string{"serve", "--port", "notaport"}); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
}

func TestServePortConflictLeavesNoPIDFile(t *testing.T) {
	// Occupy a port, then start serve on it. It must fail with exit 1 and
	// must not leave PID metadata behind: the pid file is written only after
	// the listener binds successfully.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"serve", "--port", strconv.Itoa(port)}); code != ExitError {
		t.Fatalf("serve on occupied port exit = %d, want %d (stderr: %s)", code, ExitError, errOut.String())
	}
	if !strings.Contains(errOut.String(), "listen on") {
		t.Errorf("stderr missing listen failure:\n%s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, process.PIDFileName)); !os.IsNotExist(err) {
		t.Error("pid file exists after port conflict failure")
	}
}

func TestStatusStaleLockFile(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	// A lock file left behind by an exited process (file exists, lock not
	// held) must be diagnosed as stale, not as an active lock.
	lockPath := filepath.Join(dataDir, process.LockFileName)
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"status"}); code != ExitNotRunning {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitNotRunning, errOut.String())
	}
	if !strings.Contains(errOut.String(), "not held") {
		t.Errorf("stderr should report a stale lock, got:\n%s", errOut.String())
	}
	if strings.Contains(errOut.String(), "held by a running gateway") {
		t.Errorf("stderr must not claim an active lock for a stale file:\n%s", errOut.String())
	}
}

func TestStatusActiveLock(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	// Simulate a live instance holding the lock while the management API is
	// unreachable (e.g. it is still starting up or hung).
	lock, err := process.AcquireLock(filepath.Join(dataDir, process.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"status"}); code != ExitNotRunning {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitNotRunning, errOut.String())
	}
	if !strings.Contains(errOut.String(), "held by a running gateway") {
		t.Errorf("stderr should report an active lock, got:\n%s", errOut.String())
	}
}

func TestStopNotRunning(t *testing.T) {
	t.Setenv("AI_GATEWAY_DATA_DIR", t.TempDir())
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"stop"}); code != ExitNotRunning {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitNotRunning, errOut.String())
	}
}

func TestStatusNotRunning(t *testing.T) {
	t.Setenv("AI_GATEWAY_DATA_DIR", t.TempDir())
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"status"}); code != ExitNotRunning {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitNotRunning, errOut.String())
	}
	if !strings.Contains(errOut.String(), "not running") {
		t.Errorf("stderr missing 'not running':\n%s", errOut.String())
	}
}

func TestStatusAndStopWhileRunning(t *testing.T) {
	dataDir := t.TempDir()
	port := freePort(t)
	codeCh, _, _ := runServeInTest(t, dataDir, port)
	waitFor(t, 10*time.Second, func() bool { return healthzOK(port) }, "serve to become healthy")

	// status against the running gateway exits 0 with a JSON payload.
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"status"}); code != ExitOK {
		t.Fatalf("status exit = %d (stderr: %s)", code, errOut.String())
	}
	var st server.StatusResponse
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("status output not JSON: %v\n%s", err, out.String())
	}
	if st.Listen != "127.0.0.1:"+strconv.Itoa(port) {
		t.Errorf("status listen = %q", st.Listen)
	}

	// stop against the running gateway exits 0.
	out.Reset()
	if code := Main([]string{"stop"}); code != ExitOK {
		t.Fatalf("stop exit = %d (stderr: %s)", code, errOut.String())
	}
	select {
	case code := <-codeCh:
		if code != ExitOK {
			t.Errorf("serve exit = %d after stop", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not exit after stop")
	}
	if !portReleased(port) {
		t.Error("port still bound after stop")
	}
}

func TestVersionCommand(t *testing.T) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"version"}); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	s := out.String()
	for _, want := range []string{"ai-gateway", "0.1.0-dev", "commit:", "built:", "go1.26"} {
		if !strings.Contains(s, want) {
			t.Errorf("version output missing %q:\n%s", want, s)
		}
	}
}

func TestHelpAndUnknownCommand(t *testing.T) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	if code := Main([]string{"help"}); code != ExitOK {
		t.Errorf("help exit = %d", code)
	}
	if !strings.Contains(out.String(), "usage:") {
		t.Errorf("help output missing usage:\n%s", out.String())
	}
	if code := Main([]string{"bogus"}); code != ExitUsage {
		t.Errorf("unknown command exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("stderr missing unknown command:\n%s", errOut.String())
	}
}

func TestServeWithCustomPortConfig(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	// Write a valid config with a custom port; serve must honor it.
	port := freePort(t)
	cfg := config.Defaults()
	cfg.Listen.Port = config.IntPtr(port)
	mgr := config.NewManager(filepath.Join(dataDir, config.ConfigFileName))
	if err := mgr.Write(cfg); err != nil {
		t.Fatal(err)
	}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	codeCh := make(chan int, 1)
	go func() { codeCh <- Main([]string{"serve"}) }()
	waitFor(t, 10*time.Second, func() bool { return healthzOK(port) }, "serve on configured port")
	stopAndWait(t, port, codeCh)
}

func TestServeMissingRequiredSecretFails(t *testing.T) {
	// A config whose provider declares a secret_ref without a readable
	// secret must fail startup with a remediation hint and exit code 3
	// (docs/v1-scheme.md §6.2). The default config has no secret_ref, so
	// this only triggers for configs that actually need keys.
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	cfg := config.Defaults()
	p := cfg.Providers["openrouter"]
	p.SecretRef = "provider.openrouter"
	cfg.Providers["openrouter"] = p
	mgr := config.NewManager(filepath.Join(dataDir, config.ConfigFileName))
	if err := mgr.Write(cfg); err != nil {
		t.Fatal(err)
	}

	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	stdout, stderr = out, errOut
	code := Main([]string{"serve"})
	if code != ExitConfig {
		t.Fatalf("exit = %d, want %d (stderr: %s)", code, ExitConfig, errOut.String())
	}
	// Two distinct platform behaviors must both fail startup: on a platform
	// with a working store the missing ref is named; on a platform whose
	// store is unavailable (macOS/Linux build-time implementations) the
	// unavailability itself is the failure.
	if !strings.Contains(errOut.String(), "provider.openrouter") &&
		!strings.Contains(errOut.String(), "system key store unavailable") {
		t.Errorf("stderr should name the missing ref or the unavailable store:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "修复") {
		t.Errorf("stderr should carry a remediation hint:\n%s", errOut.String())
	}
	// No listener may have been bound and no pid file left behind.
	if _, err := os.Stat(filepath.Join(dataDir, process.PIDFileName)); !os.IsNotExist(err) {
		t.Error("pid file exists after failed startup")
	}
}

func TestServeWithKeyedProviderAndSecretStarts(t *testing.T) {
	// On this machine (Windows, DPAPI available) a provider whose secret is
	// present in the real key store must start fine. The test uses the real
	// platform store rooted at a temp data dir, exercising the serve wiring
	// end to end (docs/v1-scheme.md task B Windows acceptance step 3).
	dataDir := t.TempDir()
	t.Setenv("AI_GATEWAY_DATA_DIR", dataDir)
	store := secret.New(dataDir)
	if err := store.Available(context.Background()); err != nil {
		t.Skipf("system key store not available on this machine: %v", err)
	}
	if err := store.Put(context.Background(), "provider.openrouter", []byte("sk-serve-test")); err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	p := cfg.Providers["openrouter"]
	p.SecretRef = "provider.openrouter"
	cfg.Providers["openrouter"] = p
	mgr := config.NewManager(filepath.Join(dataDir, config.ConfigFileName))
	if err := mgr.Write(cfg); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	codeCh, _, _ := runServeInTest(t, dataDir, port)
	waitFor(t, 10*time.Second, func() bool { return healthzOK(port) }, "serve to become healthy")

	// The plaintext key must never appear on disk anywhere under the data
	// root (Windows acceptance step 2: search ~/.ai-gateway).
	err := filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // locked pid/lock files may fail to read; not a key leak
		}
		if strings.Contains(string(data), "sk-serve-test") {
			t.Errorf("plaintext key found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// And the live gateway must still report ready (secret readable).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/readyz", port), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("readyz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz status = %d, want 200", resp.StatusCode)
	}

	stopAndWait(t, port, codeCh)
}
