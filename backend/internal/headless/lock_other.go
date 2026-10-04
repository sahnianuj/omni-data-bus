//go:build !unix

package headless

import (
	"errors"
	"os"
)

// Lock is a lock file created exclusively; without flock a crashed run leaves it behind, and
// the next run says to delete it.
type Lock struct{ path string }

// Acquire takes the lock or returns STATE_LOCKED.
func (s StateDir) Acquire() (*Lock, error) {
	p := s.file(LockFile)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, &ConfigError{Code: CodeStateLocked, Message: "Another odb run is using " + s.Path + " (delete " + p + " if none is)."}
	}
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return &Lock{path: p}, nil
}

// Release frees the lock.
func (l *Lock) Release() {
	if l != nil {
		_ = os.Remove(l.path)
	}
}
