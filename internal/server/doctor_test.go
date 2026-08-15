package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

func getDoctor(t *testing.T, addr string) (DoctorReport, []byte) {
	t.Helper()
	resp, data := httpJSON(t, addr, http.MethodGet, "/api/v1/doctor", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("doctor status = %d, body = %s, want 200", resp.StatusCode, data)
	}
	var report DoctorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode doctor report %s: %v", data, err)
	}
	return report, data
}

func TestDoctorHealthy(t *testing.T) {
	_, addr := startWithStore(t, config.Defaults(), secret.NewMemStore())
	report, data := getDoctor(t, addr)
	if !report.Config.OK {
		t.Errorf("config.ok = false: %s", data)
	}
	if !report.SecretStore.OK {
		t.Errorf("secret_store.ok = false: %s", data)
	}
	if report.SecretStore.Platform != "memory" {
		t.Errorf("secret_store.platform = %q, want memory", report.SecretStore.Platform)
	}
	if !report.Secrets.OK || report.Secrets.Required != 0 {
		t.Errorf("secrets = %+v, want ok with 0 required", report.Secrets)
	}
}

func TestDoctorReportsMissingSecret(t *testing.T) {
	cfg := configWithSecretRef()
	_, addr := startWithStore(t, cfg, secret.NewMemStore())
	report, _ := getDoctor(t, addr)
	if report.Secrets.OK {
		t.Error("secrets.ok = true, want false (missing secret)")
	}
	if report.Secrets.Required != 1 {
		t.Errorf("secrets.required = %d, want 1", report.Secrets.Required)
	}
	if len(report.Secrets.Missing) != 1 || !strings.Contains(report.Secrets.Missing[0], "provider.openrouter") {
		t.Errorf("secrets.missing = %v, want the openrouter ref", report.Secrets.Missing)
	}
}

func TestDoctorReportsUnavailableStore(t *testing.T) {
	cfg := configWithSecretRef()
	_, addr := startWithStore(t, cfg, brokenStore{})
	report, data := getDoctor(t, addr)
	if report.SecretStore.OK {
		t.Error("secret_store.ok = true, want false")
	}
	if report.SecretStore.Hint == "" {
		t.Errorf("secret_store.hint empty: %s", data)
	}
	if !strings.Contains(report.SecretStore.Error, "unavailable") {
		t.Errorf("secret_store.error = %q", report.SecretStore.Error)
	}
}

func TestDoctorReportsOrphanSecrets(t *testing.T) {
	// Default config references no secret at all; a ref present in the store
	// is an orphan (docs/v1-scheme.md §6.3: doctor reports orphan key
	// references). Orphans are a warning: OK stays true because every
	// required secret is readable.
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.ghost", []byte("sk-orphan")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "provider.kept", []byte("sk-kept")); err != nil {
		t.Fatal(err)
	}
	_, addr := startWithStore(t, config.Defaults(), store)
	report, _ := getDoctor(t, addr)
	if !report.Secrets.OK {
		t.Error("secrets.ok = false, want true (orphans are a warning, not a missing-secret failure)")
	}
	if len(report.Secrets.Orphans) != 2 {
		t.Fatalf("orphans = %v, want the two unreferenced refs", report.Secrets.Orphans)
	}
	// provider.kept must be listed even though it was written before ghost.
	found := false
	for _, ref := range report.Secrets.Orphans {
		if ref == "provider.kept" {
			found = true
		}
	}
	if !found {
		t.Errorf("orphans = %v, missing provider.kept", report.Secrets.Orphans)
	}
}

func TestDoctorMissingAndOrphanTogether(t *testing.T) {
	// A provider whose secret exists plus an orphan ref: missing list only
	// carries the truly missing refs, and the orphan is reported separately.
	cfg := configWithSecretRef()
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.openrouter", []byte("sk-ok")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "provider.ghost", []byte("sk-ghost")); err != nil {
		t.Fatal(err)
	}
	_, addr := startWithStore(t, cfg, store)
	report, _ := getDoctor(t, addr)
	if !report.Secrets.OK {
		t.Errorf("secrets.ok = false, want true (all required secrets present): %+v", report.Secrets)
	}
	if len(report.Secrets.Missing) != 0 {
		t.Errorf("missing = %v, want none", report.Secrets.Missing)
	}
	if len(report.Secrets.Orphans) != 1 || report.Secrets.Orphans[0] != "provider.ghost" {
		t.Errorf("orphans = %v, want [provider.ghost]", report.Secrets.Orphans)
	}
}

func TestDoctorConfigNotLoaded(t *testing.T) {
	// A server whose config manager never loaded a snapshot: doctor must
	// report the config problem instead of pretending health.
	mgr := config.NewManager(t.TempDir() + "/config.yaml")
	s := New(mgr, secret.NewMemStore(), "test-version", 1)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := s.Addr()
	go s.Serve()
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	resp, data := httpJSON(t, addr, http.MethodGet, "/api/v1/doctor", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("doctor status = %d, body = %s", resp.StatusCode, data)
	}
	var report DoctorReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Config.OK || !strings.Contains(report.Config.Error, "not loaded") {
		t.Errorf("config = %+v, want not-loaded failure", report.Config)
	}
}

func TestDoctorReportNeverLeaksSecrets(t *testing.T) {
	store := secret.NewMemStore()
	if err := store.Put(context.Background(), "provider.ghost", []byte("sk-top-secret-xyz")); err != nil {
		t.Fatal(err)
	}
	_, addr := startWithStore(t, config.Defaults(), store)
	_, data := getDoctor(t, addr)
	if strings.Contains(string(data), "sk-top-secret-xyz") {
		t.Errorf("doctor report leaked secret material: %s", data)
	}
}
