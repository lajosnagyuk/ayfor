// Package importer types a plain text file into a STRIKE event stream.
// The cadence is deliberately robotic — every keystroke exactly the same
// interval — and the session is flagged origin=imported, so machine-typed
// documents are honest about their provenance and visibly inhuman on
// replay.
package importer

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/page"
)

// RobotIntervalMS is the uniform keystroke interval for imported text.
// 40 ms is far steadier than any human and replays quickly.
const RobotIntervalMS = 40

// maxImportPresize caps the eager event-slice allocation so importing a
// hostile multi-gigabyte file cannot force a huge up-front allocation from
// its byte length. Beyond the cap the slice grows as events are appended.
const maxImportPresize = 1 << 20

// Import converts text into events under the given header's settings.
// Words wrap at the right margin; long words break hard. Form feeds and
// page overflow feed new sheets. The caller supplies the wall-clock
// timestamp for the session event.
func Import(text string, h format.Header, wallUnixMS int64) []format.Event {
	// One event per rune plus occasional CR/NewSheet: len(text) is a good
	// capacity estimate that avoids ~log2(n) reallocations of a large-file
	// event slice (a paste of a whole manuscript is a real workflow),
	// bounded so a hostile byte length cannot force a huge allocation.
	capHint := min(len(text)+16, maxImportPresize)
	events := make([]format.Event, 0, capHint)
	events = append(events,
		format.Event{Op: format.OpSession, WallUnixMS: wallUnixMS, Origin: format.OriginImported},
		format.Event{Op: format.OpNewSheet},
	)
	// Fold state as we go so wrapping decisions match replay exactly.
	d := page.New(h)
	for _, e := range events {
		d.Apply(e)
	}

	emit := func(e format.Event) {
		e.DeltaMS = RobotIntervalMS
		events = append(events, e)
		res := d.Apply(e)
		if res.PageFull && e.Op == format.OpCR {
			// Ran out of paper: feed a new sheet.
			ns := format.Event{DeltaMS: RobotIntervalMS, Op: format.OpNewSheet}
			events = append(events, ns)
			d.Apply(ns)
		}
	}

	text = strings.TrimPrefix(text, "\ufeff") // strip a leading byte-order mark
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = norm.NFC.String(text) // compose accents so "e + combining acute" is one cell
	lines := strings.Split(text, "\n")
	for li, line := range lines {
		for _, word := range splitKeepingSpaces(line) {
			isSpace := word == " "
			runes := []rune(word)
			if !isSpace && d.Col+len(runes) > d.MaxCol() && len(runes) <= d.MaxCol() {
				// Word won't fit on this line but fits on a fresh
				// one: return first.
				emit(format.Event{Op: format.OpCR})
			}
			for _, r := range runes {
				if d.AtLock() {
					emit(format.Event{Op: format.OpCR})
					if isSpace || unicode.IsSpace(r) {
						break // don't carry a space to the new line
					}
				}
				if r == '\f' {
					emit(format.Event{Op: format.OpNewSheet})
					continue
				}
				if r == '\t' {
					// No tab stops in v1: a tab is typed as spaces
					// to the next multiple of 8.
					emit(format.Event{Op: format.OpSpace})
					for d.Col%8 != 0 && !d.AtLock() {
						emit(format.Event{Op: format.OpSpace})
					}
					continue
				}
				if unicode.IsControl(r) {
					// Drop stray control characters (NUL, VT, DEL, a lone
					// CR); \f and \t are handled above. Typing them would
					// print box glyphs and consume columns.
					continue
				}
				if unicode.IsSpace(r) {
					emit(format.Event{Op: format.OpSpace})
				} else {
					emit(format.Event{Op: format.OpStrike, Rune: r})
				}
			}
		}
		if li < len(lines)-1 {
			emit(format.Event{Op: format.OpCR})
		}
	}
	return events
}

// splitKeepingSpaces splits a line into words and single-space tokens so
// wrap decisions can be made per word.
func splitKeepingSpaces(s string) []string {
	var out []string
	var cur strings.Builder
	for _, r := range s {
		if r == ' ' {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			out = append(out, " ")
		} else {
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
