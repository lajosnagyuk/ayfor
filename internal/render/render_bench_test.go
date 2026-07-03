package render

import (
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/importer"
	"github.com/lajosnagyuk/ayfor/internal/page"
)

// buildDocB is buildDoc (render_test.go) for testing.TB, so benchmarks can
// share the same corpus-building logic as the tests.
func buildDocB(tb testing.TB, text string) *page.Doc {
	tb.Helper()
	h := format.DefaultHeader(0xA4A4A4A4, 0)
	events := importer.Import(text, h, 1)
	d := page.New(h)
	for _, e := range events {
		d.Apply(e)
	}
	return d
}

// pageText is roughly one full sheet at Pica pitch on A4 (~64 cols x 57
// lines), the unit the owner reasons in ("60 pages in").
func pageText() string {
	line := "the quick brown fox jumps over the lazy dog and then types on\n"
	s := ""
	for range 57 {
		s += line
	}
	return s
}

// BenchmarkStamp is the per-keystroke cost: cmd/ayfor stamps exactly one
// new strike per keypress onto the already-rendered page bitmap (see
// ui.after), so this - not RenderPage - is what runs while typing.
func BenchmarkStamp(b *testing.B) {
	d := buildDocB(b, pageText())
	r, err := New(8)
	if err != nil {
		b.Fatal(err)
	}
	img := r.NewPage(d.PaperSeed(0))
	strikes := d.Pages[0].Strikes
	if len(strikes) == 0 {
		b.Fatal("no strikes to benchmark")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.Stamp(img, strikes[i%len(strikes)], d.Pitch); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRenderPage is the full-page cost: paid on load, page flip, and
// pitch change, not per keystroke - but must not be so slow it stalls the
// UI thread when the owner flips back through a long draft.
func BenchmarkRenderPage(b *testing.B) {
	d := buildDocB(b, pageText())
	r, err := New(8)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGlyphColdCache measures rasterizing every glyph the first time
// it is seen (cache miss) - the one-time cost paid at the start of a
// session or after a pitch change clears the cache.
func BenchmarkGlyphColdCache(b *testing.B) {
	glyphs := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.,;:!?'\"-()")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r, err := New(8)
		if err != nil {
			b.Fatal(err)
		}
		for _, g := range glyphs {
			if _, err := r.glyph(g, 10); err != nil {
				b.Fatal(err)
			}
		}
	}
}
