//go:build windows

package session

import (
	"errors"
	"fmt"
	"os"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"golang.org/x/sys/windows"
)

// lockFile takes a non-blocking exclusive lock on one sentinel byte beyond
// the largest valid STRIKE image. Windows byte-range locks are mandatory, so
// locking the manuscript itself would prevent harmless readers (including
// exports and crash-safe tests) from opening it. The sentinel still gives
// every Ayfor writer one shared exclusion point without making the document
// bytes unreadable.
//
// Only a genuinely-held lock (ERROR_LOCK_VIOLATION) maps to ErrLocked; any
// other failure is reported as itself.
func lockFile(f *os.File) error {
	const lockOffset = uint64(format.MaxFileBytes) + 1
	overlapped := &windows.Overlapped{
		Offset:     uint32(lockOffset),
		OffsetHigh: uint32(lockOffset >> 32),
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, overlapped)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return fmt.Errorf("%w: %s", ErrLocked, f.Name())
	}
	return fmt.Errorf("lock %s: %w", f.Name(), err)
}
