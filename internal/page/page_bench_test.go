package page

import (
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

// BenchmarkApplyStrike is the cost of folding one strike into Doc state:
// this runs on every keystroke via session.append, so it must stay flat
// regardless of how many strikes already sit on the page or how many
// pages exist before it - a per-keystroke cost that grows with document
// size would be exactly the "60 pages in, typing grinds to a halt" bug.
func BenchmarkApplyStrike(b *testing.B) {
	d := newDoc()
	e := format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: 'e'}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d.Apply(e)
		// Keep the carriage from locking or running off the page, which
		// would change what's being measured; a cheap sheet feed every
		// ~3600 strikes (one page) keeps the loop representative.
		if d.AtLock() {
			d.Apply(format.Event{Op: format.OpCR})
		}
		if d.YHalf >= d.MaxYHalf() {
			d.Apply(format.Event{Op: format.OpNewSheet})
		}
	}
}

// BenchmarkApplyBook folds a full 60-page document from scratch each
// iteration, so N x this cost is what a from-cold file Open() (Replay)
// pays. It exists to catch any accidental O(n^2) in Doc.Apply as page
// count grows - the benchmark's own ns/op should stay proportional to
// page count, not blow up super-linearly.
func BenchmarkApplyBook(b *testing.B) {
	events := bookEvents(60)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d := newDoc()
		for _, e := range events {
			d.Apply(e)
		}
	}
}

// bookEvents synthesizes a 60-page-equivalent event stream without going
// through the importer, keeping this package's benchmark independent of
// internal/importer.
func bookEvents(pages int) []format.Event {
	const colsPerLine = 64
	const linesPerPage = 57
	text := []rune("the quick brown fox jumps over the lazy dog while the machine hums along")
	events := make([]format.Event, 0, pages*colsPerLine*linesPerPage)
	ti := 0
	for range pages {
		events = append(events, format.Event{Op: format.OpNewSheet})
		for range linesPerPage {
			for range colsPerLine {
				r := text[ti%len(text)]
				ti++
				if r == ' ' {
					events = append(events, format.Event{DeltaMS: 90, Op: format.OpSpace})
				} else {
					events = append(events, format.Event{DeltaMS: 90, Op: format.OpStrike, Rune: r})
				}
			}
			events = append(events, format.Event{DeltaMS: 90, Op: format.OpCR})
		}
	}
	return events
}
