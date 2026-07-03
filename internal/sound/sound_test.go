package sound

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"testing"
)

func TestBankBasesDiffer(t *testing.T) {
	b := NewBank()
	if len(b.bases) < 2 {
		t.Fatalf("got %d base samples, want at least 2", len(b.bases))
	}
	for i := 1; i < len(b.bases); i++ {
		if slices.Equal(b.bases[i], b.bases[0]) {
			t.Fatalf("base %d identical to base 0", i)
		}
	}
}

func TestBasesHaveEnergy(t *testing.T) {
	b := NewBank()
	for i, base := range b.bases {
		peak := 0.0
		for _, v := range base {
			if math.Abs(v) > peak {
				peak = math.Abs(v)
			}
		}
		if peak < 0.1 {
			t.Fatalf("base %d is nearly silent, peak %f", i, peak)
		}
	}
}

func TestPickDeterministic(t *testing.T) {
	a := NewBank()
	b := NewBank()
	if !bytes.Equal(a.Pick(1, 2, 3, 0, 1.0), b.Pick(1, 2, 3, 0, 1.0)) {
		t.Fatal("same strike must always sound the same")
	}
}

func TestPickVariesByPosition(t *testing.T) {
	b := NewBank()
	seen := map[string]bool{}
	for col := range 40 {
		seen[string(b.Pick(0, 0, col, 0, 1.0))] = true
	}
	if len(seen) < 4 {
		t.Fatalf("only %d distinct variants over 40 columns - not enough variety", len(seen))
	}
}

// pcmPeak returns the loudest absolute sample in a PCM16 clip.
func pcmPeak(pcm []byte) int {
	peak := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	return peak
}

func TestInkControlsGain(t *testing.T) {
	b := NewBank()
	heavy := pcmPeak(b.Pick(0, 0, 0, 0, 1.15))
	light := pcmPeak(b.Pick(0, 0, 0, 0, 0.75))
	if heavy <= light {
		t.Fatalf("heavy strike must be louder: heavy peak=%d light peak=%d", heavy, light)
	}
}

// TestInkControlsPitch pins the modulation the owner asked for: heavier
// strikes pitch down (rate < 1, so more output samples for the same base
// sample), lighter strikes pitch up, both deterministically from ink -
// not an independent random dimension from gain.
func TestInkControlsPitch(t *testing.T) {
	b := NewBank()
	heavy := b.Pick(0, 0, 0, 0, 1.15)
	light := b.Pick(0, 0, 0, 0, 0.75)
	if len(heavy) <= len(light) {
		t.Fatalf("heavy strike must pitch down relative to light (longer clip): heavy=%d bytes light=%d bytes", len(heavy), len(light))
	}
}

// TestHostileInkStaysNominal pins the ink guards: a negative ink must not
// produce a negative (phase-inverted) gain or an out-of-spread rate, and
// NaN must not poison the output. Ink comes from strike records, so this
// only matters if an invariant breaks elsewhere - but audio is the worst
// place to discover that.
func TestHostileInkStaysNominal(t *testing.T) {
	b := NewBank()
	nominal := b.Pick(0, 0, 0, 0, 1.0)
	if got := b.Pick(0, 0, 0, 0, math.NaN()); !bytes.Equal(got, nominal) {
		t.Fatal("NaN ink must play the nominal strike")
	}
	floor := b.Pick(0, 0, 0, 0, 0.0)
	negative := b.Pick(0, 0, 0, 0, -5.0)
	if !bytes.Equal(negative, floor) {
		t.Fatal("negative ink must clamp to the lightest strike, not invert phase")
	}
}

// TestResampleScaledClamps exercises the last-resort clamp directly: the
// production gain never exceeds 1.0, but the encoder must still saturate
// rather than wrap if that ever changes.
func TestResampleScaledClamps(t *testing.T) {
	src := []float64{0.9, 0.9, -0.9, -0.9}
	out := resampleScaled(src, 1.0, 2.0)
	hi := int16(binary.LittleEndian.Uint16(out[0:]))
	lo := int16(binary.LittleEndian.Uint16(out[4:]))
	if hi != 32767 || lo != -32767 {
		t.Fatalf("clipping failed: hi=%d lo=%d", hi, lo)
	}
}
