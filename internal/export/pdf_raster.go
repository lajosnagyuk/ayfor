package export

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"

	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/render"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

// PDFRaster produces an appearance-faithful PDF for package-backed
// typewriters by embedding pages from the canonical renderer. Legacy v1 keeps
// the existing vector-ish PDF function so old output remains unchanged.
func PDFRaster(d *page.Doc, renderer *render.Renderer) ([]byte, error) {
	var out bytes.Buffer
	if err := PDFRasterTo(&out, d, renderer); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

// PDFRasterTo streams an appearance-faithful PDF. Only the current rendered
// page and its compressed stream are resident; the complete PDF need not be.
func PDFRasterTo(dst io.Writer, d *page.Doc, renderer *render.Renderer) error {
	if renderer == nil {
		return fmt.Errorf("export: nil renderer")
	}
	live := d.LivePages()
	if len(live) == 0 {
		live = []*page.Page{{}}
	}
	out := &countingWriter{w: dst}
	if _, err := io.WriteString(out, "%PDF-1.4\n%\xE2\xE3\xCF\xD3\n"); err != nil {
		return err
	}
	offsets := []int64{0}
	writeObj := func(body []byte) error {
		offsets = append(offsets, out.n)
		if _, err := fmt.Fprintf(out, "%d 0 obj\n", len(offsets)-1); err != nil {
			return err
		}
		if _, err := out.Write(body); err != nil {
			return err
		}
		_, err := io.WriteString(out, "\nendobj\n")
		return err
	}
	writeStreamObj := func(dict string, data []byte) error {
		offsets = append(offsets, out.n)
		if _, err := fmt.Fprintf(out, "%d 0 obj\n<< %s /Length %d >>\nstream\n", len(offsets)-1, dict, len(data)); err != nil {
			return err
		}
		if _, err := out.Write(data); err != nil {
			return err
		}
		_, err := io.WriteString(out, "\nendstream\nendobj\n")
		return err
	}
	var kids bytes.Buffer
	for i := range live {
		if i > 0 {
			kids.WriteByte(' ')
		}
		fmt.Fprintf(&kids, "%d 0 R", 3+i*3)
	}
	if err := writeObj([]byte("<< /Type /Catalog /Pages 2 0 R >>")); err != nil {
		return err
	}
	if err := writeObj([]byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(live)))); err != nil {
		return err
	}
	pageW, pageH := units.PaperWidthMM*72/units.InchMM, units.PaperHeightMM*72/units.InchMM
	absIndex := make(map[*page.Page]int, len(d.Pages))
	for i, p := range d.Pages {
		absIndex[p] = i
	}
	for i, target := range live {
		abs, ok := absIndex[target]
		if !ok && len(d.Pages) > 0 {
			return fmt.Errorf("export: live page is not in document")
		}
		img, err := renderer.RenderPage(target, d.Pitch, d.PaperSeed(max(abs, 0)))
		if err != nil {
			return err
		}
		rgb := make([]byte, img.Bounds().Dx()*img.Bounds().Dy()*3)
		for si, di := 0, 0; si < len(img.Pix); si, di = si+4, di+3 {
			rgb[di], rgb[di+1], rgb[di+2] = img.Pix[si], img.Pix[si+1], img.Pix[si+2]
		}
		var compressed bytes.Buffer
		zw, err := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
		if err != nil {
			return err
		}
		if _, err := zw.Write(rgb); err != nil {
			_ = zw.Close()
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		pageObj, contentObj, imageObj := 3+i*3, 4+i*3, 5+i*3
		_ = pageObj
		if err := writeObj([]byte(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.3f %.3f] /Resources << /XObject << /Im%d %d 0 R >> >> /Contents %d 0 R >>", pageW, pageH, i, imageObj, contentObj))); err != nil {
			return err
		}
		content := []byte(fmt.Sprintf("q %.3f 0 0 %.3f 0 0 cm /Im%d Do Q\n", pageW, pageH, i))
		if err := writeStreamObj("", content); err != nil {
			return err
		}
		dict := fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode", img.Bounds().Dx(), img.Bounds().Dy())
		if err := writeStreamObj(dict, compressed.Bytes()); err != nil {
			return err
		}
	}
	xref := out.n
	if _, err := fmt.Fprintf(out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)); err != nil {
		return err
	}
	for _, off := range offsets[1:] {
		if _, err := fmt.Fprintf(out, "%010d 00000 n \n", off); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return err
}
