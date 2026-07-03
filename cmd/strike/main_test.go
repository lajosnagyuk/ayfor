package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

// TestImportWritesAtomically pins the fix for the truncated-output bug:
// import encodes fully and lands via temp+rename, so a failure leaves
// nothing at the user's chosen path - never a half-written .strike.
func TestImportWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(src, []byte("the quick brown fox\njumps over the lazy dog\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Happy path: the output decodes and its hash chain verifies.
	out := filepath.Join(dir, "out.strike")
	if err := cmdImport([]string{"-seed", "7", src, out}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	res, err := format.Verify(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstBad != -1 {
		t.Fatal("imported file's hash chain is broken")
	}

	// Failure path: an unwritable destination directory must error and
	// leave no file (not even a truncated one) at the target path.
	roDir := filepath.Join(dir, "ro")
	if err := os.Mkdir(roDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0o755) })
	target := filepath.Join(roDir, "blocked.strike")
	if err := cmdImport([]string{src, target}); err == nil {
		t.Fatal("import into an unwritable directory must fail")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("failed import left a file at the target path: %v", err)
	}
	entries, err := os.ReadDir(roDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed import left debris in the target directory: %v", entries)
	}
}
