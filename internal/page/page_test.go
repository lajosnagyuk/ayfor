package page

import (
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

func newDoc() *Doc {
	d := New(format.DefaultHeader(42, 0))
	d.Apply(format.Event{Op: format.OpSession, WallUnixMS: 1, Origin: format.OriginHuman})
	d.Apply(format.Event{Op: format.OpNewSheet})
	return d
}

func typeString(d *Doc, s string) []Result {
	var out []Result
	for _, r := range s {
		if r == ' ' {
			out = append(out, d.Apply(format.Event{DeltaMS: 100, Op: format.OpSpace}))
		} else if r == '\n' {
			out = append(out, d.Apply(format.Event{DeltaMS: 100, Op: format.OpCR}))
		} else {
			out = append(out, d.Apply(format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: r}))
		}
	}
	return out
}

func TestBasicTyping(t *testing.T) {
	d := newDoc()
	typeString(d, "hello world")
	p := d.Pages[0]
	if len(p.Strikes) != 10 {
		t.Fatalf("got %d strikes, want 10", len(p.Strikes))
	}
	if d.Col != 11 {
		t.Fatalf("carriage at col %d, want 11", d.Col)
	}
	// First strike sits at the left margin slot centre.
	first := p.Strikes[0]
	wantX := d.Margins.Left + 0.5*d.Pitch.SlotMM()
	if first.XMM != wantX {
		t.Fatalf("first strike x = %f, want %f", first.XMM, wantX)
	}
}

func TestBellAndLock(t *testing.T) {
	d := newDoc()
	max := d.MaxCol()
	bellAt := -1
	for i := 0; i <= max+10; i++ {
		res := d.Apply(format.Event{DeltaMS: 80, Op: format.OpStrike, Rune: 'a'})
		if res.Bell && bellAt == -1 {
			bellAt = i
		}
		if i >= max && res.Applied {
			t.Fatalf("strike %d applied while carriage should be locked (max %d)", i, max)
		}
	}
	if bellAt == -1 {
		t.Fatal("bell never rang")
	}
	// The strike at index bellAt left the carriage at bellAt+1; from
	// there exactly BellSlots more strikes fit before the lock.
	if got := max - (bellAt + 1); got != units.BellSlots {
		t.Fatalf("bell left room for %d strikes before lock, want %d", got, units.BellSlots)
	}
	if !d.AtLock() {
		t.Fatal("carriage should be locked")
	}
	// Return releases.
	d.Apply(format.Event{DeltaMS: 100, Op: format.OpCR})
	if d.AtLock() || d.Col != 0 {
		t.Fatal("CR must release the lock and return the carriage")
	}
	res := d.Apply(format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: 'b'})
	if !res.Applied {
		t.Fatal("typing must work after CR")
	}
}

// TestMarginStopBoundary pins MaxCol's contract exactly: usable columns
// are 0..MaxCol-1 (so MaxCol is also the count of usable slots, the
// reading the importer's word-fits check relies on) and the carriage
// locks AT MaxCol.
func TestMarginStopBoundary(t *testing.T) {
	d := newDoc()
	max := d.MaxCol()
	for i := 0; i < max-1; i++ {
		if res := d.Apply(format.Event{DeltaMS: 80, Op: format.OpStrike, Rune: 'a'}); !res.Applied {
			t.Fatalf("strike into column %d refused; columns 0..%d must be usable", i, max-1)
		}
	}
	if d.AtLock() {
		t.Fatalf("carriage locked at column %d; the stop is %d", d.Col, max)
	}
	res := d.Apply(format.Event{DeltaMS: 80, Op: format.OpStrike, Rune: 'z'})
	if !res.Applied {
		t.Fatal("the last usable column (MaxCol-1) must take ink")
	}
	if d.Col != max || !d.AtLock() {
		t.Fatalf("after the last strike: Col=%d AtLock=%v, want Col=%d locked", d.Col, d.AtLock(), max)
	}
	if res := d.Apply(format.Event{DeltaMS: 80, Op: format.OpStrike, Rune: 'q'}); res.Applied {
		t.Fatal("a strike at the margin stop (MaxCol) must be refused")
	}
}

