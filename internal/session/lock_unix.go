//go:build unix

package session

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile takes a non-blocking exclusive advisory flock on the handle.
// Only a genuinely-held lock (EWOULDBLOCK/EAGAIN) maps to ErrLocked; any
// other failure (ENOLCK on NFS, EIO, an unsupported filesystem) is a
// condition we cannot diagnose as "another window" and is reported as
// itself.
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return fmt.Errorf("%w: %s", ErrLocked, f.Name())
	}
	return fmt.Errorf("lock %s: %w", f.Name(), err)
}
