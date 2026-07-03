package machine

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"testing"
)

// TestHashMatchesStdlibFNV pins the hand-rolled FNV-1a fold in h against
// hash/fnv byte for byte: every rendered page ever written depends on
// these values, so the fold must be bit-identical to the stdlib hashing
// of (seed LE, label bytes, args LE) it replaced.
func TestHashMatchesStdlibFNV(t *testing.T) {
	ref := func(seed uint64, label string, args ...int64) uint64 {
		f := fnv.New64a()
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		f.Write(b[:])
		f.Write([]byte(label))
		for _, a := range args {
			binary.LittleEndian.PutUint64(b[:], uint64(a))
			f.Write(b[:])
		}
		return f.Sum64()
	}
	cases := []struct {
		seed  uint64
		label string
		args  []int64
	}{
		{0, "", nil},
		{1, "bias", []int64{65}},
		{0xDEADBEEF, "jit", []int64{3, 14, 15, 92}},
		{^uint64(0), "wanderY", []int64{-1, -7}},
		{42, "pair", []int64{104, 105}},
		{7, "insert", []int64{2, 3}},
	}
	for _, c := range cases {
		m := New(c.seed)
		if got, want := m.h(c.label, c.args...), ref(c.seed, c.label, c.args...); got != want {
			t.Fatalf("h(seed=%d, %q, %v) = %d, want %d (stdlib FNV-1a)", c.seed, c.label, c.args, got, want)
		}
	}
}

func TestDeterminism(t *testing.T) {
	m1 := New(42)
	m2 := New(42)
	c := Context{Glyph: 'e', Prev: 'h', DeltaMS: 120, Page: 0, Row: 3, Col: 10, RibbonStrikes: 500}
	if m1.StrikeFor(c) != m2.StrikeFor(c) {
		t.Fatal("same seed + same context must give identical strikes")
	}
}

func TestSeedsDiffer(t *testing.T) {
	c := Context{Glyph: 'e', Prev: 'h', DeltaMS: 120}
	a := New(1).StrikeFor(c)
	b := New(2).StrikeFor(c)
	if a == b {
		t.Fatal("different seeds should give different machines")
	}
}

func TestHammerBiasIsConstant(t *testing.T) {
	m := New(7)
	// Same glyph in different places keeps the same hammer bias
	// component. Isolate it by zeroing jitter differences: compare two
	// contexts differing only in position and check deviation stays
	// within jitter bounds.
	a := m.StrikeFor(Context{Glyph: 'e', DeltaMS: 300, Row: 1, Col: 1})
	b := m.StrikeFor(Context{Glyph: 'e', DeltaMS: 300, Row: 20, Col: 40})
	if math.Abs(a.DX-b.DX) > 2*jitXYmm+1e-9 || math.Abs(a.DY-b.DY) > 2*jitXYmm+1e-9 {
		t.Fatalf("hammer bias drifted more than jitter allows: a=%+v b=%+v", a, b)
	}
	if a.GradAxis != b.GradAxis {
		t.Fatal("gradient axis is per-hammer and must not vary by position")
	}
}

func TestBoundsAcrossManyStrikes(t *testing.T) {
	m := New(99)
	glyphs := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.,;:!?'\"-()")
	for i := range 20000 {
		c := Context{
			Glyph:         glyphs[i%len(glyphs)],
			Prev:          glyphs[(i+13)%len(glyphs)],
			DeltaMS:       uint64(i % 3000),
			Page:          i % 30,
			Row:           i % 60,
			Col:           i % 80,
			NthOnCell:     i % 3,
			RibbonStrikes: i * 10,
			Reinsert:      i % 4,
		}
		s := m.StrikeFor(c)
		// Total planar deviation must stay small enough to remain
		// legible at Pica pitch (slot 2.54mm): worst case well under
		// half a slot.
		if math.Abs(s.DX) > 1.0 || math.Abs(s.DY) > 1.1 {
			t.Fatalf("strike %d deviates too far: %+v (ctx %+v)", i, s, c)
		}
		if s.Ink < 0.4 || s.Ink > 1.35 {
			t.Fatalf("strike %d ink out of range: %+v", i, s)
		}
		if s.GradAmt < 0 || s.GradAmt > 0.5 {
			t.Fatalf("strike %d gradient out of range: %+v", i, s)
		}
		if math.Abs(s.TiltDeg) > 3.0 {
			t.Fatalf("strike %d tilt out of range: %+v", i, s)
		}
	}
}

