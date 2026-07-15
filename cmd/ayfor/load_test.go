package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

// TestSameFileDetectsIdentity pins the core of the load-currently-open-draft
// guard: sameFile must recognise the same file through path spelling
// differences, and must not conflate two distinct files.
func TestSameFileDetectsIdentity(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "draft.strike")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "other.strike")
	if err := os.WriteFile(b, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Same file via a messy but equivalent path spelling.
	messy := filepath.Join(dir, "sub", "..", "draft.strike")

	if !sameFile(a, a) {
		t.Error("a should equal itself")
	}
	if !sameFile(a, messy) {
		t.Error("a should equal its unnormalised spelling")
	}
	if sameFile(a, b) {
		t.Error("distinct files must not compare equal")
	}
	// A non-existent path is safely not-the-same (import targets do not exist
	// yet), never a false positive that would suppress a real load.
	if sameFile(a, filepath.Join(dir, "ghost.strike")) {
		t.Error("missing file must not compare equal")
	}
}

// TestImportTextUnique pins that allocation itself is O_EXCL: a collision is
// retried without a Stat/create race.
func TestImportTextUnique(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "notes.strike")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := importTextUnique(base, "hello", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	want := filepath.Join(dir, "notes (2).strike")
	if s.Path != want {
		t.Fatalf("import path = %q, want %q", s.Path, want)
	}
	if b, err := os.ReadFile(base); err != nil || string(b) != "x" {
		t.Fatalf("colliding source changed to %q, error %v", b, err)
	}
}

// TestLoadSameOpenDraftIsNoop pins that loading the file already open does
// not reopen it (which would fork the log): the session pointer is
// unchanged, AND the branch's whole point - the flush - actually happened,
// so the on-disk file carries the freshest keystrokes.
func TestLoadSameOpenDraftIsNoop(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()
	for _, r := range "buffered" {
		if _, err := u.sess.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	before := u.sess
	u.loadPath(u.sess.Path)
	if u.sess != before {
		t.Fatal("loading the open draft replaced the live session")
	}
	b, err := os.ReadFile(u.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	strikes := 0
	for _, e := range f.Events {
		if e.Op == format.OpStrike {
			strikes++
		}
	}
	if strikes != len("buffered") {
		t.Fatalf("same-file load left %d strikes on disk, want %d - the flush this branch promises did not happen", strikes, len("buffered"))
	}
}
