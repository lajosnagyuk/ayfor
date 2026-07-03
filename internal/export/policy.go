// Package export converts a folded document into MD, DOCX and PDF.
//
// Lossiness ladder (docs/DESIGN.md 1.5): PDF is exact (per-strike
// placement, tilt, ink); DOCX keeps structure and styling; MD keeps text
// with double-strike as bold and x-ed out cells as strikethrough.
package export

import (
	"sort"

	"github.com/lajosnagyuk/ayfor/internal/page"
)

// CellStyle is the structural meaning of a cell's overstrikes.
type CellStyle uint8

const (
	Plain  CellStyle = iota
	Bold             // same glyph struck two or more times
	Struck           // a glyph overstruck with x, X or -
)

// StyledCell is one exportable character.
type StyledCell struct {
	Rune  rune
	Style CellStyle
}

// Line is one text row: styled cells indexed by column, sparse columns
// are spaces.
type Line struct {
	YHalf int
	Cells []StyledCell // dense from column 0, trailing spaces trimmed
}

func isCrossOut(r rune) bool { return r == 'x' || r == 'X' || r == '-' }

// blankLinesBetween converts a vertical gap between two occupied rows
// into a count of blank text lines, using the document's line spacing as
// the unit. prevY of -1 means "first row on the page" (no gap).
func blankLinesBetween(prevY, y int, d *page.Doc) int {
	if prevY < 0 {
		return 0
	}
	adv := max(
		// half-lines per return
		int(d.LineSpacing)/5, 1)
	n := (y-prevY)/adv - 1
	if n < 0 {
		return 0
	}
	return n
}

// classify reduces a cell's strike history to a styled character.
func classify(runes []rune) StyledCell {
	last := runes[len(runes)-1]
	if len(runes) == 1 {
		return StyledCell{Rune: last, Style: Plain}
	}
	// Same glyph repeated -> typewriter bold.
	allSame := true
	for _, r := range runes {
		if r != runes[0] {
			allSame = false
			break
		}
	}
	if allSame {
		return StyledCell{Rune: last, Style: Bold}
	}
	// A cross-out glyph over something else -> struck text; keep the
	// first non-cross glyph as the visible character.
	hasCross, under := false, rune(0)
	for _, r := range runes {
		if isCrossOut(r) {
			hasCross = true
		} else if under == 0 {
			under = r
		}
	}
	if hasCross && under != 0 {
		return StyledCell{Rune: under, Style: Struck}
	}
	// Anything else: the last strike is what the eye reads.
	return StyledCell{Rune: last, Style: Plain}
}

// Lines flattens a page into export lines, top to bottom. Cells that
// share a column but sit on adjacent half-lines (platen tricks) are
// treated as separate lines — exactly what they are.
func Lines(p *page.Page) []Line {
	byRow := make(map[int]map[int][]rune)
	for key, cell := range p.Cells {
		row := byRow[key.YHalf]
		if row == nil {
			row = make(map[int][]rune)
			byRow[key.YHalf] = row
		}
		row[key.Col] = cell.Runes
	}
	rows := make([]int, 0, len(byRow))
	for y := range byRow {
		rows = append(rows, y)
	}
	sort.Ints(rows)

	var out []Line
	for _, y := range rows {
		row := byRow[y]
		maxCol := 0
		for c := range row {
			if c > maxCol {
				maxCol = c
			}
		}
		cells := make([]StyledCell, maxCol+1)
		for i := range cells {
			cells[i] = StyledCell{Rune: ' ', Style: Plain}
		}
		for c, runes := range row {
			cells[c] = classify(runes)
		}
		out = append(out, Line{YHalf: y, Cells: cells})
	}
	return out
}
