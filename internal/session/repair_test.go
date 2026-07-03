package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

// writtenText returns the runes struck in a decoded file, in order.
func writtenText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if f.Truncated {
		t.Fatalf("%s decoded as truncated", path)
	}
	if res, err := format.Verify(f); err != nil || res.FirstBad != -1 {
		t.Fatalf("%s fails verify: err=%v firstBad=%d", path, err, res.FirstBad)
	}
	var out []rune
	for _, e := range f.Events {
		if e.Op == format.OpStrike {
			out = append(out, e.Rune)
		}
	}
	return string(out)
}

// seedFile writes "abc" into a fresh session and closes it, returning the path.
func seedFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.strike")
	s, err := New(path, 42, fakeClock(time.UnixMilli(1_000_000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range "abc" {
		if _, err := s.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOpenRecoversFromMidStreamCorruption pins that a hard decode error (not
// just a truncated tail) recovers the readable prefix, backs up the original,
// and reopens to a verifying, appendable file.
func TestOpenRecoversFromMidStreamCorruption(t *testing.T) {
	path := seedFile(t)

	// Append an overflowing varint: a corrupt event, not a clean short tail.
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	garbage := make([]byte, 11)
	for i := range garbage {
		garbage[i] = 0xFF
	}
	if _, err := fh.Write(garbage); err != nil {
		t.Fatal(err)
	}
	fh.Close()

	s, err := Open(path, fakeClock(time.UnixMilli(2_000_000), 100*time.Millisecond))
	if err != nil {
		t.Fatalf("corrupt file should recover, got %v", err)
	}
	// Typing continues on the repaired file.
	if _, err := s.Strike('d'); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if got := writtenText(t, path); got != "abcd" {
		t.Fatalf("recovered text = %q, want abcd", got)
	}
	if _, err := os.Stat(path + ".crashed"); err != nil {
		t.Fatalf(".crashed backup missing: %v", err)
	}
}

// TestOpenRepairPreservesPrefixAndAppends strengthens the truncation-repair
// coverage: after a kill mid-event, the repaired file verifies, keeps every
// pre-crash strike, and accepts new appends on a sound hash chain.
func TestOpenRepairPreservesPrefixAndAppends(t *testing.T) {
	path := seedFile(t)

	// Lop off the final byte: a genuine truncated tail.
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b[:len(b)-1], 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path, fakeClock(time.UnixMilli(2_000_000), 100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Strike('z'); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The final strike may have been the truncated one, so the prefix is
	// "ab" or "abc"; either way it must be a clean prefix of the original
	// with the new append, and the file must verify (checked in writtenText).
	got := writtenText(t, path)
	if got != "abz" && got != "abcz" {
		t.Fatalf("repaired text = %q, want abz or abcz", got)
	}
}
