package autostart

import (
	"errors"
	"path/filepath"
	"testing"

	"ai-gateway/internal/config"
)

type memoryRegistrar struct {
	status       Registration
	enableErr    error
	disableErr   error
	enableCalls  int
	disableCalls int
}

func (r *memoryRegistrar) Enable() error {
	r.enableCalls++
	if r.enableErr != nil {
		return r.enableErr
	}
	r.status = Registration{Exists: true, Enabled: true, Valid: true, Executable: `C:\Program Files\ai gateway\ai-gateway.exe`, Arguments: []string{ServeArgument}}
	return nil
}

func (r *memoryRegistrar) Disable() error {
	r.disableCalls++
	if r.disableErr != nil {
		return r.disableErr
	}
	r.status = Registration{}
	return nil
}

func (r *memoryRegistrar) Status() (Registration, error) { return r.status, nil }

func testConfigManager(t *testing.T) *config.Manager {
	t.Helper()
	manager := config.NewManager(filepath.Join(t.TempDir(), "config.yaml"))
	if err := manager.Write(config.Defaults()); err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestApplyEnableReadAndDisable(t *testing.T) {
	manager := testConfigManager(t)
	registrar := &memoryRegistrar{}
	status, err := Apply(registrar, manager, true)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || !status.Valid || !manager.Snapshot().Autostart.Enabled {
		t.Fatalf("enable status=%+v config=%v", status, manager.Snapshot().Autostart.Enabled)
	}
	status, err = Apply(registrar, manager, false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Enabled || manager.Snapshot().Autostart.Enabled {
		t.Fatalf("disable status=%+v config=%v", status, manager.Snapshot().Autostart.Enabled)
	}
}

func TestApplyDoesNotWriteConfigWhenEnableFails(t *testing.T) {
	manager := testConfigManager(t)
	registrar := &memoryRegistrar{enableErr: errors.New("scheduler denied")}
	_, err := Apply(registrar, manager, true)
	if err == nil {
		t.Fatal("Apply succeeded")
	}
	if manager.Snapshot().Autostart.Enabled {
		t.Fatal("config changed after registration failure")
	}
	if registrar.disableCalls != 1 {
		t.Fatalf("rollback disable calls = %d, want 1", registrar.disableCalls)
	}
}
