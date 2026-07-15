package export

import (
	"io"

	"github.com/lajosnagyuk/ayfor/internal/atomicfile"
)

// AtomicCreate publishes a complete export only if the destination remains
// absent for the whole render. It closes the confirmation-to-publication race.
func AtomicCreate(path string, write func(io.Writer) error) error {
	return atomicfile.Create(path, write)
}

func AtomicCreateFile(path string, data []byte) error {
	return atomicfile.CreateFile(path, data)
}
