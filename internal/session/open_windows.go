//go:build windows

package session

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openExistingSession(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil && errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		// A repaired or copy-moved manuscript remains open through a Windows
		// delete-sharing handle. An ordinary Go handle is rejected before it
		// can contend on our byte-range sentinel, but the user-facing condition
		// is still exactly "another live writer owns this manuscript".
		return nil, fmt.Errorf("%w: %s", ErrLocked, path)
	}
	return f, err
}
