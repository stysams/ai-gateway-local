//go:build !windows

package process

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes a non-blocking exclusive flock. LOCK_NB never blocks; a live
// instance holding the lock makes this call fail immediately.
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// isLockConflict reports whether err is an "already locked" failure.
func isLockConflict(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
