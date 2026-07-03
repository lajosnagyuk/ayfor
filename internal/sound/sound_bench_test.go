package sound

import "testing"

// BenchmarkPick quantifies the whole per-strike cost: base-sample hash,
// resample (pitch shift from ink), gain, and PCM16 encode, in a single
// output allocation (~12 KB for a 140ms clip at 44.1kHz). The allocation
// is deliberate, not an oversight: overlapping strikes play concurrently
// from independent voices, so each strike needs its own buffer, and oto's
// io.Reader-driven Player gives no "playback fully drained" signal a pool
// could key on - only "the bytes were consumed by Read", which happens
// before output finishes. A pool built on that would trade a bounded,
// harmless allocation for a rare but real audio-corruption bug. The cost
// is opt-in, per-keystroke, and far under a human's fastest interval.
func BenchmarkPick(b *testing.B) {
	bank := NewBank()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		bank.Pick(0, i%50, i%80, i, 0.8)
	}
}
