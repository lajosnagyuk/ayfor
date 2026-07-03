//go:build windows

package session

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes a non-blocking exclusive lock over the whole file via
// LockFileEx (the same range convention cmd/go's lockedfile uses). Only a
// genuinely-held lock (ERROR_LOCK_VIOLATION) maps to ErrLocked; any other
// failure is reported as itself.
func lockFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, ^uint32(0), ^uint32(0), new(windows.Overlapped))
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return fmt.Errorf("%w: %s", ErrLocked, f.Name())
	}
	return fmt.Errorf("lock %s: %w", f.Name(), err)
}