func TestOverstrikesNeverIdentical(t *testing.T) {
	m := New(5)
	base := Context{Glyph: 'x', DeltaMS: 200, Row: 5, Col: 5}
	first := m.StrikeFor(base)
	base.NthOnCell = 1
	second := m.StrikeFor(base)
	if first == second {
		t.Fatal("overstrike on same cell must differ from first strike")
	}
}

func TestRhythmDynamics(t *testing.T) {
	m := New(11)
	fast := m.StrikeFor(Context{Glyph: 'e', DeltaMS: 30, RibbonStrikes: 100})
	norm := m.StrikeFor(Context{Glyph: 'e', DeltaMS: 300, RibbonStrikes: 100})
	slow := m.StrikeFor(Context{Glyph: 'e', DeltaMS: 4000, RibbonStrikes: 100})
	if !(fast.Ink < norm.Ink) {
		t.Fatalf("fast strike should be lighter: fast=%.3f norm=%.3f", fast.Ink, norm.Ink)
	}
	if !(slow.Ink > norm.Ink) {
		t.Fatalf("deliberate strike should be heavier: slow=%.3f norm=%.3f", slow.Ink, norm.Ink)
	}
	if !(fast.DX < norm.DX) {
		t.Fatalf("fast strike should pull toward previous: fast=%.3f norm=%.3f", fast.DX, norm.DX)
	}
}

func TestInkVariesAtSteadyRhythm(t *testing.T) {
	// Mid-tempo typing (100-400 ms) must NOT produce uniform ink: real
	// fingers vary in force. Guard against the dead-band regression.
	m := New(21)
	var min, max float64 = 10, 0
	text := "the quick brown fox jumps over the lazy dog"
	col := 0
	var prev rune
	for i, r := range text {
		if r == ' ' {
			col++
			prev = r
			continue
		}
		s := m.StrikeFor(Context{
			Glyph: r, Prev: prev, DeltaMS: uint64(120 + (i*37)%260),
			Row: 2, Col: col, RibbonStrikes: i,
		})
		if s.Ink < min {
			min = s.Ink
		}
		if s.Ink > max {
			max = s.Ink
		}
		col++
		prev = r
	}
	if max-min < 0.10 {
		t.Fatalf("ink spread %.3f over a steady sentence - too uniform (min %.3f max %.3f)", max-min, min, max)
	}
}

func TestRibbonWear(t *testing.T) {
	m := New(3)
	fresh := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, RibbonStrikes: 0})
	tired := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, RibbonStrikes: 500000})
	if !(tired.Ink < fresh.Ink) {
		t.Fatalf("ribbon must wear: fresh=%.3f tired=%.3f", fresh.Ink, tired.Ink)
	}
}

// TestNewRibbonRestoresInk pins the owner's replace-ribbon feature: a worn
// ribbon swapped for spool 1 must print heavier again, wet-fresh boost and
// all - regardless of the small per-spool bias.
func TestNewRibbonRestoresInk(t *testing.T) {
	m := New(3)
	worn := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, RibbonStrikes: 200000, Ribbon: 0})
	replaced := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, RibbonStrikes: 0, Ribbon: 1})
	if !(replaced.Ink > worn.Ink) {
		t.Fatalf("new ribbon must restore ink: worn=%.3f replaced=%.3f", worn.Ink, replaced.Ink)
	}
}

// TestFreshRibbonPrintsWet pins the wet-start: the very first strikes on a
// new spool ink slightly heavier than the settled early life of the ribbon.
func TestFreshRibbonPrintsWet(t *testing.T) {
	m := New(3)
	wet := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, RibbonStrikes: 0})
	settled := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, RibbonStrikes: 8000})
	if !(wet.Ink > settled.Ink) {
		t.Fatalf("fresh ribbon must print wet: wet=%.3f settled=%.3f", wet.Ink, settled.Ink)
	}
}

