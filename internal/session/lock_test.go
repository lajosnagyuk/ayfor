package session

import (
	"bytes"
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

func TestRepairReplacementKeepsNewInodeLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair-lock.strike")
	s, err := New(path, 42, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte{0x80}) // truncated varint tail
	_ = f.Close()
	repaired, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repaired.Close()
	if _, err := Open(path, nil); !errors.Is(err, ErrLocked) {
		t.Fatalf("second open after inode-replacing repair = %v, want ErrLocked", err)
	}
}

func TestOpenRefusesSymlinkManuscriptPath(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.strike")
	s, err := New(realPath, 42, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.strike")
	if err := os.Symlink(realPath, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	before, _ := os.ReadFile(realPath)
	if _, err := Open(link, nil); err == nil {
		t.Fatal("opened manuscript through symlink")
	}
	after, _ := os.ReadFile(realPath)
	if !bytes.Equal(before, after) {
		t.Fatal("symlink open mutated its target")
	}
}

func TestAbortRollsBackPreparedSessions(t *testing.T) {
	dir := t.TempDir()
	createdPath := filepath.Join(dir, "unused-draft.strike")
	created, err := New(createdPath, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Abort(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(createdPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aborted draft still exists: %v", err)
	}

	path := filepath.Join(dir, "existing.strike")
	original, err := New(path, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	prepared, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.SetTouch(85); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Abort(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, before) {
		t.Fatal("aborting an unused opened session left marker/state events behind")
	}
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

func TestCloseRefusesReplacedPathAndSaveAsRecoversDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live.strike")
	s, err := New(path, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Strike('x'); err != nil {
		t.Fatal(err)
	}
	orphanName := filepath.Join(dir, "unlinked-original")
	if err := os.Rename(path, orphanName); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("rival"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("close silently accepted replaced manuscript pathname")
	}
	recovered := filepath.Join(dir, "recovered.strike")
	err = s.Rename(recovered)
	if err != nil && !errors.Is(err, ErrMoveCleanup) {
		t.Fatal(err)
	}
	b, err := os.ReadFile(recovered)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range f.Events {
		found = found || e.Op == format.OpStrike && e.Rune == 'x'
	}
	if !found {
		t.Fatal("Save As did not recover strike from locked descriptor")
	}
	rival, err := os.ReadFile(path)
	if err != nil || string(rival) != "rival" {
		t.Fatalf("recovery altered rival replacement: %q, %v", rival, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