func TestBackspaceOverstrike(t *testing.T) {
	d := newDoc()
	typeString(d, "e")
	d.Apply(format.Event{DeltaMS: 100, Op: format.OpBack})
	typeString(d, "x")
	p := d.Pages[0]
	if len(p.Strikes) != 2 {
		t.Fatalf("got %d strikes, want 2", len(p.Strikes))
	}
	cell := p.Cells[CellKey{YHalf: 0, Col: 0}]
	if cell == nil || len(cell.Runes) != 2 {
		t.Fatalf("cell should hold 2 overstruck runes, got %+v", cell)
	}
	if cell.Runes[0] != 'e' || cell.Runes[1] != 'x' {
		t.Fatalf("cell runes = %q", string(cell.Runes))
	}
	// Both strikes share the slot centre but not the exact appearance.
	if p.Strikes[0].App == p.Strikes[1].App {
		t.Fatal("overstrikes must not be pixel-identical")
	}
}

func TestBackspaceAtMarginZero(t *testing.T) {
	d := newDoc()
	res := d.Apply(format.Event{DeltaMS: 100, Op: format.OpBack})
	if res.Applied {
		t.Fatal("backspace at column 0 must be refused")
	}
}

func TestNoTypingWithoutPaper(t *testing.T) {
	d := New(format.DefaultHeader(42, 0))
	res := d.Apply(format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: 'a'})
	if res.Applied {
		t.Fatal("strike with no sheet loaded must be refused")
	}
}

func TestPageFlipAndReinsertion(t *testing.T) {
	d := newDoc()
	typeString(d, "page one")
	d.Apply(format.Event{Op: format.OpNewSheet})
	typeString(d, "page two")
	if len(d.Pages) != 2 || d.Current != 1 {
		t.Fatalf("pages=%d current=%d", len(d.Pages), d.Current)
	}
	colOnTwo := d.Col
	res := d.Apply(format.Event{Op: format.OpPagePrev})
	if !res.Applied || d.Current != 0 {
		t.Fatal("flip to previous page failed")
	}
	if d.Pages[0].Reinserts != 1 {
		t.Fatalf("page 0 reinserts = %d, want 1", d.Pages[0].Reinserts)
	}
	// Type over the old text: appearance must differ from original pass.
	d.Col = 0
	d.YHalf = 0
	d.Apply(format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: 'p'})
	last := d.Pages[0].Strikes[len(d.Pages[0].Strikes)-1]
	first := d.Pages[0].Strikes[0]
	if last.App.DX == first.App.DX && last.App.DY == first.App.DY {
		t.Fatal("re-inserted page must strike slightly misaligned")
	}
	// Flip forward restores position on page two.
	d.Apply(format.Event{Op: format.OpPageNext})
	if d.Current != 1 || d.Col != colOnTwo {
		t.Fatalf("flip forward: current=%d col=%d want col=%d", d.Current, d.Col, colOnTwo)
	}
	// Flip past the end is refused.
	res = d.Apply(format.Event{Op: format.OpPageNext})
	if res.Applied {
		t.Fatal("flipping past the last page must be refused")
	}
}

func TestToss(t *testing.T) {
	d := newDoc()
	typeString(d, "rubbish")
	d.Apply(format.Event{Op: format.OpToss})
	if !d.Pages[0].Tossed {
		t.Fatal("tossed page must be flagged")
	}
	if len(d.Pages) != 2 || d.Current != 1 {
		t.Fatal("toss must feed a fresh sheet")
	}
	if len(d.LivePages()) != 1 {
		t.Fatal("bin pages must not be live")
	}
	if len(d.Pages[0].Strikes) != 7 {
		t.Fatal("tossed page keeps its strikes (nothing is ever deleted)")
	}
	// Flip back to the binned sheet: viewable, but a scrunched ball of
	// paper takes no new ink.
	d.Apply(format.Event{Op: format.OpPagePrev})
	res := d.Apply(format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: 'z'})
	if res.Applied {
		t.Fatal("strike on a binned sheet must be refused")
	}
	if len(d.Pages[0].Strikes) != 7 {
		t.Fatal("binned sheet gained ink")
	}
}

