package sound

import (
	"encoding/binary"
	"sync"
	"testing"
)

func TestReadAndVoiceCapCanRunConcurrently(t *testing.T) {
	p := newTestPlayer(t)
	clip := constPCM(4096, 1000)
	for range maxVoices {
		p.PlayPCM(clip)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]byte, 256)
		for range 1000 {
			_, _ = p.Read(buf)
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			p.PlayPCM(clip)
		}
	}()
	wg.Wait()
}

// newTestPlayer builds a Player without touching the audio device: Strike
// and Read are pure Go over the voice list, so they're testable headless.
func newTestPlayer(t *testing.T) *Player {
	t.Helper()
	return &Player{bank: NewBank()}
}

// pcm16 encodes int16 samples as the little-endian bytes a voice holds.
func pcm16(samples ...int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(s))
	}
	return out
}

// constPCM returns n samples all at value v.
func constPCM(n int, v int16) []byte {
	s := make([]int16, n)
	for i := range s {
		s[i] = v
	}
	return pcm16(s...)
}

func TestVoicePoolCapped(t *testing.T) {
	p := newTestPlayer(t)
	for i := range maxVoices + 10 {
		p.Strike(0, 0, i, i, 1.0)
	}
	if len(p.voices) != maxVoices {
		t.Fatalf("got %d voices in flight, want capped at %d", len(p.voices), maxVoices)
	}
}

func peakAbs(buf []byte) int {
	peak := 0
	for i := 0; i+1 < len(buf); i += 2 {
		s := int(int16(binary.LittleEndian.Uint16(buf[i:])))
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	return peak
}

// TestOverlapStaysBelowClipping pins the headroom fix: overlapping voices
// used to sum with no limiter (oto's mux does `buf[i] += v * volume`
// unconditionally), so 3 identical loud voices would clip. With 1/sqrt(N)
// compensation the same 3 voices should mix louder than one alone but
// stay inside int16 range. All three strikes share one position so they
// are guaranteed the SAME base sample summing exactly in phase - the
// worst case, and the only setup where "3x the single peak would clip"
// actually proves the scaling is doing the work.
func TestOverlapStaysBelowClipping(t *testing.T) {
	single := NewBank().Pick(0, 0, 0, 0, 0.0) // quietest ink: headroom to observe mixing without the hard clamp
	singlePeak := peakAbs(single)
	if singlePeak == 0 {
		t.Fatal("single voice has no energy, test setup is broken")
	}
	if naive := 3 * singlePeak; naive <= 32767 {
		t.Fatalf("test setup doesn't exercise the fix: 3x single peak (%d) must exceed int16 range to prove scaling matters", naive)
	}

	p := newTestPlayer(t)
	p.Strike(0, 0, 0, 0, 0.0)
	p.Strike(0, 0, 0, 0, 0.0)
	p.Strike(0, 0, 0, 0, 0.0)

	buf := make([]byte, len(single)) // covers the whole clip in one Read
	if _, err := p.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}

	mixedPeak := peakAbs(buf)
	if mixedPeak >= 32767 {
		t.Fatalf("mix clipped: peak=%d, want < 32767 (headroom scaling should have prevented this)", mixedPeak)
	}
	if mixedPeak <= singlePeak {
		t.Fatalf("mix isn't louder than a single voice (peak=%d vs single=%d) - are the strikes actually being summed?", mixedPeak, singlePeak)
	}
}

// TestReadSurvivesMaxVoices exercises the worst case the pool allows -
// maxVoices identical loudest-ink strikes, all in flight at once - and
// confirms Read produces a valid, non-silent mix. (The int32 accumulator
// cannot wrap by construction: 32 voices x 32767 is about 1.05M, far
// inside int32 range; the clamp exists for the scaled float sum.)
func TestReadSurvivesMaxVoices(t *testing.T) {
	p := newTestPlayer(t)
	for i := range maxVoices {
		p.Strike(0, 0, i, i, 1.2) // loudest ink, maximum plausible overlap
	}
	buf := make([]byte, 4096)
	if _, err := p.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if peakAbs(buf) == 0 {
		t.Fatal("expected non-silent output with maxVoices strikes in flight")
	}
}

