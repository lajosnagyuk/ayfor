package machine

import "testing"

// BenchmarkFor is the single-strike cost of the personality model: this
// runs once per keystroke, so its allocations are GC pressure the owner
// feels as typing lag over a long session.
func BenchmarkFor(b *testing.B) {
	m := New(99)
	c := Context{
		Glyph: 'e', Prev: 'h', DeltaMS: 120,
		Page: 0, Row: 3, Col: 10, NthOnCell: 0, RibbonStrikes: 500,
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = m.StrikeFor(c)
	}
}

// BenchmarkForVariedContext exercises every branch (upper case shift,
// pair slack, jitter, ribbon wear, reinsertion) so the benchmark cannot be
// fooled by a single cached branch.
func BenchmarkForVariedContext(b *testing.B) {
	m := New(99)
	glyphs := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.,;:!?'\"-()")
	ctxs := make([]Context, 4096)
	for i := range ctxs {
		ctxs[i] = Context{
			Glyph:         glyphs[i%len(glyphs)],
			Prev:          glyphs[(i+13)%len(glyphs)],
			DeltaMS:       uint64(30 + (i*37)%3000),
			Page:          i % 30,
			Row:           i % 60,
			Col:           i % 80,
			NthOnCell:     i % 3,
			RibbonStrikes: i * 10,
			Reinsert:      i % 4,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.StrikeFor(ctxs[i%len(ctxs)])
	}
}

// BenchmarkForPage types roughly one full sheet (~3600 slots at Pica
// pitch on A4) through the model, the unit the owner actually reasons in
// ("60 pages in").
func BenchmarkForPage(b *testing.B) {
	m := New(99)
	const charsPerPage = 3648 // ~64 cols x 57 lines, default margins/Pica
	glyphs := []rune("abcdefghijklmnopqrstuvwxyz ")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var prev rune
		for n := range charsPerPage {
			g := glyphs[n%len(glyphs)]
			_ = m.StrikeFor(Context{
				Glyph: g, Prev: prev, DeltaMS: uint64(90 + n%300),
				Row: n / 64, Col: n % 64, RibbonStrikes: n,
			})
			prev = g
		}
	}
}
