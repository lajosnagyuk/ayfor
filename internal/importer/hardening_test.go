package importer

import (
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
)

func countStrikes(events []format.Event) int {
	n := 0
	for _, e := range events {
		if e.Op == format.OpStrike {
			n++
		}
	}
	return n
}

// TestBOMStripped pins that a leading byte-order mark is not typed as a
// zero-width glyph in the first cell.
func TestBOMStripped(t *testing.T) {
	h := format.DefaultHeader(1, 0)
	withBOM := countStrikes(Import("\ufeffhi", h, 0))
	plain := countStrikes(Import("hi", h, 0))
	if withBOM != plain {
		t.Fatalf("BOM added strikes: %d vs %d", withBOM, plain)
	}
}

// TestControlCharsDropped pins that stray control characters do not become
// struck glyphs.
func TestControlCharsDropped(t *testing.T) {
	h := format.DefaultHeader(1, 0)
	withCtrl := countStrikes(Import("a\x00b\x07c\x1bd\x7f", h, 0))
	plain := countStrikes(Import("abcd", h, 0))
	if withCtrl != plain {
		t.Fatalf("control chars struck: %d vs %d (want equal)", withCtrl, plain)
	}
}

// TestNFCComposesAccents pins that decomposed input (base letter + combining
// mark) becomes a single composed cell rather than two.
func TestNFCComposesAccents(t *testing.T) {
	h := format.DefaultHeader(1, 0)
	nfd := countStrikes(Import("cafe\u0301", h, 0)) // e + combining acute
	nfc := countStrikes(Import("caf\u00e9", h, 0))  // precomposed e-acute
	if nfd != nfc {
		t.Fatalf("NFD not composed: %d strikes vs %d", nfd, nfc)
	}
	if nfc != 4 {
		t.Fatalf("expected 4 strikes, got %d", nfc)
	}
}
