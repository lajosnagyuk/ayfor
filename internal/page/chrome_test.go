package page

import (
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

// TestWordCounts pins per-page and whole-document word counts, and that a
// tossed sheet is excluded from the document total.
func TestWordCounts(t *testing.T) {
	d := newDoc()
	typeString(d, "hello world") // 2 words on page 0
	d.Apply(format.Event{Op: format.OpNewSheet})
	typeString(d, "one two three") // 3 words on page 1

	pg, doc := d.WordCounts()
	if pg != 3 {
		t.Errorf("current page words = %d, want 3", pg)
	}
	if doc != 5 {
		t.Errorf("document words = %d, want 5", doc)
	}

	// X-ing out a word does not delete it, so it still counts: overstrike an
	// x on the first cell of page 1's "one".
	d.Apply(format.Event{Op: format.OpNewSheet}) // page 2 to keep page 1 stable
	d.Current = 1
	d.Col, d.YHalf = 0, 0
	d.Apply(format.Event{Op: format.OpStrike, Rune: 'x'})
	if _, doc = d.WordCounts(); doc != 5 {
		t.Errorf("x-out changed the count: doc = %d, want 5", doc)
	}

	// Toss page 1; the document total drops by its 3 words.
	d.Current = 1
	d.Apply(format.Event{Op: format.OpToss})
	if _, doc = d.WordCounts(); doc != 2 {
		t.Errorf("document words after toss = %d, want 2", doc)
	}
}

// TestWordCountCacheInvalidates pins the strike-count cache in WordCount:
// counting, typing more, and counting again must reflect the new words -
// a cache that failed to invalidate would keep returning the old total.
func TestWordCountCacheInvalidates(t *testing.T) {
	d := newDoc()
	typeString(d, "hello")
	if _, doc := d.WordCounts(); doc != 1 {
		t.Fatalf("doc words = %d, want 1", doc)
	}
	// Repeat count with no typing: the cached path must agree.
	if _, doc := d.WordCounts(); doc != 1 {
		t.Fatalf("repeated count = %d, want 1", doc)
	}
	typeString(d, " world and more")
	if _, doc := d.WordCounts(); doc != 4 {
		t.Fatalf("doc words after typing = %d, want 4 (stale word-count cache?)", doc)
	}
}

// TestChromeStrikeIsPureAndDeterministic pins that deriving chrome appearance
// is deterministic and never mutates document state.
func TestChromeStrikeIsPureAndDeterministic(t *testing.T) {
	d := newDoc()
	typeString(d, "some words here")

	before := d.Pages[d.Current].WordCount()
	pages, current, col, yhalf := len(d.Pages), d.Current, d.Col, d.YHalf

	a := d.ChromeStrike('3', -1, 40)
	b := d.ChromeStrike('3', -1, 40)
	if a != b {
		t.Fatal("ChromeStrike is not deterministic")
	}
	for i := range 100 {
		d.ChromeStrike(rune('0'+i%10), -1, i)
	}
	if len(d.Pages) != pages || d.Current != current || d.Col != col || d.YHalf != yhalf {
		t.Fatal("ChromeStrike mutated carriage/page state")
	}
	if got := d.Pages[d.Current].WordCount(); got != before {
		t.Fatalf("ChromeStrike changed the word count: %d -> %d", before, got)
	}
}
