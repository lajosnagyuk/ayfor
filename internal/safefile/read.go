// Package safefile contains bounded, descriptor-based reads for untrusted
// paths selected at the CLI or in a file dialog.
package safefile

import (
	"fmt"
	"io"
)

// ReadRegular opens path once, requires that exact descriptor to be a regular
// file, and reads at most limit+1 bytes. The platform opener is non-blocking
// and no-follow where available, so a FIFO or symlink swap cannot turn a size
// check into an unbounded/blocking read.
func ReadRegular(path string, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("invalid read limit %d", limit)
	}
	f, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, fmt.Errorf("%s exceeds %d-byte safety limit", path, limit)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s grew beyond %d-byte safety limit while reading", path, limit)
	}
	return b, nil
}
