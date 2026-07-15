// Package atomicfile is the one place a whole file is written to disk:
// temp file in the destination directory, write, fsync, rename, directory
// fsync. Exports, the CLI importer and the session's crash repair all land
// their bytes through here, so the "either the whole file or nothing"
// promise has a single implementation to audit.
package atomicfile

import (
	"io"
	"os"
	"path/filepath"
)

// Create publishes a newly generated file atomically and refuses to replace
// any destination, including one created while generation is in progress.
// The temporary is fully written and synced before an atomic hard-link claim.
func Create(path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".create-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	// The requested final name is already complete and durable. A temp cleanup
	// error can leave only a hidden orphan and must not misreport publication.
	_ = os.Remove(tmpName)
	return nil
}

func CreateFile(path string, data []byte) error {
	return Create(path, func(w io.Writer) error {
		n, err := w.Write(data)
		if err == nil && n != len(data) {
			return io.ErrShortWrite
		}
		return err
	})
}

// WriteFile writes data to path atomically and durably. A failure
// mid-write (a full disk) leaves no truncated file under the final name -
// either the whole file lands or the previous file (if any) is untouched.
// The sync before the rename matters: without it a power loss can make
// the rename durable while the data is not, leaving a zero-length or
// partial file under the final name. The directory is fsynced too
// (best-effort; not all platforms support it) so the rename itself
// survives the crash.
func WriteFile(path string, data []byte) error {
	return Write(path, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// Write streams an atomic replacement through write without retaining the
// full result in memory. The callback must return every generation/write
// failure so the temporary file can be discarded instead of published.
func Write(path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".write-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if err := write(tmp); err != nil {
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