func TestPageBottom(t *testing.T) {
	d := newDoc()
	full := false
	for range 200 {
		res := d.Apply(format.Event{DeltaMS: 50, Op: format.OpCR})
		if res.PageFull {
			full = true
			break
		}
	}
	if !full {
		t.Fatal("CR spam must eventually report PageFull")
	}
	if d.YHalf != d.MaxYHalf() {
		t.Fatalf("baseline clamped at %d, want %d", d.YHalf, d.MaxYHalf())
	}
	// Strikes on the last line still land on the paper.
	res := d.Apply(format.Event{DeltaMS: 50, Op: format.OpStrike, Rune: 'z'})
	if !res.Applied {
		t.Fatal("last line must still be writable")
	}
	last := d.Pages[0].Strikes[len(d.Pages[0].Strikes)-1]
	if last.YMM > units.PaperHeightMM-d.Margins.Bottom {
		t.Fatalf("baseline %f is below the bottom margin", last.YMM)
	}
}

func TestPlatenHalfSteps(t *testing.T) {
	d := newDoc()
	// Move off the top line first: half-up at the very top is refused.
	d.Apply(format.Event{Op: format.OpCR})
	typeString(d, "x2")
	d.Apply(format.Event{Op: format.OpBack})
	d.Apply(format.Event{Op: format.OpHalfUp})
	res := d.Apply(format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: '2'})
	if !res.Applied {
		t.Fatal("superscript strike refused")
	}
	sup := d.Pages[0].Strikes[len(d.Pages[0].Strikes)-1]
	base := d.Pages[0].Strikes[0]
	if sup.YMM >= base.YMM {
		t.Fatal("half-up strike must sit above the baseline")
	}
	// HalfUp at top of page is refused.
	d2 := newDoc()
	if r := d2.Apply(format.Event{Op: format.OpHalfUp}); r.Applied {
		t.Fatal("half-up at top must be refused")
	}
}

func TestPicaLineFitsA4(t *testing.T) {
	d := newDoc()
	// A4 210mm, margins 25+20 -> 165mm usable, Pica 2.54mm -> 64 slots.
	if got := d.MaxCol() + 1; got != 64 {
		t.Fatalf("Pica usable slots = %d, want 64", got)
	}
	d.Apply(format.Event{Op: format.OpSetPitch, Value: 12})
	if got := d.MaxCol() + 1; got != 77 {
		t.Fatalf("Elite usable slots = %d, want 77", got)
	}
}

func TestSettingsEvents(t *testing.T) {
	d := newDoc()
	d.Apply(format.Event{Op: format.OpSetLinespace, Value: 20})
	y0 := d.YHalf
	d.Apply(format.Event{Op: format.OpCR})
	if d.YHalf-y0 != 4 {
		t.Fatalf("double spacing CR advanced %d halves, want 4", d.YHalf-y0)
	}
	d.Apply(format.Event{Op: format.OpSetMargins, Margins: units.Margins{Left: 40, Right: 40, Top: 30, Bottom: 30}})
	if d.Margins.Left != 40 {
		t.Fatal("margins not applied")
	}
}

