// Package units holds the physical constants of the machine: paper,
// pitch, and line spacing. All model space is millimetres.
package units

const (
	// A4 paper, portrait.
	PaperWidthMM  = 210.0
	PaperHeightMM = 297.0

	InchMM = 25.4

	// Base vertical grid: 6 lines per inch, the near-universal
	// typewriter standard.
	BaseLineMM = InchMM / 6.0

	// CourierAdvanceEM is Courier Prime's monospace advance in em units.
	// The renderer, the DOCX exporter and the PDF exporter all size the
	// face from the pitch through this one number - three private copies
	// of 0.6 is how a future font swap misplaces glyphs in exactly one
	// exporter.
	CourierAdvanceEM = 0.6
)

// Pitch is horizontal character density in characters per inch.
type Pitch uint8

const (
	Pica  Pitch = 10 // 2.54 mm per slot
	Elite Pitch = 12 // 2.1167 mm per slot
)

// SlotMM is the horizontal advance per character slot.
func (p Pitch) SlotMM() float64 { return InchMM / float64(p) }

// Valid reports whether p is a pitch the machine offers. A zero or unknown
// pitch would make SlotMM degenerate (0 -> +Inf), so decoders reject it.
func (p Pitch) Valid() bool { return p == Pica || p == Elite }

// LineSpacing is stored as tenths (10, 15, 20 = single, one-and-a-half,
// double).
type LineSpacing uint8

const (
	Single  LineSpacing = 10
	OneHalf LineSpacing = 15
	Double  LineSpacing = 20
)

// AdvanceMM is the vertical advance of one carriage return.
func (ls LineSpacing) AdvanceMM() float64 {
	return BaseLineMM * float64(ls) / 10.0
}

// Valid reports whether ls is a line spacing the machine offers. A zero or
// unknown spacing would stop the carriage advancing on return, so decoders
// reject it.
func (ls LineSpacing) Valid() bool {
	return ls == Single || ls == OneHalf || ls == Double
}

// Margins in millimetres.
type Margins struct {
	Left, Right, Top, Bottom float64
}

// DefaultMargins are comfortable typewriter margins.
func DefaultMargins() Margins {
	return Margins{Left: 25, Right: 20, Top: 25, Bottom: 25}
}

// BellSlots is how many slots before the right margin the bell rings.
const BellSlots = 6