// TestRibbonSpoolsDiffer pins per-spool character: the same strike on two
// different ribbons does not ink identically.
func TestRibbonSpoolsDiffer(t *testing.T) {
	m := New(3)
	a := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, Ribbon: 0})
	b := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, Ribbon: 1})
	if a.Ink == b.Ink {
		t.Fatal("two ribbon spools must not ink identically")
	}
}

// TestTexVariesPerStrike pins the texture seed: neighbouring strikes must
// get independent ink-texture noise or fast typing looks screen-printed.
func TestTexVariesPerStrike(t *testing.T) {
	m := New(3)
	seen := map[uint64]bool{}
	for col := range 20 {
		s := m.StrikeFor(Context{Glyph: 'e', DeltaMS: 150, Row: 4, Col: col})
		if seen[s.Tex] {
			t.Fatalf("texture seed repeated at col %d", col)
		}
		seen[s.Tex] = true
	}
}

// TestFillBoundedAndPerHammer pins die fouling: within [0, fillMax], fixed
// per glyph, different across glyphs (checked over the alphabet so a single
// hash collision cannot fail the test).
func TestFillBoundedAndPerHammer(t *testing.T) {
	m := New(3)
	distinct := map[float64]bool{}
	for r := 'a'; r <= 'z'; r++ {
		s := m.StrikeFor(Context{Glyph: r, DeltaMS: 300})
		if s.Fill < 0 || s.Fill > fillMax {
			t.Fatalf("fill %.3f for %q outside [0, %v]", s.Fill, r, fillMax)
		}
		again := m.StrikeFor(Context{Glyph: r, DeltaMS: 900, Col: 30})
		if again.Fill != s.Fill {
			t.Fatalf("fill for %q must be constant per hammer", r)
		}
		distinct[s.Fill] = true
	}
	if len(distinct) < 10 {
		t.Fatalf("only %d distinct fill values over the alphabet", len(distinct))
	}
}

func TestReinsertionOffset(t *testing.T) {
	m := New(8)
	orig := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, Page: 2})
	back := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, Page: 2, Reinsert: 1})
	if orig.DX == back.DX && orig.DY == back.DY {
		t.Fatal("re-inserted page must be slightly misaligned")
	}
}

// TestTouchScalesInk pins the touch dial: a light hand prints lighter, a
// firm hand heavier, all else equal. Touch 0 must behave as 1.0 so files
// without SET_TOUCH events keep rendering as before.
func TestTouchScalesInk(t *testing.T) {
	m := New(3)
	light := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, Touch: 0.85})
	unset := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300})
	medium := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, Touch: 1.0})
	firm := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, Touch: 1.12})
	if !(light.Ink < medium.Ink && medium.Ink < firm.Ink) {
		t.Fatalf("touch must order ink: light=%.3f medium=%.3f firm=%.3f", light.Ink, medium.Ink, firm.Ink)
	}
	if unset != medium {
		t.Fatal("Touch 0 must be identical to Touch 1.0 (old files)")
	}
}

// TestTouchWearsRibbonFaster pins the wear coupling: at the same strike
// count, a firm typist's ribbon is further gone (compare wear ratios so
// the direct ink scaling cancels out).
func TestTouchWearsRibbonFaster(t *testing.T) {
	m := New(3)
	ratio := func(touch float64) float64 {
		worn := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, RibbonStrikes: 60000, Touch: touch})
		fresh := m.StrikeFor(Context{Glyph: 'a', DeltaMS: 300, RibbonStrikes: 0, Touch: touch})
		return worn.Ink / fresh.Ink
	}
	if !(ratio(1.12) < ratio(0.85)) {
		t.Fatalf("firm touch must deplete the ribbon faster: firm ratio=%.4f light ratio=%.4f", ratio(1.12), ratio(0.85))
	}
}

