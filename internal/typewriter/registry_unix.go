//go:build !windows

package typewriter

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func publishRegistryClaim(tmpName, claim string) error {
	if err := os.Link(tmpName, claim); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return err
	}
	d, err := os.Open(filepath.Dir(claim))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
