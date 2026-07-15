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
// import encodes fully and lands via no-replace publication, so a collision
// leaves the user's existing file untouched and no temporary debris behind.
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

	// Failure path: a pre-existing destination must be preserved byte for
	// byte. Unlike Unix permission bits, this assertion has the same meaning
	// on Windows and when the test process has elevated privileges.
	collisionDir := filepath.Join(dir, "collision")
	if err := os.Mkdir(collisionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(collisionDir, "existing.strike")
	sentinel := []byte("do not overwrite")
	if err := os.WriteFile(target, sentinel, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdImport([]string{src, target}); err == nil {
		t.Fatal("import over an existing destination must fail")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != string(sentinel) {
		t.Fatalf("failed import changed destination to %q: %v", got, err)
	}
	entries, err := os.ReadDir(collisionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
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
