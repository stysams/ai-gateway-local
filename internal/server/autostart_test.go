package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"ai-gateway/internal/autostart"
	"ai-gateway/internal/config"
	"ai-gateway/internal/secret"
)

type fakeAutostartRegistrar struct {
	status autostart.Registration
}

func (r *fakeAutostartRegistrar) Enable() error {
	r.status = autostart.Registration{Exists: true, Enabled: true, Valid: true, Executable: `C:\Program Files\ai gateway\ai-gateway.exe`, Arguments: []string{"serve"}}
	return nil
}
func (r *fakeAutostartRegistrar) Disable() error {
	r.status = autostart.Registration{}
	return nil
}
func (r *fakeAutostartRegistrar) Status() (autostart.Registration, error) { return r.status, nil }

func TestAutostartAPIEnableReadAndDisable(t *testing.T) {
	s := newTestServerWithStore(t, config.Defaults(), secret.NewMemStore())
	s.autostart = &fakeAutostartRegistrar{}
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	resp, body := httpJSON(t, s.Addr(), http.MethodPut, "/api/v1/autostart", AutostartRequest{Enabled: true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable: %d %s", resp.StatusCode, body)
	}
	var enabled AutostartResponse
	if err := json.Unmarshal(body, &enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || !enabled.Valid || !s.cfg.Snapshot().Autostart.Enabled {
		t.Fatalf("enable response=%+v config=%v", enabled, s.cfg.Snapshot().Autostart.Enabled)
	}

	statusResp, statusBody := httpJSON(t, s.Addr(), http.MethodGet, "/api/v1/status", nil)
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d %s", statusResp.StatusCode, statusBody)
	}
	var status StatusResponse
	if err := json.Unmarshal(statusBody, &status); err != nil {
		t.Fatal(err)
	}
	if !status.AutostartEnabled {
		t.Fatal("status did not read enabled config")
	}
	doctor, _ := getDoctor(t, s.Addr())
	if !doctor.Autostart.OK || !doctor.Autostart.Registration.Valid {
		t.Fatalf("doctor autostart = %+v", doctor.Autostart)
	}

	resp, body = httpJSON(t, s.Addr(), http.MethodPut, "/api/v1/autostart", AutostartRequest{Enabled: false})
	if resp.StatusCode != http.StatusOK || s.cfg.Snapshot().Autostart.Enabled {
		t.Fatalf("disable: %d %s config=%v", resp.StatusCode, body, s.cfg.Snapshot().Autostart.Enabled)
	}
}