// avgInk averages ink over the alphabet at a given context template, so
// mood/condition effects show through per-glyph hammer bias.
func avgInk(m *Machine, tmpl Context) float64 {
	sum := 0.0
	n := 0
	for r := 'a'; r <= 'z'; r++ {
		c := tmpl
		c.Glyph = r
		sum += m.StrikeFor(c).Ink
		n++
	}
	return sum / float64(n)
}

// spread returns max-min of a lever over a line of columns.
func spreadDX(m *Machine, tmpl Context) float64 {
	lo, hi := 1e9, -1e9
	for col := 0; col < 60; col++ {
		c := tmpl
		c.Col = col
		c.Glyph = rune('a' + col%26)
		v := m.StrikeFor(c).DX
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return hi - lo
}

// TestDispositionDarkensAndScatters pins the mood dial: a furious hand
// prints heavier than a composed one (averaged over the alphabet so
// per-hammer bias does not mask it).
func TestDispositionDarkensAndScatters(t *testing.T) {
	m := New(3)
	composed := avgInk(m, Context{DeltaMS: 300, Disposition: 1.0})
	furious := avgInk(m, Context{DeltaMS: 300, Disposition: 1.8})
	if !(furious > composed) {
		t.Fatalf("furious must print heavier: composed=%.3f furious=%.3f", composed, furious)
	}
	// Unset disposition must equal composed exactly (old files).
	if avgInk(m, Context{DeltaMS: 300}) != composed {
		t.Fatal("unset disposition must be identical to 1.0")
	}
}

// TestSobrietyWandersBaseline pins the drunk dial: legless typing spreads
// the baseline (DY) far more than sober typing across a line, while sober
// stays where the machine put it.
func TestSobrietyWandersBaseline(t *testing.T) {
	m := New(3)
	spreadDY := func(sob float64) float64 {
		lo, hi := 1e9, -1e9
		for col := 0; col < 60; col++ {
			v := m.StrikeFor(Context{Glyph: 'e', DeltaMS: 200, Row: 3, Col: col, Sobriety: sob}).DY
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		return hi - lo
	}
	sober := spreadDY(1.0)
	legless := spreadDY(1.85)
	if !(legless > sober*1.5) {
		t.Fatalf("legless baseline must wander much more: sober spread=%.3f legless spread=%.3f", sober, legless)
	}
}

// TestConditionScalesInconsistency pins Bash/Fix: a banged-up machine
// scatters letter placement more than a serviced one, and a serviced one
// more uniform than factory. Unset condition equals factory (1.0).
func TestConditionScalesInconsistency(t *testing.T) {
	m := New(3)
	fixed := spreadDX(m, Context{DeltaMS: 300, Condition: 0.4})
	factory := spreadDX(m, Context{DeltaMS: 300, Condition: 1.0})
	bashed := spreadDX(m, Context{DeltaMS: 300, Condition: 2.0})
	if !(fixed < factory && factory < bashed) {
		t.Fatalf("condition must order placement scatter: fixed=%.3f factory=%.3f bashed=%.3f", fixed, factory, bashed)
	}
	if spreadDX(m, Context{DeltaMS: 300}) != factory {
		t.Fatal("unset condition must equal factory (1.0)")
	}
}

// TestConditionClampsToRange pins that absurd condition values cannot fly
// the letters off the sheet: even far out of range, offsets stay bounded.
func TestConditionClampsToRange(t *testing.T) {
	m := New(3)
	for _, cond := range []float64{-5, 0.01, 99} {
		for r := 'a'; r <= 'z'; r++ {
			s := m.StrikeFor(Context{Glyph: r, DeltaMS: 300, Sobriety: 1.85, Disposition: 1.8, Condition: cond, Col: 17, Row: 4})
			if math.Abs(s.DX) > 2.0 || math.Abs(s.DY) > 2.0 {
				t.Fatalf("cond=%v glyph=%q flew off: DX=%.3f DY=%.3f", cond, r, s.DX, s.DY)
			}
			if s.Ink < inkMin-1e-9 || s.Ink > inkMax+1e-9 {
				t.Fatalf("cond=%v glyph=%q ink out of range: %.3f", cond, r, s.Ink)
			}
		}
	}
}