func TestReadWithNoVoicesIsSilent(t *testing.T) {
	p := newTestPlayer(t)
	buf := make([]byte, 256)
	for i := range buf {
		buf[i] = 0xAA // poison the buffer so a no-op Read would be caught
	}
	n, err := p.Read(buf)
	if err != nil || n != len(buf) {
		t.Fatalf("Read: n=%d err=%v, want n=%d err=nil", n, err, len(buf))
	}
	for _, b := range buf {
		if b != 0 {
			t.Fatal("expected silence with no voices in flight")
		}
	}
}

// TestCrossBufferVoiceContinuity pins the pos-resume bookkeeping: a voice
// spanning several Reads must pick up exactly where the previous Read
// left off, with no repeated, skipped or zeroed samples at the seam.
// (This coverage was missing entirely while the handover claimed it was
// merely a skipped test.)
func TestCrossBufferVoiceContinuity(t *testing.T) {
	src := make([]int16, 20)
	for i := range src {
		src[i] = int16(100 + i)
	}
	p := newTestPlayer(t)
	p.PlayPCM(pcm16(src...))

	buf := make([]byte, 20) // 10 samples per Read
	for chunk := range 2 {
		if _, err := p.Read(buf); err != nil {
			t.Fatalf("Read: %v", err)
		}
		for i := range 10 {
			got := int16(binary.LittleEndian.Uint16(buf[i*2:]))
			want := src[chunk*10+i]
			if got != want {
				t.Fatalf("chunk %d sample %d = %d, want %d (voice did not resume at its offset)", chunk, i, got, want)
			}
		}
	}

	// The voice was consumed by the second Read exactly; it must be gone
	// from the pool NOW, not linger a Read longer occupying a cap slot.
	if len(p.voices) != 0 {
		t.Fatalf("%d spent voices still occupy pool slots", len(p.voices))
	}

	// And the next Read is silence, not a replay of the tail.
	if _, err := p.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if peakAbs(buf) != 0 {
		t.Fatal("expected silence after the voice fully drained")
	}
}

// TestHeadroomReleasesSmoothy pins the gain-step fix: when a voice ends,
// the 1/sqrt(N) headroom scale on the surviving voices must ease back up
// over a few ms, not step up +3dB at the next buffer boundary (an audible
// pump in the middle of a decay tail, with no masking transient). The
// down direction stays instant on purpose - it coincides with the new
// strike's own onset, which masks it, and easing down would clip.
func TestHeadroomReleasesSmoothly(t *testing.T) {
	const level = 8000
	p := newTestPlayer(t)
	p.PlayPCM(constPCM(200, level))  // ends during the first Read
	p.PlayPCM(constPCM(1400, level)) // survives it

	buf := make([]byte, 800) // 400 samples
	if _, err := p.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Second Read: one voice left, target scale 1.0, but the previous
	// Read ran at 1/sqrt(2). The first output sample must continue near
	// the old scaled level and rise smoothly toward the full level.
	if _, err := p.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	first := int(int16(binary.LittleEndian.Uint16(buf[0:])))
	if first > 6000 {
		t.Fatalf("first sample after the voice count dropped = %d; the headroom scale stepped instead of ramping (steady level %d, previous scaled level ~5657)", first, level)
	}
	prev := first
	for i := 1; i < 400; i++ {
		s := int(int16(binary.LittleEndian.Uint16(buf[i*2:])))
		if s < prev {
			t.Fatalf("sample %d = %d dropped below %d during the release ramp", i, s, prev)
		}
		if s-prev > 20 {
			t.Fatalf("sample %d jumped by %d during the release ramp; want a per-sample glide", i, s-prev)
		}
		prev = s
	}
	if prev <= first {
		t.Fatalf("scale never rose across the buffer (first=%d last=%d)", first, prev)
	}
}
