package sys

import (
	"errors"
	"os"
	"time"
)

// TryAcquireLock attempts to create the lock file with O_CREATE|O_EXCL. If the
// lock already exists and is older than staleAfter, it is removed and the
// acquisition is retried once (handles crashed/killed workers/refreshers).
func TryAcquireLock(lockPath string, staleAfter time.Duration) bool {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		_ = f.Close()
		return true
	}
	if !errors.Is(err, os.ErrExist) {
		return false
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		return false
	}
	if time.Since(info.ModTime()) <= staleAfter {
		return false
	}
	_ = os.Remove(lockPath)
	f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL, 0o644)
	if err == nil {
		_ = f.Close()
		return true
	}
	return false
}
