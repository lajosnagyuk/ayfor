package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

// TestSecondOpenIsRefused pins that a live .strike file cannot be opened by
// a second writer, which would fork the append log and hash chain.
func TestSecondOpenIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "locked.strike")
	s, err := New(path, 42, fakeClock(time.UnixMilli(1_000_000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := Open(path, nil); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open: want ErrLocked, got %v", err)
	}
	if _, err := New(filepath.Join(dir, "other.strike"), 1, nil); err != nil {
		t.Fatalf("a different file should still open: %v", err)
	}
}

// TestLockReleasedOnClose pins that the lock is advisory and released, so a
// closed file reopens normally.
func TestLockReleasedOnClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reuse.strike")
	s, err := New(path, 42, fakeClock(time.UnixMilli(1_000_000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Strike('a'); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, nil)
	if err != nil {
		t.Fatalf("reopen after close should succeed: %v", err)
	}
	s2.Close()
}

// TestSaveAsRecoversFromStickyFlushError pins that after a flush failure
// sets the sticky error, Rename to a fresh location still succeeds and
// carries every keystroke (the buffered tail included), rather than
// dead-ending because Rename's own Check() writes into the dead buffer.
func TestSaveAsRecoversFromStickyFlushError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "draft.strike")
	s, err := New(path, 42, fakeClock(time.UnixMilli(1_000_000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	// Type a few keystrokes; they buffer.
	for _, r := range "hello" {
		if _, err := s.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a failed background flush to the current volume: the tail is
	// stranded in bw.buf and bw.err is now sticky.
	s.bw.mu.Lock()
	s.bw.err = errors.New("simulated disk full")
	s.bw.mu.Unlock()

	// A normal keystroke now fails (the sticky error surfaces) - proving the
	// precondition of the bug.
	if _, err := s.Strike('!'); err == nil {
		t.Fatal("expected the sticky error to surface on the next keystroke")
	}

	// Save-As must recover: the sticky error forces Rename through the copy
	// path, which rebuilds a fresh writer over the full image.
	newPath := filepath.Join(dir, "named.strike")
	if err := s.Rename(newPath); err != nil {
		t.Fatalf("Save-As should recover from the sticky error, got %v", err)
	}
	if s.Path != newPath {
		t.Fatalf("path not updated: %s", s.Path)
	}
	// Typing works again after recovery.
	if _, err := s.Strike('x'); err != nil {
		t.Fatalf("typing should resume after recovery: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The recovered file must decode cleanly, verify, and contain the tail
	// that was buffered at the time of the failure.
	b, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatalf("recovered file does not decode: %v", err)
	}
	if f.Truncated {
		t.Fatal("recovered file is truncated")
	}
	res, err := format.Verify(f)
	if err != nil || res.FirstBad != -1 {
		t.Fatalf("recovered file fails hash verify: err=%v firstBad=%d", err, res.FirstBad)
	}
	var typed []rune
	for _, e := range f.Events {
		if e.Op == format.OpStrike {
			typed = append(typed, e.Rune)
		}
	}
	if string(typed) != "hellox" {
		t.Fatalf("lost keystrokes across recovery: got %q, want %q", string(typed), "hellox")
	}
}
