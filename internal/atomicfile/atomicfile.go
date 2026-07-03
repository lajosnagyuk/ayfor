// Package atomicfile is the one place a whole file is written to disk:
// temp file in the destination directory, write, fsync, rename, directory
// fsync. Exports, the CLI importer and the session's crash repair all land
// their bytes through here, so the "either the whole file or nothing"
// promise has a single implementation to audit.
package atomicfile

import (
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically and durably. A failure
// mid-write (a full disk) leaves no truncated file under the final name -
// either the whole file lands or the previous file (if any) is untouched.
// The sync before the rename matters: without it a power loss can make
// the rename durable while the data is not, leaving a zero-length or
// partial file under the final name. The directory is fsynced too
// (best-effort; not all platforms support it) so the rename itself
// survives the crash.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
