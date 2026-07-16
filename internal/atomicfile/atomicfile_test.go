package atomicfile

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateRefusesDestinationCreatedDuringGeneration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.pdf")
	err := Create(path, func(w io.Writer) error {
		if _, err := w.Write([]byte("new export")); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("rival contents"), 0o644)
	})
	if err == nil {
		t.Fatal("Create replaced a destination published during generation")
	}
	b, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(b) != "rival contents" {
		t.Fatalf("rival destination changed to %q", b)
	}
}

func TestCreateFilePublishesCompleteContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	if err := CreateFile(path, []byte("complete")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "complete" {
		t.Fatalf("contents %q, error %v", b, err)
	}
}
