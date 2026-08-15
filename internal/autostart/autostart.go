// Package autostart manages per-user login registration for the gateway.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	// TaskName is the stable operating-system registration name.
	TaskName = "ai-gateway"
	// ServeArgument is the only argument used by a login registration.
	ServeArgument = "serve"
)

var ErrNotSupported = errors.New("autostart is not supported on this platform")

// Registration is the live operating-system registration, not the config
// preference. Valid is true only when the executable, argument and login
// trigger match the gateway contract.
type Registration struct {
	Exists     bool     `json:"exists"`
	Enabled    bool     `json:"enabled"`
	Valid      bool     `json:"valid"`
	Executable string   `json:"executable,omitempty"`
	Arguments  []string `json:"arguments,omitempty"`
	Issue      string   `json:"issue,omitempty"`
}

// Registrar manages the current user's login registration.
type Registrar interface {
	Enable() error
	Disable() error
	Status() (Registration, error)
}

// New returns the platform registrar for executable. The path is converted
// to an absolute, cleaned path so registration validation is stable.
func New(executable string) Registrar {
	abs, err := filepath.Abs(executable)
	if err != nil {
		return &unavailableRegistrar{err: fmt.Errorf("resolve executable path: %w", err)}
	}
	return newPlatform(filepath.Clean(abs))
}

// NewCurrentExecutable returns a registrar for the running executable.
func NewCurrentExecutable() Registrar {
	executable, err := os.Executable()
	if err != nil {
		return &unavailableRegistrar{err: fmt.Errorf("locate current executable: %w", err)}
	}
	return New(executable)
}

type unavailableRegistrar struct{ err error }

func (r *unavailableRegistrar) Enable() error                 { return r.err }
func (r *unavailableRegistrar) Disable() error                { return r.err }
func (r *unavailableRegistrar) Status() (Registration, error) { return Registration{}, r.err }

type commandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ai-gateway-autostart-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
