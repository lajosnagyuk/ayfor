//go:build windows

package typewriter

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

func publishRegistryClaim(tmpName, claim string) error {
	from, err := windows.UTF16PtrFromString(tmpName)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(claim)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
	if errors.Is(err, syscall.ERROR_ALREADY_EXISTS) || errors.Is(err, syscall.ERROR_FILE_EXISTS) {
		return nil
	}
	return err
}