func TestNewRibbonResetsWear(t *testing.T) {
	d := newDoc()
	typeString(d, "some wear on the spool")
	if d.ribbonStrikes == 0 {
		t.Fatal("test setup: typing must accumulate ribbon strikes")
	}
	res := d.Apply(format.Event{Op: format.OpNewRibbon})
	if !res.Applied {
		t.Fatal("NEW_RIBBON must apply")
	}
	if d.ribbonStrikes != 0 || d.ribbon != 1 {
		t.Fatalf("after NEW_RIBBON: strikes=%d ribbon=%d, want 0 and 1", d.ribbonStrikes, d.ribbon)
	}
	typeString(d, "x")
	if d.ribbonStrikes != 1 {
		t.Fatalf("strikes since new ribbon = %d, want 1", d.ribbonStrikes)
	}
}

func TestNewRibbonWorksWithoutPaper(t *testing.T) {
	// Maintenance is not typing: replacing the ribbon needs no sheet.
	d := New(format.DefaultHeader(42, 0))
	res := d.Apply(format.Event{Op: format.OpNewRibbon})
	if !res.Applied || d.ribbon != 1 {
		t.Fatalf("NEW_RIBBON without paper: applied=%v ribbon=%d", res.Applied, d.ribbon)
	}
}

func TestPaperSeedVariesPerSheet(t *testing.T) {
	d := newDoc()
	if d.PaperSeed(0) == d.PaperSeed(1) {
		t.Fatal("each sheet must get its own paper grain seed")
	}
	e := New(format.DefaultHeader(43, 0))
	if d.PaperSeed(0) == e.PaperSeed(0) {
		t.Fatal("different machines must grain their paper differently")
	}
}

func TestSetTouchAffectsSubsequentStrikes(t *testing.T) {
	before := newDoc()
	typeString(before, "a")
	after := newDoc()
	after.Apply(format.Event{Op: format.OpSetTouch, Value: 85})
	typeString(after, "a")
	b := before.Pages[0].Strikes[0].App.Ink
	a := after.Pages[0].Strikes[0].App.Ink
	if !(a < b) {
		t.Fatalf("light touch must print lighter: before=%.3f after=%.3f", b, a)
	}
	// Value 0 is ignored (defensive: a corrupt event must not zero ink).
	after.Apply(format.Event{Op: format.OpSetTouch, Value: 0})
	if after.touch != 0.85 {
		t.Fatalf("SET_TOUCH 0 must be ignored, touch=%v", after.touch)
	}
}

// TestVerifyModelRefusesMismatch pins the DESIGN.md promise: renderers
// refuse model-version mismatches rather than misrender.
func TestVerifyModelRefusesMismatch(t *testing.T) {
	h := format.DefaultHeader(42, 0)
	if err := VerifyModel(h); err != nil {
		t.Fatalf("current model version must verify: %v", err)
	}
	h.ModelVersion = 99
	if err := VerifyModel(h); err == nil {
		t.Fatal("model v99 must be refused, not misrendered")
	}
}

func TestHumanAndMachineDialsAffectStrikes(t *testing.T) {
	// Two docs, same seed and text; one furious/legless/bashed, one at
	// defaults. Their strikes must differ, and the dials must be settable
	// mid-document (future strikes only).
	base := newDoc()
	typeString(base, "aaa")

	moody := newDoc()
	moody.Apply(format.Event{Op: format.OpSetDisposition, Value: 180})
	moody.Apply(format.Event{Op: format.OpSetSobriety, Value: 185})
	moody.Apply(format.Event{Op: format.OpSetCondition, Value: 200})
	typeString(moody, "aaa")

	b := base.Pages[0].Strikes[0].App
	m := moody.Pages[0].Strikes[0].App
	if b == m {
		t.Fatal("furious/legless/bashed strike must differ from a plain one")
	}
	if moody.Condition() != 2.0 {
		t.Fatalf("condition getter = %v, want 2.0", moody.Condition())
	}
	// Value 0 is ignored (defensive against corrupt events).
	moody.Apply(format.Event{Op: format.OpSetCondition, Value: 0})
	if moody.condition != 2.0 {
		t.Fatalf("SET_CONDITION 0 must be ignored, condition=%v", moody.condition)
	}
}
