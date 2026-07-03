// Package page folds a STRIKE event stream into document state: a stack
// of pages, each holding the strikes that hit it. This is the single
// source of truth for both the screen and every exporter.
//
// Coordinates: the carriage column is an integer slot index from the left
// margin; the vertical position is an integer count of half-lines
// (yHalf) below the first baseline. The first baseline sits one base line
// height below the top margin.
package page

import (
	"fmt"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/machine"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

// VerifyModel refuses headers whose personality-model version this build
// does not implement. DESIGN.md promises "renderers refuse mismatches
// rather than misrender": deriving appearance with the wrong model would
// silently misrepresent the manuscript, which is worse than an error.
// Callers that only read metadata (info, hash verification) need not
// check; anything that folds strikes for display or export must.
func VerifyModel(h format.Header) error {
	if h.ModelVersion != machine.ModelVersion {
		return fmt.Errorf("strike: file uses personality model v%d, this build implements v%d; rendering would misrepresent the manuscript", h.ModelVersion, machine.ModelVersion)
	}
	return nil
}

// The version stamped into a new file's header (format.ModelVersion, via
// DefaultHeader) and the version the renderer implements and VerifyModel
// checks against (machine.ModelVersion) must stay equal, or every newly
// created document would fail VerifyModel on its next open. These two
// assertions fail to compile if the constants drift apart, because uint()
// of a negative constant overflows - a build-time guard, no test needed.
const (
	_ = uint(format.ModelVersion - machine.ModelVersion)
	_ = uint(machine.ModelVersion - format.ModelVersion)
)

// CellKey identifies one character cell on a page.
type CellKey struct {
	YHalf int // half-lines below first baseline
	Col   int // slots right of left margin
}

// StrikeRec is one hammer strike as it landed.
type StrikeRec struct {
	Rune rune
	Cell CellKey
	XMM  float64 // slot centre, before machine offsets
	YMM  float64 // baseline, before machine offsets
	App  machine.Strike
	AtMS uint64 // cumulative ms since file start
}

// Cell aggregates the strikes that share a slot, for export policy.
type Cell struct {
	Runes []rune
}

// Page is one sheet of paper.
type Page struct {
	Strikes   []StrikeRec
	Cells     map[CellKey]*Cell
	Tossed    bool
	Reinserts int

	savedCol   int
	savedYHalf int

	// Word-count cache (see WordCount): valid while len(Strikes) still
	// equals wcStrikes. Strikes are append-only and cells only change when
	// a strike lands, so the strike count is a complete dirtiness signal.
	wcStrikes int
	wcCount   int
}

// Result tells the caller what an event did, so a GUI can ding and lock.
type Result struct {
	Applied  bool // false when the machine refused (locked carriage, margin stop...)
	Bell     bool // ring it
	Locked   bool // carriage is at the right margin stop
	PageFull bool // baseline is on the last writable line
}

// Doc is the folded state of an event stream.
type Doc struct {
	Header format.Header

	Pages   []*Page
	Current int // index into Pages; -1 before the first NEW_SHEET

	// Live settings (mutable via SET_* events).
	Pitch       units.Pitch
	LineSpacing units.LineSpacing
	Margins     units.Margins

	// Carriage and platen.
	Col   int
	YHalf int

	// Machine memory.
	mach          *machine.Machine
	prevGlyph     rune
	ribbonStrikes int     // strikes since the current ribbon was installed
	ribbon        int     // 0-based index of the installed ribbon spool
	touch         float64 // writer's hand, 1.0 = medium (SET_TOUCH x100)
	disposition   float64 // writer's mood, 1.0 = composed (SET_DISPOSITION)
	sobriety      float64 // writer's state, 1.0 = sober (SET_SOBRIETY)
	condition     float64 // machine wear, 1.0 = factory (SET_CONDITION)
	clockMS       uint64
}

// New creates an empty document from a header. No pages exist until a
// NEW_SHEET event arrives.
func New(h format.Header) *Doc {
	return &Doc{
		Header:      h,
		Current:     -1,
		Pitch:       h.Pitch,
		LineSpacing: h.LineSpacing,
		Margins:     h.Margins,
		touch:       1.0,
		disposition: 1.0,
		sobriety:    1.0,
		condition:   1.0,
		mach:        machine.New(h.Seed),
	}
}

// Replay folds every event of a decoded file.
func Replay(f *format.File) *Doc {
	d := New(f.Header)
	for _, e := range f.Events {
		d.Apply(e)
	}
	return d
}

func (d *Doc) page() *Page {
	if d.Current < 0 || d.Current >= len(d.Pages) {
		return nil
	}
	return d.Pages[d.Current]
}

// MaxCol is the right margin stop: the first column the carriage locks at
// (AtLock is Col >= MaxCol), so it is NOT writable. Usable columns are
// 0..MaxCol-1, which also makes MaxCol the count of usable slots - the
// reading the importer's word-fits check relies on.
func (d *Doc) MaxCol() int {
	usable := units.PaperWidthMM - d.Margins.Left - d.Margins.Right
	n := max(int(usable/d.Pitch.SlotMM()), 1)
	return n - 1
}

// MaxYHalf is the lowest allowed baseline position.
func (d *Doc) MaxYHalf() int {
	usable := units.PaperHeightMM - d.Margins.Top - d.Margins.Bottom - units.BaseLineMM
	n := max(int(usable/(units.BaseLineMM/2)), 0)
	return n
}

// lineAdvanceHalves converts the live line spacing to half-line steps.
func (d *Doc) lineAdvanceHalves() int {
	return int(d.LineSpacing) / 5 // 10->2, 15->3, 20->4
}

// XMM returns the slot-centre x for a column under current settings.
func (d *Doc) XMM(col int) float64 {
	return d.Margins.Left + (float64(col)+0.5)*d.Pitch.SlotMM()
}

// YMM returns the baseline y for a half-line position.
func (d *Doc) YMM(yHalf int) float64 {
	return d.Margins.Top + units.BaseLineMM + float64(yHalf)*units.BaseLineMM/2
}

// AtLock reports whether the carriage is at the margin stop.
func (d *Doc) AtLock() bool { return d.Col >= d.MaxCol() }

// InBellZone reports whether the carriage is within BellSlots of the stop.
func (d *Doc) InBellZone() bool { return d.Col >= d.MaxCol()-units.BellSlots }

func (d *Doc) newSheet() {
	p := &Page{Cells: make(map[CellKey]*Cell)}
	d.Pages = append(d.Pages, p)
	d.Current = len(d.Pages) - 1
	d.Col = 0
	d.YHalf = 0
}

func (d *Doc) flipTo(idx int) bool {
	if idx < 0 || idx >= len(d.Pages) || idx == d.Current {
		return false
	}
	if p := d.page(); p != nil {
		p.savedCol, p.savedYHalf = d.Col, d.YHalf
	}
	d.Current = idx
	p := d.page()
	p.Reinserts++
	d.Col, d.YHalf = p.savedCol, p.savedYHalf
	return true
}

// advance moves the carriage one slot, reporting bell and lock.
func (d *Doc) advance(res *Result) {
	if d.AtLock() {
		res.Applied = false
		res.Locked = true
		return
	}
	before := d.InBellZone()
	d.Col++
	if !before && d.InBellZone() {
		res.Bell = true
	}
	res.Locked = d.AtLock()
}

// Apply folds one event into the document.
func (d *Doc) Apply(e format.Event) Result {
	res := Result{Applied: true}
	d.clockMS += e.DeltaMS

	switch e.Op {
	case format.OpStrike:
		p := d.page()
		if p == nil || d.AtLock() || p.Tossed {
			// No paper, locked carriage, or a scrunched sheet - a
			// ball of paper does not go back in the platen.
			res.Applied = false
			res.Locked = d.AtLock()
			return res
		}
		key := CellKey{YHalf: d.YHalf, Col: d.Col}
		cell := p.Cells[key]
		if cell == nil {
			cell = &Cell{}
			p.Cells[key] = cell
		}
		ctx := machine.Context{
			Glyph:         e.Rune,
			Prev:          d.prevGlyph,
			DeltaMS:       e.DeltaMS,
			Page:          d.Current,
			Row:           d.YHalf,
			Col:           d.Col,
			NthOnCell:     len(cell.Runes),
			RibbonStrikes: d.ribbonStrikes,
			Ribbon:        d.ribbon,
			Reinsert:      p.Reinserts,
			Touch:         d.touch,
			Disposition:   d.disposition,
			Sobriety:      d.sobriety,
			Condition:     d.condition,
		}
		rec := StrikeRec{
			Rune: e.Rune,
			Cell: key,
			XMM:  d.XMM(d.Col),
			YMM:  d.YMM(d.YHalf),
			App:  d.mach.StrikeFor(ctx),
			AtMS: d.clockMS,
		}
		p.Strikes = append(p.Strikes, rec)
		cell.Runes = append(cell.Runes, e.Rune)
		d.prevGlyph = e.Rune
		d.ribbonStrikes++
		d.advance(&res)
		res.Applied = true // the strike itself landed even if now locked

	case format.OpSpace:
		if d.page() == nil {
			res.Applied = false
			return res
		}
		d.advance(&res)

	case format.OpBack:
		if d.page() == nil || d.Col == 0 {
			res.Applied = false
			return res
		}
		d.Col--

	case format.OpCR:
		if d.page() == nil {
			res.Applied = false
			return res
		}
		d.Col = 0
		d.YHalf += d.lineAdvanceHalves()
		if d.YHalf > d.MaxYHalf() {
			d.YHalf = d.MaxYHalf()
			res.PageFull = true
			res.Bell = true
		}

	case format.OpLF:
		if d.page() == nil {
			res.Applied = false
			return res
		}
		d.YHalf += d.lineAdvanceHalves()
		if d.YHalf > d.MaxYHalf() {
			d.YHalf = d.MaxYHalf()
			res.PageFull = true
		}

	case format.OpHalfDown:
		if d.page() == nil {
			res.Applied = false
			return res
		}
		if d.YHalf < d.MaxYHalf() {
			d.YHalf++
		} else {
			res.Applied = false
			res.PageFull = true
		}

	case format.OpHalfUp:
		if d.page() == nil || d.YHalf == 0 {
			res.Applied = false
			return res
		}
		d.YHalf--

	case format.OpNewSheet:
		d.newSheet()

	case format.OpPagePrev:
		res.Applied = d.flipTo(d.Current - 1)

	case format.OpPageNext:
		res.Applied = d.flipTo(d.Current + 1)

	case format.OpToss:
		p := d.page()
		if p == nil {
			res.Applied = false
			return res
		}
		p.Tossed = true
		d.newSheet()

	case format.OpSetPitch:
		// Ignore an unknown pitch (keep the current one) rather than adopt a
		// degenerate slot width, matching the guards on the dials below.
		if p := units.Pitch(e.Value); p.Valid() {
			d.Pitch = p
		}

	case format.OpSetLinespace:
		if ls := units.LineSpacing(e.Value); ls.Valid() {
			d.LineSpacing = ls
		}

	case format.OpNewRibbon:
		// Maintenance, not typing: works with or without paper loaded.
		d.ribbonStrikes = 0
		d.ribbon++

	case format.OpSetTouch:
		if e.Value > 0 {
			d.touch = float64(e.Value) / 100
		}

	case format.OpSetDisposition:
		if e.Value > 0 {
			d.disposition = float64(e.Value) / 100
		}

	case format.OpSetSobriety:
		if e.Value > 0 {
			d.sobriety = float64(e.Value) / 100
		}

	case format.OpSetCondition:
		if e.Value > 0 {
			d.condition = float64(e.Value) / 100
		}

	case format.OpSetMargins:
		d.Margins = e.Margins
		if d.Col > d.MaxCol() {
			d.Col = d.MaxCol()
		}
		if d.YHalf > d.MaxYHalf() {
			d.YHalf = d.MaxYHalf()
		}

	case format.OpSession, format.OpCheck:
		// bookkeeping events; no machine state change

	default:
		res.Applied = false
	}
	return res
}

// PaperSeed returns the texture seed for a page index (see
// machine.PaperSeed).
func (d *Doc) PaperSeed(idx int) uint64 { return d.mach.PaperSeed(idx) }

// Condition is the machine's current wear multiplier (1.0 = factory), so
// the GUI's Bash/Fix steppers can nudge it relative to where it sits.
func (d *Doc) Condition() float64 { return d.condition }

// LivePages returns the pages that are not in the bin.
func (d *Doc) LivePages() []*Page {
	var out []*Page
	for _, p := range d.Pages {
		if !p.Tossed {
			out = append(out, p)
		}
	}
	return out
}
