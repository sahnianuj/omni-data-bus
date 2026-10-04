//go:build unix

package headless

import (
	"errors"
	"os"
	"syscall"
)

// Lock holds <state>/.lock with flock, so two runners never share a state directory (OC-051
// AC7). The kernel releases it when the process dies, even on SIGKILL.
type Lock struct{ f *os.File }

// Acquire takes the lock or returns STATE_LOCKED.
func (s StateDir) Acquire() (*Lock, error) {
	f, err := os.OpenFile(s.file(LockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // G115: a file descriptor fits in int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &ConfigError{Code: CodeStateLocked, Message: "Another odb run is using the state directory " + s.Path + "."}
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release frees the lock.
func (l *Lock) Release() {
	if l != nil && l.f != nil {
		_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN) //nolint:gosec // G115: a file descriptor fits in int
		_ = l.f.Close()
	}
}
