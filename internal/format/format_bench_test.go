package format

import (
	"io"
	"testing"
)

// bookEvents builds a realistic multi-page event stream: 60 pages worth
// of strikes (the scale the owner reasons in - "60 pages in"), interleaved
// with the space/CR/CHECK traffic a real session produces.
func bookEvents(pages int) []Event {
	const colsPerLine = 64
	const linesPerPage = 57
	text := []rune("the quick brown fox jumps over the lazy dog while the machine hums along")
	events := make([]Event, 0, pages*colsPerLine*linesPerPage)
	ti := 0
	for range pages {
		events = append(events, Event{Op: OpNewSheet})
		for range linesPerPage {
			for range colsPerLine {
				r := text[ti%len(text)]
				ti++
				if r == ' ' {
					events = append(events, Event{DeltaMS: 90, Op: OpSpace})
				} else {
					events = append(events, Event{DeltaMS: 90, Op: OpStrike, Rune: r})
				}
			}
			events = append(events, Event{DeltaMS: 90, Op: OpCR})
		}
	}
	return events
}

func BenchmarkEncodeEvent(b *testing.B) {
	e := Event{DeltaMS: 90, Op: OpStrike, Rune: 'e'}
	buf := make([]byte, 0, 16)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf, _ = EncodeEvent(buf[:0], e)
	}
}

// BenchmarkWriterAppend isolates the per-keystroke encode+hash cost from
// disk I/O by writing to io.Discard: this is the CPU floor under every
// strike regardless of storage speed.
func BenchmarkWriterAppend(b *testing.B) {
	w, err := NewWriter(io.Discard, sampleHeader())
	if err != nil {
		b.Fatal(err)
	}
	e := Event{Op: OpStrike, Rune: 'e'}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e.DeltaMS = uint64(90 + i%50)
		if err := w.Append(e); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeBook(b *testing.B) {
	events := bookEvents(60)
	buf := EncodeHeader(sampleHeader())
	for _, e := range events {
		var err error
		buf, err = EncodeEvent(buf, e)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(buf)))
	for i := 0; i < b.N; i++ {
		f, err := Decode(buf)
		if err != nil {
			b.Fatal(err)
		}
		if len(f.Events) != len(events) {
			b.Fatalf("decoded %d events, want %d", len(f.Events), len(events))
		}
	}
}

func BenchmarkVerifyBook(b *testing.B) {
	events := bookEvents(60)
	h := sampleHeader()
	buf := EncodeHeader(h)
	for _, e := range events {
		var err error
		buf, err = EncodeEvent(buf, e)
		if err != nil {
			b.Fatal(err)
		}
	}
	f, err := Decode(buf)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(buf)))
	for i := 0; i < b.N; i++ {
		if _, err := Verify(f); err != nil {
			b.Fatal(err)
		}
	}
}
