package format

import (
	"bytes"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/units"
)

func sampleHeader() Header {
	return DefaultHeader(0xDEADBEEFCAFE1234, 1767225600000)
}

func TestHeaderRoundTrip(t *testing.T) {
	h := sampleHeader()
	h.Margins = units.Margins{Left: 25.5, Right: 20, Top: 12.3, Bottom: 30}
	b := EncodeHeader(h)
	if len(b) != HeaderSize {
		t.Fatalf("header size = %d, want %d", len(b), HeaderSize)
	}
	got, err := DecodeHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, h)
	}
}

func TestBadMagic(t *testing.T) {
	b := EncodeHeader(sampleHeader())
	b[0] = 'X'
	if _, err := DecodeHeader(b); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func allEventKinds() []Event {
	return []Event{
		{DeltaMS: 0, Op: OpSession, WallUnixMS: 1767225600000, Origin: OriginHuman},
		{DeltaMS: 5, Op: OpNewSheet},
		{DeltaMS: 812, Op: OpStrike, Rune: 'H'},
		{DeltaMS: 143, Op: OpStrike, Rune: 'é'}, // multi-byte rune
		{DeltaMS: 90, Op: OpSpace},
		{DeltaMS: 200, Op: OpBack},
		{DeltaMS: 350, Op: OpCR},
		{DeltaMS: 10, Op: OpLF},
		{DeltaMS: 10, Op: OpHalfUp},
		{DeltaMS: 10, Op: OpHalfDown},
		{DeltaMS: 10, Op: OpPagePrev},
		{DeltaMS: 10, Op: OpPageNext},
		{DeltaMS: 10, Op: OpToss},
		{DeltaMS: 10, Op: OpSetPitch, Value: 12},
		{DeltaMS: 10, Op: OpSetLinespace, Value: 15},
		{DeltaMS: 10, Op: OpSetMargins, Margins: units.Margins{Left: 30, Right: 15, Top: 20, Bottom: 20}},
		{DeltaMS: 10, Op: OpNewRibbon},
		{DeltaMS: 10, Op: OpSetTouch, Value: 85},
		{DeltaMS: 10, Op: OpSetDisposition, Value: 180},
		{DeltaMS: 10, Op: OpSetSobriety, Value: 185},
		{DeltaMS: 10, Op: OpSetCondition, Value: 60},
		{DeltaMS: 100000, Op: OpStrike, Rune: 'z'}, // large delta varint
	}
}

func TestEventRoundTrip(t *testing.T) {
	events := allEventKinds()
	buf := EncodeHeader(sampleHeader())
	for _, e := range events {
		var err error
		buf, err = EncodeEvent(buf, e)
		if err != nil {
			t.Fatal(err)
		}
	}
	f, err := Decode(buf)
	if err != nil {
		t.Fatal(err)
	}
	if f.Truncated {
		t.Fatal("unexpected truncation")
	}
	if len(f.Events) != len(events) {
		t.Fatalf("decoded %d events, want %d", len(f.Events), len(events))
	}
	for i := range events {
		if f.Events[i] != events[i] {
			t.Errorf("event %d:\n got %+v\nwant %+v", i, f.Events[i], events[i])
		}
	}
}

func TestTruncationTolerance(t *testing.T) {
	buf := EncodeHeader(sampleHeader())
	buf, _ = EncodeEvent(buf, Event{DeltaMS: 1, Op: OpStrike, Rune: 'a'})
	buf, _ = EncodeEvent(buf, Event{DeltaMS: 1, Op: OpStrike, Rune: 'b'})
	full := len(buf)
	buf, _ = EncodeEvent(buf, Event{DeltaMS: 1, Op: OpSession, WallUnixMS: 123, Origin: 0})
	// Chop the final event at every possible partial length.
	for cut := full + 1; cut < len(buf); cut++ {
		f, err := Decode(buf[:cut])
		if err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
		if !f.Truncated {
			t.Fatalf("cut %d: expected Truncated", cut)
		}
		if len(f.Events) != 2 {
			t.Fatalf("cut %d: got %d events, want 2", cut, len(f.Events))
		}
	}
}

func TestWriterHashChain(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, sampleHeader())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Event{Op: OpSession, WallUnixMS: 1, Origin: OriginHuman}); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Event{Op: OpNewSheet}); err != nil {
		t.Fatal(err)
	}
	for i := range 1200 {
		if err := w.Append(Event{DeltaMS: uint64(50 + i%400), Op: OpStrike, Rune: rune('a' + i%26)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Check(); err != nil {
		t.Fatal(err)
	}
	f, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Verify(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checks < 3 {
		t.Fatalf("expected >=3 CHECK events (interval emission), got %d", res.Checks)
	}
	if res.FirstBad != -1 {
		t.Fatalf("hash chain failed at event %d", res.FirstBad)
	}

	// Tamper with one strike and confirm the chain catches it.
	tampered := append([]byte(nil), buf.Bytes()...)
	// Find a strike byte well inside the stream and flip its rune.
	tampered[HeaderSize+40] ^= 0x01
	f2, err := Decode(tampered)
	if err != nil {
		// Tampering may make the stream undecodable; that also counts
		// as detection.
		return
	}
	res2, err := Verify(f2)
	if err != nil {
		return
	}
	if res2.FirstBad == -1 {
		t.Fatal("tampering was not detected by hash chain")
	}
}

func TestResumeWriter(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, sampleHeader())
	w.Append(Event{Op: OpSession, WallUnixMS: 1, Origin: OriginHuman})
	w.Append(Event{Op: OpNewSheet})
	w.Append(Event{DeltaMS: 100, Op: OpStrike, Rune: 'a'})
	w.Check()

	existing := append([]byte(nil), buf.Bytes()...)
	w2, err := ResumeWriter(&buf, existing)
	if err != nil {
		t.Fatal(err)
	}
	w2.Append(Event{DeltaMS: 100, Op: OpStrike, Rune: 'b'})
	w2.Check()

	f, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Verify(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.FirstBad != -1 {
		t.Fatalf("resumed hash chain failed at event %d", res.FirstBad)
	}
}

func TestWriterRefusesDecoderLimitsBeforePublishingEvent(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, DefaultHeader(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	w.events = MaxEvents - 1 // a payload must still reserve its final CHECK
	before := buf.Len()
	if err := w.Append(Event{Op: OpStrike, Rune: 'x'}); err == nil {
		t.Fatal("writer accepted payload with no room for final checkpoint")
	}
	if buf.Len() != before {
		t.Fatal("writer published bytes before rejecting event limit")
	}

	w.events = 0
	w.bytes = MaxFileBytes - 1
	before = buf.Len()
	if err := w.Append(Event{Op: OpStrike, Rune: 'x'}); err == nil {
		t.Fatal("writer accepted payload beyond byte ceiling")
	}
	if buf.Len() != before {
		t.Fatal("writer published bytes before rejecting byte limit")
	}
}

func TestWriterFinalCheckpointIsIdempotentAtEventCeiling(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, DefaultHeader(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	w.events = MaxEvents - 1
	w.dirty = true
	if err := w.Check(); err != nil {
		t.Fatal(err)
	}
	afterFinal := buf.Len()
	if err := w.Check(); err != nil {
		t.Fatalf("second finalization should be a no-op: %v", err)
	}
	if buf.Len() != afterFinal || w.events != MaxEvents {
		t.Fatal("idempotent checkpoint appended another event")
	}
}

func TestWriterAllowsPayloadAndPeriodicCheckpointInFinalTwoSlots(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewWriter(&buf, DefaultHeader(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	w.events = MaxEvents - 2
	w.sinceCheck = CheckInterval - 1
	// Give the byte accounting ample room; this test isolates event slots.
	if err := w.Append(Event{Op: OpStrike, Rune: 'x'}); err != nil {
		t.Fatalf("payload plus its periodic checkpoint should fit: %v", err)
	}
	if w.events != MaxEvents || w.dirty {
		t.Fatalf("events=%d dirty=%v, want a clean final checkpoint at %d", w.events, w.dirty, MaxEvents)
	}
	if err := w.Check(); err != nil {
		t.Fatalf("final Check after periodic boundary should be idempotent: %v", err)
	}
}

func TestBytesPerKeystroke(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, sampleHeader())
	w.Append(Event{Op: OpSession, WallUnixMS: 1, Origin: OriginHuman})
	w.Append(Event{Op: OpNewSheet})
	n := 10000
	for i := range n {
		w.Append(Event{DeltaMS: uint64(80 + i%300), Op: OpStrike, Rune: rune('a' + i%26)})
	}
	perKey := float64(buf.Len()) / float64(n)
	if perKey > 6 {
		t.Fatalf("%.2f bytes per keystroke, want <= 6", perKey)
	}
	t.Logf("%.2f bytes per keystroke", perKey)
}
