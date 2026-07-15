package render

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/importer"
	"github.com/lajosnagyuk/ayfor/internal/machine"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

func TestRendererRejectsUnsafeScale(t *testing.T) {
	for _, scale := range []float64{0, -1, math.NaN(), math.Inf(1), maxRenderScale + 1} {
		if _, err := New(scale); err == nil {
			t.Fatalf("accepted unsafe scale %v", scale)
		}
	}
}

func TestGlyphCacheDeduplicatesMissingRunesAndStaysBounded(t *testing.T) {
	r, err := New(4)
	if err != nil {
		t.Fatal(err)
	}
	for ru := rune(0x10000); ru < 0x12000; ru++ {
		if _, err := r.glyph(ru, units.Pica); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.cache) > maxGlyphCacheEntries {
		t.Fatalf("cache has %d entries, limit %d", len(r.cache), maxGlyphCacheEntries)
	}
	if r.cachePixels > maxGlyphCachePixels {
		t.Fatalf("cache holds %d pixels, limit %d", r.cachePixels, maxGlyphCachePixels)
	}
}

func buildDoc(t *testing.T, text string) *page.Doc {
	t.Helper()
	h := format.DefaultHeader(0xA4A4A4A4, 0)
	events := importer.Import(text, h, 1)
	d := page.New(h)
	for _, e := range events {
		d.Apply(e)
	}
	return d
}

func TestRenderProducesInk(t *testing.T) {
	d := buildDoc(t, "The quick brown fox jumps over the lazy dog.")
	r, err := New(5.67)
	if err != nil {
		t.Fatal(err)
	}
	img, err := r.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if err != nil {
		t.Fatal(err)
	}
	inked := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] < 0x80 {
			inked++
		}
	}
	if inked < 500 {
		t.Fatalf("only %d dark pixels; the page looks blank", inked)
	}
}

func TestRenderDeterministic(t *testing.T) {
	d := buildDoc(t, "determinism")
	r1, _ := New(5.67)
	r2, _ := New(5.67)
	a, err := r1.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := r2.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("same doc must render byte-identically")
	}
}

// TestRenderSamplePNG writes an eyeball sample when AYFOR_SAMPLE_DIR is
// set (used during development, skipped in CI).
func TestRenderSamplePNG(t *testing.T) {
	dir := os.Getenv("AYFOR_SAMPLE_DIR")
	if dir == "" {
		t.Skip("set AYFOR_SAMPLE_DIR to write a sample page")
	}
	text := "Dear reader,\n\n" +
		"This page was not typed by hands, and its perfectly even\n" +
		"rhythm will say so on replay. But every hammer still has its\n" +
		"own alignment, every strike its own ink, and the machine its\n" +
		"own character. AaBbCcDdEeFfGg 0123456789 !?&()\n\n" +
		"The quick brown fox jumps over the lazy dog.\n" +
		"THE QUICK BROWN FOX JUMPS OVER THE LAZY DOG.\n"
	d := buildDoc(t, text)
	r, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	img, err := r.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "sample.png")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}

// countDeviating counts pixels whose red channel deviates from the flat
// paper colour, and reports the largest deviation seen.
func countDeviating(pix []byte) (n, maxDev int) {
	for i := 0; i < len(pix); i += 4 {
		d := int(pix[i]) - int(paper.R)
		if d < 0 {
			d = -d
		}
		if d > 0 {
			n++
		}
		if d > maxDev {
			maxDev = d
		}
	}
	return
}

// TestPaperGrainSubtleAndDeterministic pins the 50 gsm brief: the grain is
// there (most pixels deviate from flat), it is gentle (a few RGB levels at
// most), and the same seed always produces the same sheet.
func TestPaperGrainSubtleAndDeterministic(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	a := r.NewPage(7)
	n, maxDev := countDeviating(a.Pix)
	if n < len(a.Pix)/4/2 {
		t.Fatalf("only %d of %d pixels carry grain - page is too flat", n, len(a.Pix)/4)
	}
	if maxDev > 8 {
		t.Fatalf("grain deviates up to %d levels - this is parchment, not 50 gsm", maxDev)
	}
	b := r.NewPage(7)
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("same paper seed must produce the identical sheet")
	}
}

// TestPaperGrainVariesPerSheet pins "generated and not always the same".
func TestPaperGrainVariesPerSheet(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	a := r.NewPage(7)
	b := r.NewPage(8)
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("different paper seeds must grain differently")
	}
}

