package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

// fakeClock advances a fixed step per call, so deltas are predictable.
func fakeClock(start time.Time, step time.Duration) Clock {
	t := start
	return func() time.Time {
		t = t.Add(step)
		return t
	}
}

// countStrikes decodes the file on disk and counts strike events,
// verifying deltas along the way.
func countStrikes(t *testing.T, path string, wantDelta uint64) int {
	t.Helper()
	b, err := os.ReadFile(path)
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
			if e.DeltaMS != wantDelta {
				t.Fatalf("delta = %d, want %d from the fake clock", e.DeltaMS, wantDelta)
			}
		}
	}
	return strikes
}

// TestWritesAreBatchedThenDurable pins the buffered-write model
// (owner-accepted 2026-07-03): keystrokes sit in memory between flushes,
// the opening events are on disk immediately (a crash must never leave a
// headerless file), and once flushed - explicitly here, by the 5 s timer
// or Close in real life - a hard kill loses nothing.
func TestWritesAreBatchedThenDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.strike")
	s, err := New(path, 42, fakeClock(time.UnixMilli(1000000), 150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	// The header and opening events must already be on disk.
	if b, err := os.ReadFile(path); err != nil || len(b) < format.HeaderSize {
		t.Fatalf("fresh session file must hold at least the header: %d bytes, err=%v", len(b), err)
	}

	for _, r := range "hello" {
		if _, err := s.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	if got := countStrikes(t, path, 150); got != 0 {
		t.Fatalf("found %d strikes on disk before any flush, want 0 (writes are batched)", got)
	}

	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	// Do NOT close: simulate a crash after the flush.
	if got := countStrikes(t, path, 150); got != 5 {
		t.Fatalf("found %d strikes on disk after flush, want 5", got)
	}
	s.Close()
}

// TestBackgroundFlusherWrites pins the timer: with a tiny flush interval,
// typed strikes reach the disk without any explicit Flush or Close.
func TestBackgroundFlusherWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.strike")
	s, err := New(path, 42, fakeClock(time.UnixMilli(1000000), 150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Shrink this session's interval (a per-session field, so no other
	// session is affected) and restart the flusher on the new cadence.
	s.stopFlusher()
	s.flushEvery = 20 * time.Millisecond
	s.startFlusher()
	for _, r := range "hello" {
		if _, err := s.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if countStrikes(t, path, 150) == 5 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background flusher never wrote the strikes to disk")
}

func TestOpenResumesAndSeparatesSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume.strike")
	s, err := New(path, 42, fakeClock(time.UnixMilli(1000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	s.Strike('a')
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen much later; the gap must not appear as a keystroke delta.
	s2, err := Open(path, fakeClock(time.UnixMilli(9000000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	s2.Strike('b')
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}

	b, _ := os.ReadFile(path)
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	sessions := 0
	for _, e := range f.Events {
		if e.Op == format.OpSession {
			sessions++
		}
		if e.Op == format.OpStrike && e.DeltaMS > 10000 {
			t.Fatalf("a strike absorbed the between-sessions gap: %d ms", e.DeltaMS)
		}
	}
	if sessions != 2 {
		t.Fatalf("got %d session markers, want 2", sessions)
	}
	res, err := format.Verify(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstBad != -1 {
		t.Fatal("hash chain broken after resume")
	}
	// Both strikes visible.
	if s2.Doc.Pages[0].Strikes[0].Rune != 'a' || s2.Doc.Pages[0].Strikes[1].Rune != 'b' {
		t.Fatal("strikes lost across sessions")
	}
}

func TestOpenRepairsTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crash.strike")
	s, err := New(path, 42, fakeClock(time.UnixMilli(1000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	s.Strike('a')
	s.Strike('b')
	s.Close()

	// Chop mid-event to simulate a crash during append.
	b, _ := os.ReadFile(path)
	os.WriteFile(path, b[:len(b)-3], 0o644)

	s2, err := Open(path, fakeClock(time.UnixMilli(2000000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := os.Stat(path + ".crashed"); err != nil {
		t.Fatal("crash copy not kept")
	}
	if _, err := s2.Strike('c'); err != nil {
		t.Fatal(err)
	}
}

func TestImportTextSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "import.strike")
	s, err := ImportText(path, "typed by machine", 7, fakeClock(time.UnixMilli(1000000), time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Doc.Pages) != 1 || len(s.Doc.Pages[0].Strikes) != 14 {
		t.Fatalf("import folded wrong: pages=%d", len(s.Doc.Pages))
	}
	// Human can keep typing after the machine.
	if _, err := s.Strike('!'); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	f, _ := format.Decode(b)
	res, _ := format.Verify(f)
	if res.FirstBad != -1 {
		t.Fatal("hash chain broken")
	}
}

func TestNewRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.strike")
	os.WriteFile(path, []byte("precious"), 0o644)
	if _, err := New(path, 1, nil); err == nil {
		t.Fatal("New must never overwrite an existing file")
	}
}

// TestOpenRefusesForeignModelVersion pins that a session cannot open (and
// then append to, at the wrong model) a file from a future model version.
func TestOpenRefusesForeignModelVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future.strike")
	h := format.DefaultHeader(42, 0)
	h.ModelVersion = 2
	b := format.EncodeHeader(h)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, nil); err == nil {
		t.Fatal("opening a model-v2 file with a v1 build must fail")
	}
}
