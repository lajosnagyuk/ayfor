//go:build !windows

package session

import "os"

func openExistingSession(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o644)
}
