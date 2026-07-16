package export

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

// DOCX renders the live pages as a minimal Office Open XML document:
// one paragraph per typewriter line in a monospace face sized to the
// pitch, bold and strikethrough per the export policy, real page breaks
// between sheets. Hand-rolled on purpose — the subset we need is tiny.
func DOCX(d *page.Doc) ([]byte, error) {
	emMM := d.Pitch.SlotMM() / units.CourierAdvanceEM
	return docx(d, "Courier Prime", "Courier New", emMM)
}

// DOCXWithProfile produces the semantic DOCX representation using the
// package's declared family and physical em size. Office may substitute the
// face when it is not installed; PNG/PDF remain the appearance-faithful paths.
func DOCXWithProfile(d *page.Doc, profile *typewriter.Profile) ([]byte, error) {
	if profile == nil {
		return nil, fmt.Errorf("export: nil typewriter profile")
	}
	return docx(d, profile.Manifest.Typeface.Family, profile.Manifest.Typeface.Family, float64(profile.Manifest.Typeface.EMMicrometres)/1000)
}

func docx(d *page.Doc, family, complexFamily string, emMM float64) ([]byte, error) {
	var doc bytes.Buffer
	doc.WriteString(xml.Header)
	doc.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` + "\n<w:body>\n")

	// Font size in half-points from the em implied by the pitch.
	halfPts := int(emMM*72/units.InchMM*2 + 0.5)
	family = xmlAttribute(family)
	complexFamily = xmlAttribute(complexFamily)

	pages := d.LivePages()
	for pi, p := range pages {
		prevY := -1
		for _, line := range Lines(p) {
			for i := 0; i < blankLinesBetween(prevY, line.YHalf, d); i++ {
				doc.WriteString(`<w:p><w:pPr><w:spacing w:after="0"/></w:pPr></w:p>` + "\n")
			}
			prevY = line.YHalf
			doc.WriteString(`<w:p><w:pPr><w:spacing w:after="0"/></w:pPr>`)
			writeDocxRuns(&doc, line.Cells, halfPts, family, complexFamily)
			doc.WriteString("</w:p>\n")
		}
		if pi < len(pages)-1 {
			doc.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>` + "\n")
		}
	}
	// Section properties: A4 page and the document's own margins, in twips
	// (1 mm = 1440/25.4). Without this Word falls back to its default page
	// size (often Letter) and rewraps the fixed-width monospace lines.
	twip := func(mm float64) int { return int(mm*1440/units.InchMM + 0.5) }
	fmt.Fprintf(&doc,
		`<w:sectPr><w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="%d" w:right="%d" w:bottom="%d" w:left="%d" w:header="0" w:footer="0" w:gutter="0"/></w:sectPr>`+"\n",
		twip(units.PaperWidthMM), twip(units.PaperHeightMM),
		twip(d.Margins.Top), twip(d.Margins.Right), twip(d.Margins.Bottom), twip(d.Margins.Left))
	doc.WriteString("</w:body>\n</w:document>\n")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// A fixed slice, not a map: zip entry order must be deterministic (same
	// document = byte-identical .docx, the same promise every render path
	// keeps), and OPC consumers prefer [Content_Types].xml first anyway.
	files := []struct{ name, content string }{
		{"[Content_Types].xml", xml.Header + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`},
		{"_rels/.rels", xml.Header + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`</Relationships>`},
		{"word/document.xml", doc.String()},
	}
	for _, f := range files {
		name, content := f.name, f.content
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeDocxRuns(doc *bytes.Buffer, cells []StyledCell, halfPts int, family, complexFamily string) {
	end := len(cells)
	for end > 0 && cells[end-1].Rune == ' ' {
		end--
	}
	cells = cells[:end]
	i := 0
	for i < len(cells) {
		style := cells[i].Style
		j := i
		for j < len(cells) && sameRun(cells[j], style) {
			j++
		}
		var props strings.Builder
		fmt.Fprintf(&props, `<w:rFonts w:ascii="%s" w:hAnsi="%s" w:cs="%s"/>`, family, family, complexFamily)
		fmt.Fprintf(&props, `<w:sz w:val="%d"/><w:szCs w:val="%d"/>`, halfPts, halfPts)
		if style == Bold {
			props.WriteString(`<w:b/>`)
		}
		if style == Struck {
			props.WriteString(`<w:strike/>`)
		}
		var text bytes.Buffer
		xml.EscapeText(&text, []byte(runString(cells[i:j])))
		fmt.Fprintf(doc, `<w:r><w:rPr>%s</w:rPr><w:t xml:space="preserve">%s</w:t></w:r>`,
			props.String(), text.String())
		i = j
	}
	// A blank line is preserved by the caller's empty <w:p>; there is
	// nothing to add here when cells is empty.
}

func xmlAttribute(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
