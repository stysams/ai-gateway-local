//go:build windows

package process

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes a non-blocking exclusive byte-range lock on the lock file.
// LOCKFILE_FAIL_IMMEDIATELY never blocks; a live instance owning the lock
// makes this call fail immediately with ERROR_LOCK_VIOLATION.
func lockFile(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{})
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}

// isLockConflict reports whether err is a Windows "already locked" failure.
func isLockConflict(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
