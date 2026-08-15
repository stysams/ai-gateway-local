// Package process provides single-instance locking, the diagnostic PID file
// and cross-platform graceful-shutdown signals for the ai-gateway headless
// process. Platform-specific behavior lives in _windows.go / _unix.go files
// selected by build tags; no runtime platform branching is allowed here.
package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrAlreadyRunning is returned by AcquireLock when another live instance
// holds the lock.
var ErrAlreadyRunning = errors.New("ai-gateway is already running")

// LockFileName is the fixed name of the single-instance mutex file.
const LockFileName = "gateway.lock"

// LockPath returns the lock file path inside a data root.
func LockPath(dataDir string) string { return filepath.Join(dataDir, LockFileName) }

// FileLock is an exclusive advisory lock on gateway.lock held for the
// lifetime of one gateway instance. It is the only authority for
// single-instance semantics: a stale PID file alone never blocks startup.
type FileLock struct {
	file *os.File
	path string
}

// AcquireLock opens (creating if needed) the lock file and takes a
// non-blocking exclusive lock. On conflict it returns ErrAlreadyRunning.
func AcquireLock(path string) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if isLockConflict(err) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("lock file %s: %w", path, err)
	}
	return &FileLock{file: f, path: path}, nil
}

// Release unlocks and closes the lock file. It is safe to call multiple
// times; later calls are no-ops.
func (l *FileLock) Release() error {
	if l.file == nil {
		return nil
	}
	unlockErr := unlockFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return fmt.Errorf("unlock %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close lock file %s: %w", l.path, closeErr)
	}
	return nil
}

// LockState describes the result of a non-blocking lock probe.
type LockState int

const (
	// LockAbsent means the lock file does not exist at all.
	LockAbsent LockState = iota
	// LockFree means the lock file exists but no live instance holds it
	// (a stale file left behind by an earlier process).
	LockFree
	// LockHeld means a live instance currently holds the lock.
	LockHeld
)

// ProbeLock reports whether the lock file at path is currently held by a
// live instance. It is non-blocking and leaves no side effect: it never
// creates the file, and if it acquires the lock for probing it releases it
// again before returning. The lock file alone is not liveness evidence — it
// persists after a clean shutdown — so callers must distinguish LockHeld
// from LockFree/LockAbsent instead of treating file existence as an active
// lock (docs/v1-scheme.md §14.2).
func ProbeLock(path string) (LockState, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LockAbsent, nil
		}
		return LockAbsent, fmt.Errorf("open lock file %s: %w", path, err)
	}
	defer f.Close()
	if err := lockFile(f); err != nil {
		if isLockConflict(err) {
			return LockHeld, nil
		}
		return LockAbsent, fmt.Errorf("lock file %s: %w", path, err)
	}
	// We acquired the lock, so no live instance holds it. Release it again
	// so probing leaves no side effect; the file itself is left in place.
	if err := unlockFile(f); err != nil {
		return LockAbsent, fmt.Errorf("unlock %s: %w", path, err)
	}
	return LockFree, nil
}

// PIDFileName is the fixed name of the diagnostic PID file.
const PIDFileName = "gateway.pid.json"

// PIDPath returns the PID file path inside a data root.
func PIDPath(dataDir string) string { return filepath.Join(dataDir, PIDFileName) }

// PIDFile is diagnostic metadata only. It must never be treated as evidence
// that a process is alive; the lock is the only liveness authority.
type PIDFile struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	Listen    string `json:"listen"`
	Version   string `json:"version"`
	StoppedAt string `json:"stopped_at,omitempty"`
}

// WritePIDFile persists the PID file atomically (temp file + rename in the
// same directory).
func WritePIDFile(path string, info PIDFile) error {
	data, err := marshalJSON(info)
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0o600)
}

// ReadPIDFile reads the PID file. A missing file returns (nil, nil).
func ReadPIDFile(path string) (*PIDFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pid file %s: %w", path, err)
	}
	var info PIDFile
	if err := unmarshalJSON(data, &info); err != nil {
		return nil, fmt.Errorf("parse pid file %s: %w", path, err)
	}
	return &info, nil
}
