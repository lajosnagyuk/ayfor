package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

func TestRenameKeepsAppending(t *testing.T) {
	dir := t.TempDir()
	draft := filepath.Join(dir, "draft.strike")
	s, err := New(draft, 42, fakeClock(time.UnixMilli(1000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	s.Strike('a')

	named := filepath.Join(dir, "my-novel.strike")
	if err := s.Rename(named); err != nil {
		t.Fatal(err)
	}
	if s.Path != named {
		t.Fatalf("Path = %s, want %s", s.Path, named)
	}
	if _, err := os.Stat(draft); !os.IsNotExist(err) {
		t.Fatal("draft file must be gone after rename")
	}

	// The open handle must keep appending to the renamed file.
	s.Strike('b')
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(named)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	var got []rune
	for _, e := range f.Events {
		if e.Op == format.OpStrike {
			got = append(got, e.Rune)
		}
	}
	if string(got) != "ab" {
		t.Fatalf("strikes = %q, want ab", string(got))
	}
	res, err := format.Verify(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstBad != -1 {
		t.Fatal("hash chain broken across rename")
	}
}

func TestRenameRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "draft.strike"), 42, fakeClock(time.UnixMilli(1000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	target := filepath.Join(dir, "precious.strike")
	os.WriteFile(target, []byte("do not touch"), 0o644)
	if err := s.Rename(target); err == nil {
		t.Fatal("rename must refuse to overwrite")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "do not touch" {
		t.Fatal("existing file was clobbered")
	}
}

func TestRenameToSamePathIsNoop(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "same.strike")
	s, err := New(p, 42, fakeClock(time.UnixMilli(1000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Rename(p); err != nil {
		t.Fatal(err)
	}
}

// TestFailedMoveKeepsSessionLive pins the tear-down-last ordering in
// moveByCopy: the new file is built and locked BEFORE the original is
// touched, so a failure at any step leaves the session exactly as it was
// - writer intact, lock never released, flusher running - and typing
// continues. (The old implementation closed the original first; a failed
// move could leave a writerless session with a flusher goroutine leaked
// over a closed fd.)
func TestFailedMoveKeepsSessionLive(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "draft.strike"), 42, fakeClock(time.UnixMilli(1000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	s.Strike('a')

	// A destination in a directory that does not exist fails the copy.
	if err := s.moveByCopy(filepath.Join(dir, "no-such-dir", "gone.strike")); err == nil {
		t.Fatal("moveByCopy into a missing directory must fail")
	}
	if s.w == nil {
		t.Fatal("failed move tore down the session writer")
	}
	if s.stopFlush == nil {
		t.Fatal("failed move left the flusher stopped")
	}
	if _, err := s.Strike('b'); err != nil {
		t.Fatalf("typing after a failed move: %v", err)
	}

	// Close stops the flusher and the file holds both strikes.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.stopFlush != nil {
		t.Fatal("Close left the flusher goroutine running")
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	var got []rune
	for _, e := range f.Events {
		if e.Op == format.OpStrike {
			got = append(got, e.Rune)
		}
	}
	if string(got) != "ab" {
		t.Fatalf("strikes = %q, want ab", string(got))
	}
}

// TestCloseOnWriterlessSessionStopsFlusher pins the Close-order fix: even
// a session that somehow lost its writer must not leak the flusher
// goroutine past Close.
func TestCloseOnWriterlessSessionStopsFlusher(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "draft.strike"), 42, fakeClock(time.UnixMilli(1000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	s.w = nil // simulate a torn-down writer with the flusher still running
	if err := s.Close(); err == nil {
		t.Fatal("Close on a writerless session must report ErrClosed")
	}
	if s.stopFlush != nil {
		t.Fatal("Close left the flusher goroutine running")
	}
}

func TestMoveByCopyKeepsAppending(t *testing.T) {
	// Exercise the cross-volume fallback path directly.
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "draft.strike"), 42, fakeClock(time.UnixMilli(1000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	s.Strike('a')
	named := filepath.Join(dir, "copied.strike")
	if err := s.moveByCopy(named); err != nil {
		t.Fatal(err)
	}
	s.Strike('b')
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(named)
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	res, err := format.Verify(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstBad != -1 {
		t.Fatal("hash chain broken across copy-move")
	}
	var got []rune
	for _, e := range f.Events {
		if e.Op == format.OpStrike {
			got = append(got, e.Rune)
		}
	}
	if string(got) != "ab" {
		t.Fatalf("strikes = %q, want ab", string(got))
	}
}
