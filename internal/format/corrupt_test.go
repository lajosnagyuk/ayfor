package format

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// TestTamperedRuneFailsVerify pins that a byte flip that stays decodable (a
// changed rune) is caught by the hash chain - unconditionally, not only when
// the edit happens to break decoding.
func TestTamperedRuneFailsVerify(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, sampleHeader())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Event{Op: OpStrike, Rune: 'a'}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Event{Op: OpStrike, Rune: 'b'}); err != nil {
		t.Fatal(err)
	}
	if err := w.Check(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()

	// Clean file verifies.
	f, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := Verify(f); res.FirstBad != -1 {
		t.Fatalf("clean file failed verify at %d", res.FirstBad)
	}

	// Flip the first strike's rune byte: header(40) + delta(0x00) + op(0x01),
	// so offset 42 is the rune of 'a'. It stays a valid, decodable rune.
	b[42] = 'X'
	f2, err := Decode(b)
	if err != nil {
		t.Fatalf("tampered file should still decode, got %v", err)
	}
	if f2.Events[0].Rune != 'X' {
		t.Fatalf("expected the tamper to change the rune, got %q", f2.Events[0].Rune)
	}
	res, err := Verify(f2)
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstBad == -1 {
		t.Fatal("hash chain did not detect a decodable tamper")
	}
}

// TestForgedCheckFailsVerify pins that editing a CHECK's stored hash to hide
// upstream tampering is itself detected.
func TestForgedCheckFailsVerify(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, sampleHeader())
	w.Append(Event{Op: OpStrike, Rune: 'a'})
	w.Check()
	b := buf.Bytes()
	// The CHECK is the final event; its 8-byte hash occupies the last 8
	// bytes. Flip one so it no longer matches the running hash.
	b[len(b)-1] ^= 0xFF
	f, err := Decode(b)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res, _ := Verify(f); res.FirstBad == -1 {
		t.Fatal("forged CHECK not detected")
	}
}

// buildStream returns a header plus the given raw event bytes.
func buildStream(events []byte) []byte {
	b := EncodeHeader(sampleHeader())
	return append(b, events...)
}

// oneStrike encodes a delta=0 strike of r as wire bytes.
func oneStrike(r rune) []byte {
	var out []byte
	out = putUvarint(out, 0) // delta
	out = append(out, OpStrike)
	out = putUvarint(out, uint64(r))
	return out
}

// overflowVarint returns bytes that make binary.Uvarint report overflow
// (n < 0): more than MaxVarintLen64 continuation bytes. (Exactly
// MaxVarintLen64 continuation bytes reports n == 0 - buffer too short -
// which is genuine truncation, a different case.)
func overflowVarint() []byte {
	b := make([]byte, binary.MaxVarintLen64+1)
	for i := range b {
		b[i] = 0xFF
	}
	return b
}

// TestOverflowDeltaIsCorruptNotTruncated pins that an overflowing delta
// varint mid-stream is a hard corruption error, not misreported as a
// truncated tail (which the session repair would silently amputate).
func TestOverflowDeltaIsCorruptNotTruncated(t *testing.T) {
	// A valid strike, then an overflowing delta varint on the next event.
	raw := oneStrike('a')
	f, err := Decode(buildStream(append(raw, overflowVarint()...)))
	if err == nil {
		t.Fatalf("expected a hard error, got nil (Truncated=%v)", f.Truncated)
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	if errors.Is(err, ErrTruncated) {
		t.Fatal("corruption was misclassified as truncation")
	}
}

// TestOverflowRuneIsCorrupt pins the rune-varint arm of the same fix.
func TestOverflowRuneIsCorrupt(t *testing.T) {
	var raw []byte
	raw = putUvarint(raw, 0) // delta
	raw = append(raw, OpStrike)
	raw = append(raw, overflowVarint()...) // overflowing rune varint
	_, err := Decode(buildStream(raw))
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

// TestOutOfRangeRuneRejected pins that a rune varint above unicode.MaxRune
// or in the surrogate range is corruption, not a silently accepted garbage
// rune.
func TestOutOfRangeRuneRejected(t *testing.T) {
	for _, r := range []uint64{0x110000, 0xD800, 0xDFFF, 1 << 40} {
		var raw []byte
		raw = putUvarint(raw, 0)
		raw = append(raw, OpStrike)
		raw = putUvarint(raw, r)
		_, err := Decode(buildStream(raw))
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("rune %#x: want ErrCorrupt, got %v", r, err)
		}
	}
}

// TestValidRunesStillDecode guards against a false reject: the boundary
// valid runes must still round-trip.
func TestValidRunesStillDecode(t *testing.T) {
	for _, r := range []rune{'a', 0, 0xD7FF, 0xE000, 0x10FFFF} {
		f, err := Decode(buildStream(oneStrike(r)))
		if err != nil {
			t.Fatalf("rune %#x: unexpected error %v", r, err)
		}
		if got := f.Events[len(f.Events)-1].Rune; got != r {
			t.Fatalf("rune %#x round-tripped to %#x", r, got)
		}
	}
}

// TestGenuineTruncationStillTolerated guards the other side of the split: a
// real truncated tail (the buffer simply runs out) stays tolerated.
func TestGenuineTruncationStillTolerated(t *testing.T) {
	full := buildStream(oneStrike('a'))
	// Chop the final byte of the rune varint: the buffer genuinely ran out.
	f, err := Decode(full[:len(full)-1])
	if err != nil {
		t.Fatalf("truncation should be tolerated, got %v", err)
	}
	if !f.Truncated {
		t.Fatal("expected Truncated=true on a genuine short tail")
	}
}

// TestInvalidHeaderRejected pins that a header carrying a degenerate pitch,
// line spacing, paper, or font is rejected as corruption rather than decoded
// into a document with +Inf slot width or a non-advancing carriage.
func TestInvalidHeaderRejected(t *testing.T) {
	corrupt := func(mutate func(b []byte)) error {
		b := EncodeHeader(sampleHeader())
		mutate(b)
		_, err := DecodeHeader(b)
		return err
	}
	cases := map[string]func(b []byte){
		"pitch=0":     func(b []byte) { b[25] = 0 },
		"pitch=99":    func(b []byte) { b[25] = 99 },
		"linespace=0": func(b []byte) { b[26] = 0 },
		"paper=2":     func(b []byte) { b[24] = 2 },
		"font=0":      func(b []byte) { b[27] = 0 },
	}
	for name, mutate := range cases {
		if err := corrupt(mutate); !errors.Is(err, ErrCorrupt) {
			t.Errorf("%s: want ErrCorrupt, got %v", name, err)
		}
	}
	// A default header stays valid.
	if _, err := DecodeHeader(EncodeHeader(sampleHeader())); err != nil {
		t.Errorf("default header rejected: %v", err)
	}
}

// TestPresizeHintCapped pins that the eager allocation hint is bounded, so a
// hostile byte length cannot force a huge up-front allocation.
func TestPresizeHintCapped(t *testing.T) {
	if got := presizeHint(20); got != 10 {
		t.Fatalf("small file: want 10, got %d", got)
	}
	if got := presizeHint(1 << 40); got != maxDecodePresize {
		t.Fatalf("hostile length: want cap %d, got %d", maxDecodePresize, got)
	}
	if got := presizeHint(0); got != 0 {
		t.Fatalf("empty: want 0, got %d", got)
	}
}
