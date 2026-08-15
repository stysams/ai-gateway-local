package process

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLockExclusiveAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)

	l1, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer l1.Release()

	// A second lock on the same file must fail while the first is held.
	if _, err := AcquireLock(path); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second AcquireLock err = %v, want ErrAlreadyRunning", err)
	}

	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// After release the lock must be acquirable again.
	l2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock after release: %v", err)
	}
	if err := l2.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestLockReleaseIsIdempotent(t *testing.T) {
	l, err := AcquireLock(filepath.Join(t.TempDir(), LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("double Release: %v", err)
	}
}

func TestProbeLockAbsent(t *testing.T) {
	state, err := ProbeLock(filepath.Join(t.TempDir(), "absent.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if state != LockAbsent {
		t.Errorf("state = %v, want LockAbsent", state)
	}
}

func TestProbeLockHeldAndFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)

	l, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ProbeLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if state != LockHeld {
		t.Errorf("while held: state = %v, want LockHeld", state)
	}

	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	// The file persists after release; the probe must report Free, not Held.
	state, err = ProbeLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if state != LockFree {
		t.Errorf("after release: state = %v, want LockFree", state)
	}
}

func TestProbeLockNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LockFileName)

	// Probing an absent file must not create it.
	if _, err := ProbeLock(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("ProbeLock created the lock file")
	}

	// Probing a free file must not leave it locked: a subsequent AcquireLock
	// must succeed immediately.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeLock(path); err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock after ProbeLock: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestPIDFileRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), PIDFileName)
	info := PIDFile{
		PID:       1234,
		StartedAt: "2026-08-14T08:00:00Z",
		Listen:    "127.0.0.1:12600",
		Version:   "0.1.0-dev",
	}
	if err := WritePIDFile(path, info); err != nil {
		t.Fatalf("WritePIDFile: %v", err)
	}
	got, err := ReadPIDFile(path)
	if err != nil {
		t.Fatalf("ReadPIDFile: %v", err)
	}
	if got.PID != 1234 || got.Listen != "127.0.0.1:12600" || got.Version != "0.1.0-dev" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestPIDFileMissing(t *testing.T) {
	got, err := ReadPIDFile(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("ReadPIDFile of missing file: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}
