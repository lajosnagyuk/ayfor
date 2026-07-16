//go:build windows

package session

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows does not expose portable directory fsync through os.File. The copy
// path still FlushFileBuffers the new file before retiring the old pathname;
// NTFS/ReFS journal namespace changes. Keeping this platform hook explicit
// avoids pretending a Unix directory handle primitive works here.
func syncRenameDir(path string) error { return nil }

func publishNoReplace(tmpName, newPath string) error {
	from, err := windows.UTF16PtrFromString(tmpName)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	// Deliberately omit MOVEFILE_REPLACE_EXISTING: publication is atomic and
	// refuses a rival destination. The temp handle was opened share-delete.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

// createRepairTemp reopens the temporary with delete sharing. Windows cannot
// rename an ordinary Go os.File while it is open; delete sharing lets the
// locked replacement handle survive publication under the manuscript name.
func createRepairTemp(dir string) (*os.File, error) {
	created, err := os.CreateTemp(dir, ".strike-repair-*.tmp")
	if err != nil {
		return nil, err
	}
	name := created.Name()
	if err := created.Close(); err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}

func publishRepair(path, tmpName string, old, tmp *os.File) error {
	if err := ensurePathNamesFile(path, old); err != nil {
		return err
	}
	// The original Go handle does not share delete access. Close it before the
	// replacement; if another writer enters the gap, its own non-delete-sharing
	// handle makes Rename fail rather than allowing us to replace its file.
	if err := old.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func closeAndRemoveOwned(path string, f *os.File) (pathErr, removeErr, closeErr error) {
	info, err := f.Stat()
	if err != nil {
		return err, nil, f.Close()
	}
	if pathErr = ensurePathNamesFile(path, f); pathErr != nil {
		return pathErr, nil, f.Close()
	}
	closeErr = f.Close()
	if closeErr != nil {
		return nil, nil, closeErr
	}
	// Revalidate after releasing the non-delete-sharing handle so a replaced
	// path is never removed blindly.
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		if err == nil {
			err = syscall.ERROR_FILE_NOT_FOUND
		}
		return err, nil, nil
	}
	removeErr = os.Remove(path)
	return nil, removeErr, nil
}
