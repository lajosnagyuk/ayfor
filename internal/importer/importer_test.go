package importer

import (
	"errors"
	"strings"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/page"
)

func header() format.Header { return format.DefaultHeader(42, 0) }

// textOf reads back the visible text of a doc, last-glyph-wins per cell.
func textOf(d *page.Doc) string {
	var sb strings.Builder
	for _, p := range d.LivePages() {
		maxY, maxC := 0, 0
		for k := range p.Cells {
			if k.YHalf > maxY {
				maxY = k.YHalf
			}
			if k.Col > maxC {
				maxC = k.Col
			}
		}
		for y := 0; y <= maxY; y++ {
			var line strings.Builder
			for c := 0; c <= maxC; c++ {
				if cell, ok := p.Cells[page.CellKey{YHalf: y, Col: c}]; ok && len(cell.Runes) > 0 {
					for line.Len() < c {
						line.WriteByte(' ')
					}
					line.WriteRune(cell.Runes[len(cell.Runes)-1])
				}
			}
			if line.Len() > 0 {
				sb.WriteString(strings.TrimRight(line.String(), " "))
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

func TestSimpleImport(t *testing.T) {
	events := Import("hello world", header(), 1234)
	if events[0].Op != format.OpSession || events[0].Origin != format.OriginImported {
		t.Fatal("import must start with an imported-origin session")
	}
	d := page.New(header())
	for _, e := range events {
		d.Apply(e)
	}
	got := textOf(d)
	if got != "hello world\n" {
		t.Fatalf("round trip text = %q", got)
	}
}

func TestRoboticCadence(t *testing.T) {
	events := Import("abc def", header(), 1)
	// events[0] and [1] are the session and sheet-feed bootstrap; the
	// typing itself must be perfectly uniform.
	for i, e := range events[2:] {
		if e.DeltaMS != RobotIntervalMS {
			t.Fatalf("event %d delta = %d, want uniform %d", i+2, e.DeltaMS, RobotIntervalMS)
		}
	}
}

func TestWordWrap(t *testing.T) {
	h := header()
	// 64 usable Pica columns. A line of five-letter words + spaces will
	// need wrapping; no word may be split.
	text := strings.TrimSpace(strings.Repeat("alpha bravo charl ", 8))
	events := Import(text, h, 1)
	d := page.New(h)
	for _, e := range events {
		d.Apply(e)
	}
	got := textOf(d)
	for line := range strings.SplitSeq(strings.TrimSpace(got), "\n") {
		for w := range strings.FieldsSeq(line) {
			if w != "alpha" && w != "bravo" && w != "charl" {
				t.Fatalf("word split across lines: %q in line %q", w, line)
			}
		}
	}
	if strings.ReplaceAll(strings.ReplaceAll(got, "\n", " "), "  ", " ") == "" {
		t.Fatal("no output")
	}
}

func TestLongWordHardBreaks(t *testing.T) {
	h := header()
	long := strings.Repeat("x", 100) // longer than any line
	events := Import(long, h, 1)
	d := page.New(h)
	for _, e := range events {
		d.Apply(e)
	}
	total := 0
	for _, p := range d.LivePages() {
		total += len(p.Strikes)
	}
	if total != 100 {
		t.Fatalf("hard break lost strikes: %d of 100", total)
	}
}

func TestPageOverflowFeedsSheet(t *testing.T) {
	h := header()
	// Enough lines to overflow one A4 page (about 58 single-spaced).
	text := strings.TrimSpace(strings.Repeat("line\n", 80))
	events := Import(text, h, 1)
	d := page.New(h)
	for _, e := range events {
		d.Apply(e)
	}
	if len(d.Pages) < 2 {
		t.Fatalf("80 lines must span multiple pages, got %d", len(d.Pages))
	}
	got := strings.Count(textOf(d), "line")
	if got != 80 {
		t.Fatalf("lost lines on page feed: %d of 80", got)
	}
}

func TestReplayDeterminism(t *testing.T) {
	a := Import("The quick brown fox.", header(), 99)
	b := Import("The quick brown fox.", header(), 99)
	if len(a) != len(b) {
		t.Fatal("import must be deterministic")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs", i)
		}
	}
}

func TestImportLimitedRejectsExpansionWithoutReturningPartialDocument(t *testing.T) {
	h := format.DefaultHeader(1, 1)
	events, err := ImportLimited(strings.Repeat("\t", 100), h, 1, 32)
	if !errors.Is(err, ErrEventLimit) {
		t.Fatalf("error = %v, want ErrEventLimit", err)
	}
	if events != nil {
		t.Fatalf("returned %d partial events; want nil", len(events))
	}
}

func TestImportLimitedAcceptsInputWithinBudget(t *testing.T) {
	h := format.DefaultHeader(1, 1)
	events, err := ImportLimited("hello", h, 1, 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 7 { // SESSION, NEW_SHEET, five strikes
		t.Fatalf("event count = %d, want 7", len(events))
	}
}
