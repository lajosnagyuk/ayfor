package importer

import (
	"strings"
	"testing"
)

// bookText is roughly 60 pages of prose (~220k characters), the scale a
// long manuscript import (File > Load a .txt) has to handle without
// stalling the UI.
func bookText() string {
	para := "The quick brown fox jumps over the lazy dog, again and again, " +
		"until the ribbon wears thin and the platen needs a rest. "
	var b strings.Builder
	for i := range 3200 {
		b.WriteString(para)
		if i%6 == 5 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func BenchmarkImportBook(b *testing.B) {
	text := bookText()
	h := header()
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events := Import(text, h, 1)
		if len(events) == 0 {
			b.Fatal("no events produced")
		}
	}
}
