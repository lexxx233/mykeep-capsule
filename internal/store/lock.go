package store

import (
	"errors"
	"os"
)

// ErrAlreadyRunning means another joyvend process holds the drive lock (PLAN §10.4:
// single-writer; two processes re-sealing one blob would corrupt/lose data).
var ErrAlreadyRunning = errors.New("joyvend: another instance is already using this drive")

type fileLock struct{ f *os.File }

// IsRunning reports whether another joyvend instance holds the drive lock (for the
// doctor command). It attempts to acquire and immediately release the lock.
func IsRunning(blobPath string) bool {
	l, err := acquireLock(blobPath + ".lock")
	if err != nil {
		return true
	}
	l.release()
	return false
}
