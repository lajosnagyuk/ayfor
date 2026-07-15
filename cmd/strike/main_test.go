package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

func TestPackRejectsInvalidMaterializedAsset(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "io.ayfor.typewriters.classic")
	if err := os.CopyFS(source, os.DirFS("../../assets/typewriters/io.ayfor.typewriters.classic")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "fonts/CourierPrime-Regular.ttf"), []byte("not a font"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "bad.aytw")
	if err := packSource(source, target); err == nil {
		t.Fatal("pack published a package with an invalid font")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("failed pack left target behind: %v", err)
	}
}

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

func TestImportCanBindTypewriterPackage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(src, []byte("package-bound import"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.strike")
	archive := filepath.Join("..", "..", "assets", "typewriter-releases", "olympia-sm3-pica-1957-0.1.0.aytw")
	if err := cmdImport([]string{"-seed", "7", "-typewriter", archive, src, out}); err != nil {
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
	if f.Header.FormatVersion != format.Version2 || f.Header.Typewriter == nil || f.Header.Typewriter.ID != "io.ayfor.typewriters.olympia-sm3-pica-1957" {
		t.Fatalf("import header did not bind package: %+v", f.Header)
	}
}
