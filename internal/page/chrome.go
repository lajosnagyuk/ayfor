package page

import (
	"sort"
	"unicode"

	"github.com/lajosnagyuk/ayfor/internal/machine"
)

// This file exposes read-only hooks for display-only chrome (page numbers,
// word counts). None of it mutates document state or is ever written to the
// file; appearance stays derived.

// WordCount counts the words on the sheet. A word is a maximal run of
// consecutive occupied columns on one line; the spaces between words leave
// gaps in the column indices. X-ed-out words still count - the machine
// cannot delete, so neither does the counter.
//
// The scan is cached against the page's strike count (append-only, and
// cells only change when a strike lands), so a document-wide total
// (WordCounts, recomputed on a timer while the comfort is on) rescans
// only pages actually typed on since the last call - not the whole
// manuscript on every tick. A fresh page cold-hits the zero-value cache
// correctly: zero strikes, zero words.
func (p *Page) WordCount() int {
	if len(p.Strikes) == p.wcStrikes {
		return p.wcCount
	}
	rows := map[int][]int{}
	for k, c := range p.Cells {
		if cellHasInk(c) {
			rows[k.YHalf] = append(rows[k.YHalf], k.Col)
		}
	}
	words := 0
	for _, cols := range rows {
		sort.Ints(cols)
		for i, col := range cols {
			if i == 0 || col != cols[i-1]+1 {
				words++ // a gap (a space) precedes this run: new word
			}
		}
	}
	p.wcStrikes, p.wcCount = len(p.Strikes), words
	return words
}

// cellHasInk reports whether a cell holds any non-space rune.
func cellHasInk(c *Cell) bool {
	for _, r := range c.Runes {
		if !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// WordCounts returns the word count of the currently shown page and of the
// whole document (live pages only; binned sheets are excluded, matching the
// exporters).
func (d *Doc) WordCounts() (page, doc int) {
	for _, p := range d.LivePages() {
		doc += p.WordCount()
	}
	if d.Current >= 0 && d.Current < len(d.Pages) {
		page = d.Pages[d.Current].WordCount()
	}
	return page, doc
}

// The hand chrome is always typed with: light and even, on a settled ribbon
// well past its wet-fresh boost, then trimmed lighter still. Owner-facing.
const (
	chromeTouch    = 0.85 // light hand
	chromeRibbon   = 4000 // strikes in: a settled ribbon, no wet-fresh boost
	chromeInkScale = 0.78 // final trim so chrome reads light, not bold
)

// ChromeStrike derives the typed look of one display-only glyph (page number,
// word count). Chrome is meta-text, not the manuscript, so it is always a
// light, even hand on a settled, factory-clean machine - it does NOT inherit
// the document's touch, mood, sobriety, wear or ribbon, so page numbers and
// word counts stay light and legible even when the writing is furious or
// drunk. It is pure: it reads document state and appends nothing to any file.
// DeltaMS is fixed so the look does not shimmer between recalculations, and
// row is passed by the caller (use a negative row for chrome so its hash never
// collides with a real strike's row).
func (d *Doc) ChromeStrike(glyph rune, row, col int) machine.Strike {
	st := d.mach.StrikeFor(machine.Context{
		Glyph:         glyph,
		Prev:          0,
		DeltaMS:       300,
		Page:          d.Current,
		Row:           row,
		Col:           col,
		NthOnCell:     0,
		RibbonStrikes: chromeRibbon,
		Touch:         chromeTouch,
		Disposition:   1.0,
		Sobriety:      1.0,
		Condition:     1.0,
	})
	st.Ink *= chromeInkScale
	return st
}
