// Package machine is the personality model, version 1. Given a seed and
// the circumstances of a strike, it returns how that strike looks: offset,
// tilt, ink density, and ink gradient. Pure functions of their inputs —
// no RNG, no time, no global state — so the same file renders identically
// everywhere, forever. See docs/DESIGN.md section 3.
package machine

import (
	"math"
	"unicode"
)

// ModelVersion implemented by this package. Must match the file header;
// renderers refuse mismatches rather than misrender.
const ModelVersion = 1

// Strike is the appearance of one hammer strike.
type Strike struct {
	DX, DY   float64 // offset from slot centre, mm
	TiltDeg  float64 // rotation about glyph centre
	Ink      float64 // 0..~1.2 multiplier on glyph alpha
	GradAxis float64 // radians; direction of the ink gradient (hammer face tilt)
	GradAmt  float64 // 0..0.5; how uneven the inking is across the glyph
	Tex      uint64  // per-strike texture seed (ink speckle, ribbon weave phase)
	Fill     float64 // 0..1 die fouling; gunked dies print fatter, counters close up
}

// Context is everything the model needs to know about one strike.
type Context struct {
	Glyph         rune
	Prev          rune   // previous struck glyph in time order (0 if none)
	DeltaMS       uint64 // time since previous event
	Page          int    // 0-based page index in the stack
	Row, Col      int    // slot coordinates on the page
	NthOnCell     int    // 0 for first strike on this cell, 1 for overstrike...
	RibbonStrikes int    // strikes since the current ribbon was installed (wear)
	Ribbon        int    // which ribbon is installed, 0-based (per-ribbon character)
	Reinsert      int    // how many times this page has been re-inserted

	// Touch is the writer's hand: how hard they habitually strike.
	// 1.0 is a medium touch; ~0.85 a light typist, ~1.12 a firm one.
	// 0 means unset and is treated as 1.0. Set via SET_TOUCH events.
	Touch float64

	// Disposition is the writer's mood (1.0 composed, higher = angrier):
	// ink up, force scatter up. Sobriety is their state (1.0 sober,
	// higher = drunker): baseline wander, looser placement. Condition is
	// the machine's wear (1.0 factory, < 1 serviced, > 1 banged up):
	// scales the machine's own inconsistencies. 0 means unset -> 1.0.
	Disposition float64
	Sobriety    float64
	Condition   float64
}

// Machine derives strike appearance from a seed.
type Machine struct {
	seed uint64
}

func New(seed uint64) *Machine { return &Machine{seed: seed} }

// FNV-1a constants (hash/fnv's offset64 and prime64), for the hand-rolled
// fold in h.
const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// h hashes the seed plus a label plus integer arguments to a uint64. The
// FNV-1a fold is done by hand: h runs up to a dozen times per strike and
// the hash/fnv object costs an interface method call per Write. The fold
// is BIT-IDENTICAL to fnv.New64a over the same little-endian bytes -
// TestHashMatchesStdlibFNV pins that, because every page ever rendered
// depends on these values never changing.
func (m *Machine) h(label string, args ...int64) uint64 {
	v := fnvFold8(fnvOffset64, m.seed)
	for i := 0; i < len(label); i++ {
		v = (v ^ uint64(label[i])) * fnvPrime64
	}
	for _, a := range args {
		v = fnvFold8(v, uint64(a))
	}
	return v
}

// fnvFold8 folds one uint64, as its 8 little-endian bytes, into an FNV-1a
// state.
func fnvFold8(h, v uint64) uint64 {
	for range 8 {
		h = (h ^ (v & 0xFF)) * fnvPrime64
		v >>= 8
	}
	return h
}

// unit maps a hash to [0,1). Uses the top 53 bits for a clean float.
func unit(h uint64) float64 {
	return float64(h>>11) / float64(1<<53)
}

// span maps a hash to [-r, +r].
func span(h uint64, r float64) float64 {
	return (unit(h)*2 - 1) * r
}

