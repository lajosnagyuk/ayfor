package export

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

// opticalCentreEM is where above the baseline (in em units) the tilt
// pivots: roughly the x-height midpoint. It pairs with the renderer's
// rotation pivot - if the two drift apart, PDF glyphs tilt about a
// different point than the bitmap's and the exports stop matching.
const opticalCentreEM = 0.28

// PDF renders the live pages exactly: every strike is placed with its own
// text matrix (offset plus tilt, pivoting about the glyph centre) and its
// own ink level as a fill gray. Overstrikes overlap like ink.
//
// v1 limitations, by design: the built-in core font Courier stands in for
// Courier Prime (both are 0.6 em monospace, so positions are identical);
// runes outside Latin-1 print as '?'; the per-glyph ink gradient is
// approximated by the flat ink level. Embedding the real face is a v2
// task, noted in the handover.
func PDF(d *page.Doc) ([]byte, error) {
	const k = 72.0 / units.InchMM // mm -> pt
	pageW := units.PaperWidthMM * k
	pageH := units.PaperHeightMM * k

	emPt := d.Pitch.SlotMM() / units.CourierAdvanceEM * k // font size in points
	advPt := emPt * units.CourierAdvanceEM

	pages := d.LivePages()
	if len(pages) == 0 {
		pages = []*page.Page{{}}
	}

	// Content stream per page.
	streams := make([][]byte, len(pages))
	for i, p := range pages {
		var s bytes.Buffer
		fmt.Fprintf(&s, "BT\n/F1 %.3f Tf\n", emPt)
		lastGray := -1.0
		// Scratch buffer for the per-strike Tm/g lines: strconv.AppendFloat
		// into a stack array instead of fmt.Fprintf's %f verbs, which box
		// every float64 argument onto the heap. This loop runs once per
		// strike on the page (thousands per sheet), so the boxing was the
		// dominant allocation cost of a PDF export.
		var scratch [96]byte
		for _, rec := range p.Strikes {
			b := latin1(rec.Rune) // unmappable runes print '?', never skipped
			gray := grayFromInk(rec.App.Ink)
			if math.Abs(gray-lastGray) > 0.004 {
				line := strconv.AppendFloat(scratch[:0], gray, 'f', 2, 64)
				line = append(line, " g\n"...)
				s.Write(line)
				lastGray = gray
			}
			// Baseline-left origin in PDF space (y up).
			ox := (rec.XMM+rec.App.DX)*k - advPt/2
			oy := pageH - (rec.YMM+rec.App.DY)*k
			// Rotate about the optical glyph centre, sign flipped for
			// the y-up coordinate system.
			theta := -rec.App.TiltDeg * math.Pi / 180
			sin, cos := math.Sincos(theta)
			pcx := advPt / 2
			pcy := emPt * opticalCentreEM
			e := ox + pcx - cos*pcx + sin*pcy
			f := oy + pcy - sin*pcx - cos*pcy
			line := scratch[:0]
			line = strconv.AppendFloat(line, cos, 'f', 4, 64)
			line = append(line, ' ')
			line = strconv.AppendFloat(line, sin, 'f', 4, 64)
			line = append(line, ' ')
			line = strconv.AppendFloat(line, -sin, 'f', 4, 64)
			line = append(line, ' ')
			line = strconv.AppendFloat(line, cos, 'f', 4, 64)
			line = append(line, ' ')
			line = strconv.AppendFloat(line, e, 'f', 3, 64)
			line = append(line, ' ')
			line = strconv.AppendFloat(line, f, 'f', 3, 64)
			line = append(line, " Tm\n"...)
			s.Write(line)
			s.WriteString("(")
			switch b {
			case '(', ')', '\\':
				s.WriteByte('\\')
				s.WriteByte(b)
			default:
				s.WriteByte(b)
			}
			s.WriteString(") Tj\n")
		}
		s.WriteString("ET\n")
		streams[i] = s.Bytes()
	}

	// Assemble objects: 1 catalog, 2 pages, 3 font, then per page a page
	// object and a content object.
	var out bytes.Buffer
	offsets := []int{0} // object 0 is the free head
	writeObj := func(body string) {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offsets)-1, body)
	}

	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")

	var kids strings.Builder
	for i := range pages {
		if i > 0 {
			kids.WriteString(" ")
		}
		fmt.Fprintf(&kids, "%d 0 R", 4+2*i)
	}
	writeObj("<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(pages)))
	writeObj("<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>")
	for i := range pages {
		writeObj(fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>",
			pageW, pageH, 5+2*i))
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n<< /Length %d >>\nstream\n", len(offsets)-1, len(streams[i]))
		out.Write(streams[i])
		out.WriteString("endstream\nendobj\n")
	}

	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, off := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return out.Bytes(), nil
}

// grayFromInk maps the model's ink multiplier to a PDF fill gray.
// Heavier ink is darker; a worn ribbon reads visibly lighter.
func grayFromInk(ink float64) float64 {
	g := 1.05 - ink
	if g < 0 {
		g = 0
	}
	if g > 0.75 {
		g = 0.75
	}
	return g
}

// latin1 maps a rune to a WinAnsi-compatible byte, '?' if unmappable.
func latin1(r rune) byte {
	switch {
	case r >= 0x20 && r <= 0x7E:
		return byte(r)
	case r >= 0xA0 && r <= 0xFF:
		return byte(r)
	default:
		return '?'
	}
}
