package session

import (
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkStrike measures the real, disk-durable cost of one keystroke:
// every strike is appended to disk before it's applied (session.append),
// so this - not the in-memory model - is the true per-keystroke latency
// floor the owner would feel as lag.
func BenchmarkStrike(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.strike")
	s, err := New(path, 1, fakeClock(time.UnixMilli(0), time.Millisecond))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()

	glyphs := []rune("abcdefghijklmnopqrstuvwxyz")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Strike(glyphs[i%len(glyphs)]); err != nil {
			b.Fatal(err)
		}
	}
}