// derive splits one hash into several independent streams cheaply.
func derive(h uint64, n uint64) uint64 {
	// SplitMix64 step keyed by n.
	z := h + n*0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Model constants (version 1). Changing any of these is a model version
// bump — old files must keep rendering with old constants.
const (
	biasXmm = 0.15
	biasYmm = 0.18
	// Slugs are soldered to their bars: rotation is the smallest of the
	// deviations, mostly visible only in tall glyphs.
	biasTiltDeg  = 1.1
	gradMin      = 0.05
	gradMax      = 0.35
	shiftYmm     = 0.22
	shiftWtMax   = 0.15
	pairXmm      = 0.08
	jitXYmm      = 0.05
	jitTiltDeg   = 0.3
	fastMS       = 90
	slowMS       = 1400
	fastPullMM   = 0.10
	ribbonFloor  = 0.72
	ribbonSpan   = 0.28
	ribbonDecayN = 45000.0
	// A fresh ribbon prints wet for its first few thousand strikes, and no
	// two ribbon spools ink quite alike.
	ribbonWetBoost = 0.05
	ribbonWetN     = 1200.0
	ribbonBiasSpan = 0.03
	insertXmm      = 0.4
	insertYmm      = 0.5

	// Die fouling: dried ink and fibre gunk in the die recesses. Most
	// hammers are near clean (the unit hash is squared to skew low); the
	// worst offenders print visibly fatter with closing counters.
	fillMax = 0.30

	// Force variation: no two finger strikes are equal even at a steady
	// rhythm. A per-hammer flatness bias plus per-strike jitter.
	inkBiasSpan = 0.06 // each hammer hits ±6% from nominal, forever
	inkJitSpan  = 0.11 // each individual strike varies ±11%
	inkMin      = 0.45
	inkMax      = 1.30

	// Human disposition (SET_DISPOSITION, 1.0 = composed). A furious
	// typist hammers harder (ink up, toward the ceiling), with more force
	// scatter and a touch more rotation. Sound rides ink, so a furious
	// page also sounds harder for free.
	dispInkGain  = 0.34 // extra ink per unit of fury above composed
	dispJitGain  = 1.05 // widens per-strike ink scatter
	dispTiltGain = 0.55 // and per-strike rotation

	// Human sobriety (SET_SOBRIETY, 1.0 = sober). Drink does not add
	// force, it removes control: the baseline wanders, placement loosens,
	// letters lean. Distinct fingerprint from fury (loose, not violent).
	sobJitGain   = 1.30 // looser placement
	sobTiltGain  = 1.05 // and rotation
	sobWanderMM  = 0.55 // baseline roam at full legless
	wanderPeriod = 7    // columns per wander lattice cell (low frequency)

	// Machine condition (SET_CONDITION, 1.0 = as it left the factory).
	// Scales the machine's OWN inconsistencies - hammer alignment, die
	// fouling, per-die ink flatness - so a serviced machine (< 1.0)
	// prints more uniform letters and a barn find (> 1.0) prints a wreck.
	// This is the typewriter's wear, kept separate from the writer's hand
	// (touch) and state (disposition/sobriety).
	ConditionMin = 0.30
	ConditionMax = 2.20
)

// ClampCondition constrains a raw condition dial to the range the model
// honours. Exported so a frontend's persisted preference cannot drift
// outside the band StrikeFor enforces.
func ClampCondition(v float64) float64 {
	return math.Max(ConditionMin, math.Min(ConditionMax, v))
}

// norm treats an unset (zero) dial as its identity value of 1.0, so files
// without the corresponding SET_ event render exactly as before.
func norm(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}

// PaperSeed derives the texture seed for one sheet of paper: every sheet
// fed into this machine gets its own grain, deterministically.
func (m *Machine) PaperSeed(page int) uint64 {
	return m.h("paper", int64(page))
}

// wander returns a smooth drift in [-1, 1] for a slot, low-frequency
// along the line (lattice every wanderPeriod columns, smoothstep
// interpolated) so a drunk baseline undulates instead of jittering.
func (m *Machine) wander(label string, row, col int) float64 {
	i := col / wanderPeriod
	f := float64(col%wanderPeriod) / float64(wanderPeriod)
	f = f * f * (3 - 2*f)
	a := unit(m.h(label, int64(row), int64(i)))*2 - 1
	b := unit(m.h(label, int64(row), int64(i+1)))*2 - 1
	return a*(1-f) + b*f
}

// StrikeFor returns the appearance of a strike in context c.
func (m *Machine) StrikeFor(c Context) Strike {
	var s Strike

	touch := norm(c.Touch)
	disp := norm(c.Disposition)
	sob := norm(c.Sobriety)
	cond := ClampCondition(norm(c.Condition))

	// Per-strike deviation amplitudes, scaled by the machine's condition
	// (its own inconsistency) and, for placement, the writer's sobriety.
	jxy := jitXYmm * cond * (1 + sobJitGain*(sob-1))
	jtilt := jitTiltDeg * cond * (1 + dispTiltGain*(disp-1)) * (1 + sobTiltGain*(sob-1))
	inkScatter := inkJitSpan * (2 - touch) * (1 + dispJitGain*(disp-1))

	// 1. Hammer bias: each glyph's die has a fixed misalignment, wider on
	// a worn machine.
	hb := m.h("bias", int64(c.Glyph))
	s.DX = span(derive(hb, 1), biasXmm*cond)
	s.DY = span(derive(hb, 2), biasYmm*cond)
	s.TiltDeg = span(derive(hb, 3), biasTiltDeg*cond)
	s.GradAxis = unit(derive(hb, 4)) * 2 * math.Pi
	s.GradAmt = gradMin + unit(derive(hb, 5))*(gradMax-gradMin)*cond
	s.Ink = 1.0

	// 2. Basket shift: capitals ride the lifted basket.
	if unicode.IsUpper(c.Glyph) {
		hs := m.h("shift")
		s.DY += span(derive(hs, 1), shiftYmm)
		s.Ink *= 1.0 + unit(derive(hs, 2))*shiftWtMax
	}

	// 3. Pair slack: the linkage remembers the previous letter.
	if c.Prev != 0 {
		hp := m.h("pair", int64(c.Prev), int64(c.Glyph))
		s.DX += span(hp, pairXmm)
	}

	// 4. Rhythm dynamics from recorded timing: a smooth curve, not a
	// dead band - even mid-tempo typing carries a little of the rhythm.
	if c.DeltaMS > 0 {
		dt := float64(c.DeltaMS)
		switch {
		case dt < fastMS:
			// Flying start: lighter, pulled toward the previous strike.
			speed := 1.0 - dt/fastMS // 0..1, faster = higher
			s.Ink *= 0.95 - 0.17*speed
			s.DX -= fastPullMM * speed
			s.GradAmt = math.Min(0.5, s.GradAmt*(1+0.5*speed))
		case dt < 350:
			// Rolling along: slightly under full force, easing up to it.
			s.Ink *= 0.95 + 0.05*(dt-fastMS)/(350-fastMS)
		case dt < slowMS:
			// Settled rhythm: nominal force.
		default:
			// Deliberate strike after a pause lands heavy.
			s.Ink *= 1.05 + 0.10*math.Min(1, (dt-slowMS)/5000)
		}
	}

	// 5. Per-strike jitter so overstrikes never align perfectly, and so
	// no two strikes carry identical force. A light touch is also a less
	// consistent one - the force variation widens as the hand relaxes.
	hj := m.h("jit", int64(c.Page), int64(c.Row), int64(c.Col), int64(c.NthOnCell))
	s.DX += span(derive(hj, 1), jxy)
	s.DY += span(derive(hj, 2), jxy)
	s.TiltDeg += span(derive(hj, 3), jtilt)
	s.Ink *= 1 + span(derive(hj, 4), inkScatter)
	s.Tex = derive(hj, 5)

	// 5b. Per-hammer flatness: some dies just print heavier, more so on a
	// worn machine.
	s.Ink *= 1 + span(m.h("inkbias", int64(c.Glyph)), inkBiasSpan*cond)

	// 5c. Die fouling, constant per hammer, worse on a worn machine.
	fu := unit(m.h("fill", int64(c.Glyph)))
	s.Fill = math.Min(1, fillMax*cond*fu*fu)

	// 5d. Sobriety: the baseline wanders and the hand sways. Low-frequency
	// drift, so the line undulates rather than shaking.
	if sob > 1 {
		amp := sobWanderMM * (sob - 1)
		s.DY += amp * m.wander("wanderY", c.Row, c.Col)
		s.DX += amp * 0.6 * m.wander("wanderX", c.Row, c.Col)
	}

	// 6. Ribbon wear since installation, wet-fresh boost, and the spool's
	// own character. Replacing the ribbon (NEW_RIBBON event) resets the
	// strike counter and moves to the next spool. A firm touch transfers
	// more ink per strike, so it also depletes the spool faster (touch^2
	// on the effective strike count).
	n := float64(c.RibbonStrikes) * touch * touch
	wear := ribbonFloor + ribbonSpan*math.Exp(-n/ribbonDecayN)
	wet := 1 + ribbonWetBoost*math.Exp(-n/ribbonWetN)
	s.Ink *= wear * wet
	s.Ink *= 1 + span(m.h("ribbon", int64(c.Ribbon)), ribbonBiasSpan)

	// 6b. The writer's hand and mood, last of the force factors: a firm
	// touch prints heavier, a furious one hammers harder still.
	s.Ink *= touch
	s.Ink *= 1 + dispInkGain*(disp-1)

	s.Ink = math.Max(inkMin, math.Min(inkMax, s.Ink))

	// 7. Page re-insertion misalignment.
	if c.Reinsert > 0 {
		hi := m.h("insert", int64(c.Page), int64(c.Reinsert))
		s.DX += span(derive(hi, 1), insertXmm)
		s.DY += span(derive(hi, 2), insertYmm)
	}

	return s
}
