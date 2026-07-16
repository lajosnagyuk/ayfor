//go:build !windows

package session

import (
	"os"
	"path/filepath"
)

func syncRenameDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func publishNoReplace(tmpName, newPath string) error {
	dir := filepath.Dir(newPath)
	if err := os.Link(tmpName, newPath); err != nil {
		return err
	}
	if err := syncRenameDir(dir); err != nil {
		_ = os.Remove(newPath)
		_ = syncRenameDir(dir)
		return err
	}
	// The final hard link is already durable. Temp cleanup failure can only
	// leave a hidden orphan, never a corrupt apparent manuscript.
	_ = os.Remove(tmpName)
	_ = syncRenameDir(dir)
	return nil
}

func createRepairTemp(dir string) (*os.File, error) {
	return os.CreateTemp(dir, ".strike-repair-*.tmp")
}

func publishRepair(path, tmpName string, old, tmp *os.File) error {
	if err := ensurePathNamesFile(path, old); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	_ = old.Close()
	return nil
}

func closeAndRemoveOwned(path string, f *os.File) (pathErr, removeErr, closeErr error) {
	pathErr = ensurePathNamesFile(path, f)
	if pathErr == nil {
		removeErr = os.Remove(path)
	}
	closeErr = f.Close()
	return
}
