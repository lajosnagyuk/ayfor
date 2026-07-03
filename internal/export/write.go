package export

import "github.com/lajosnagyuk/ayfor/internal/atomicfile"

// AtomicWriteFile writes an export to disk atomically and durably; see
// atomicfile.WriteFile for the guarantees. Kept as a package-level name so
// every export call site reads as what it is.
func AtomicWriteFile(path string, data []byte) error {
	return atomicfile.WriteFile(path, data)
}
