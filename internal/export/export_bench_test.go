package export

import (
	"strings"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/importer"
	"github.com/lajosnagyuk/ayfor/internal/page"
)

// benchDoc builds a multi-page document (~10 sheets) to exercise export at
// a scale bigger than a single page without making the benchmark suite
// itself slow; internal/page and internal/importer benchmarks already
// cover full 60-page throughput of the folding step these exporters read.
func benchDoc(b *testing.B) *page.Doc {
	b.Helper()
	para := "The quick brown fox jumps over the lazy dog, again and again. "
	var sb strings.Builder
	for i := range 550 {
		sb.WriteString(para)
		if i%8 == 7 {
			sb.WriteString("\n\n")
		}
	}
	h := format.DefaultHeader(7, 0)
	d := page.New(h)
	for _, e := range importer.Import(sb.String(), h, 1) {
		d.Apply(e)
	}
	if len(d.Pages) < 5 {
		b.Fatalf("corpus too small: only %d pages", len(d.Pages))
	}
	return d
}

func BenchmarkMarkdown(b *testing.B) {
	d := benchDoc(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Markdown(d)
	}
}

func BenchmarkDOCX(b *testing.B) {
	d := benchDoc(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DOCX(d); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPDF(b *testing.B) {
	d := benchDoc(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := PDF(d); err != nil {
			b.Fatal(err)
		}
	}
}
