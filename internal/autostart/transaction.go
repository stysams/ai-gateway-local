package autostart

import (
	"fmt"

	"ai-gateway/internal/config"
)

// ApplyError reports a failed registration/config transaction. RollbackErr
// is non-nil only when the operating-system state may differ from config.
type ApplyError struct {
	Operation   string
	Err         error
	RollbackErr error
}

func (e *ApplyError) Error() string {
	if e.RollbackErr != nil {
		return fmt.Sprintf("%s: %v; rollback failed: %v", e.Operation, e.Err, e.RollbackErr)
	}
	return fmt.Sprintf("%s: %v", e.Operation, e.Err)
}

func (e *ApplyError) Unwrap() error { return e.Err }

// Partial reports whether manual repair may be required.
func (e *ApplyError) Partial() bool { return e.RollbackErr != nil }

// Apply changes the live per-user registration, verifies it, then persists
// the matching config flag. Callers must serialize this with other config
// writes.
func Apply(registrar Registrar, manager *config.Manager, enabled bool) (Registration, error) {
	before, err := registrar.Status()
	if err != nil {
		return Registration{}, &ApplyError{Operation: "read login registration", Err: err}
	}
	current := manager.Snapshot()
	if current == nil {
		return Registration{}, &ApplyError{Operation: "read config", Err: fmt.Errorf("config not loaded")}
	}

	if enabled {
		if !before.Exists || !before.Enabled || !before.Valid {
			if err := registrar.Enable(); err != nil {
				return Registration{}, rollbackError(registrar, before, "enable login registration", err)
			}
		}
	} else if before.Exists {
		if err := registrar.Disable(); err != nil {
			return Registration{}, rollbackError(registrar, before, "disable login registration", err)
		}
	}

	after, err := registrar.Status()
	if err != nil {
		return Registration{}, rollbackError(registrar, before, "verify login registration", err)
	}
	if enabled && (!after.Enabled || !after.Valid) {
		return Registration{}, rollbackError(registrar, before, "verify login registration", fmt.Errorf("registration is not enabled and valid: %s", after.Issue))
	}
	if !enabled && after.Exists {
		return Registration{}, rollbackError(registrar, before, "verify login registration", fmt.Errorf("registration still exists"))
	}

	current.Autostart.Enabled = enabled
	if err := manager.Write(current); err != nil {
		return Registration{}, rollbackError(registrar, before, "write config", err)
	}
	return after, nil
}

func rollbackError(registrar Registrar, before Registration, operation string, cause error) *ApplyError {
	var rollbackErr error
	if before.Exists && before.Enabled && before.Valid {
		rollbackErr = registrar.Enable()
	} else {
		rollbackErr = registrar.Disable()
	}
	if before.Exists && (!before.Enabled || !before.Valid) && rollbackErr == nil {
		rollbackErr = fmt.Errorf("the prior invalid or disabled registration cannot be restored exactly; it was removed")
	}
	return &ApplyError{Operation: operation, Err: cause, RollbackErr: rollbackErr}
}