// stampOne renders a single synthetic strike and returns the bitmap.
func stampOne(t *testing.T, app machine.Strike) *image.RGBA {
	t.Helper()
	r, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	img := r.NewPage(1)
	rec := page.StrikeRec{Rune: 'o', XMM: 100, YMM: 100, App: app}
	if err := r.Stamp(img, rec, 10); err != nil {
		t.Fatal(err)
	}
	return img
}

// inkMass sums how much darker than paper each pixel is: a proxy for how
// much ink landed.
func inkMass(img *image.RGBA) int {
	mass := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if d := int(paper.R) - int(img.Pix[i]); d > 0 {
			mass += d
		}
	}
	return mass
}

// TestDieFoulingFattens pins Fill: a gunked die must put down more ink
// area than a clean one, everything else equal.
func TestDieFoulingFattens(t *testing.T) {
	base := machine.Strike{Ink: 1.0}
	fouled := machine.Strike{Ink: 1.0, Fill: 0.3}
	clean := inkMass(stampOne(t, base))
	fat := inkMass(stampOne(t, fouled))
	if !(fat > clean) {
		t.Fatalf("fouled die must print fatter: clean=%d fouled=%d", clean, fat)
	}
}

// TestInkTextureVariesByTex pins the speckle: two strikes identical except
// for their texture seed must not lay identical ink.
func TestInkTextureVariesByTex(t *testing.T) {
	a := stampOne(t, machine.Strike{Ink: 0.8, Tex: 1})
	b := stampOne(t, machine.Strike{Ink: 0.8, Tex: 2})
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("different texture seeds must speckle differently")
	}
}

// TestReliefIsDirectional pins the deboss: a stamped glyph must both
// lighten some pixels above paper level (the lit flank) and the effect
// must vanish when ink is zero would be meaningless, so instead check
// lit pixels exist at all - the flat pre-texture renderer could never
// produce a pixel lighter than the sheet.
func TestReliefIsDirectional(t *testing.T) {
	img := stampOne(t, machine.Strike{Ink: 1.2})
	base := New8Page(t)
	lit := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > base.Pix[i] {
			lit++
		}
	}
	if lit == 0 {
		t.Fatal("no pixel caught the light - deboss relief is not being applied")
	}
}

// TestStampFlatCompositesOverTransparency pins the source-over fix: a
// glyph flat-stamped onto a TRANSPARENT overlay, composited over the
// paper, must match the same glyph flat-stamped straight onto the paper.
// Before the fix every partial-coverage pixel was forced opaque, so the
// overlay carried a dark fringe (and near-black box pixels) that did not
// exist on the page - the "doubled, overstruck" chrome artifact.
func TestStampFlatCompositesOverTransparency(t *testing.T) {
	r, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	rec := page.StrikeRec{Rune: 'o', XMM: 100, YMM: 100, App: machine.Strike{Ink: 1.0}}

	direct := r.NewPage(1)
	if err := r.StampFlat(direct, rec, 10); err != nil {
		t.Fatal(err)
	}

	overlay := image.NewRGBA(direct.Bounds())
	if err := r.StampFlat(overlay, rec, 10); err != nil {
		t.Fatal(err)
	}

	semi := 0
	for i := 3; i < len(overlay.Pix); i += 4 {
		if a := overlay.Pix[i]; a > 0 && a < 255 {
			semi++
		}
	}
	if semi == 0 {
		t.Fatal("no semi-transparent edge pixels on the overlay; alpha is being forced")
	}

	// Composite the overlay over an identical fresh sheet (premultiplied
	// source-over, the same operation the display does) and compare.
	composited := r.NewPage(1)
	for i := 0; i < len(overlay.Pix); i += 4 {
		a := float64(overlay.Pix[i+3]) / 255
		for c := range 4 {
			composited.Pix[i+c] = uint8(float64(overlay.Pix[i+c]) + float64(composited.Pix[i+c])*(1-a) + 0.5)
		}
	}
	for i := range direct.Pix {
		d := int(direct.Pix[i]) - int(composited.Pix[i])
		if d < -2 || d > 2 {
			t.Fatalf("composited overlay differs from direct stamp at byte %d: %d vs %d", i, composited.Pix[i], direct.Pix[i])
		}
	}
}

// New8Page returns the untouched sheet stampOne stamps on, for comparison.
func New8Page(t *testing.T) *image.RGBA {
	t.Helper()
	r, err := New(8)
	if err != nil {
		t.Fatal(err)
	}
	return r.NewPage(1)
}
