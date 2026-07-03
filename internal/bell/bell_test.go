package bell

import (
	"encoding/binary"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/sound"
)

func samples(t *testing.T) []int16 {
	t.Helper()
	b := PCM()
	if len(b)%2 != 0 {
		t.Fatalf("PCM length %d is not whole int16 samples", len(b))
	}
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[i*2:]))
	}
	return out
}

func TestBellRings(t *testing.T) {
	s := samples(t)
	if got, want := len(s), int(SampleRate*duration); got != want {
		t.Fatalf("bell is %d samples, want %d", got, want)
	}
	peak := int16(0)
	for _, v := range s {
		if v > peak {
			peak = v
		}
	}
	if peak < 8000 {
		t.Fatalf("bell is nearly silent, peak %d", peak)
	}
}

func TestBellEnvelope(t *testing.T) {
	s := samples(t)
	// Attack: starts from silence (no click), peaks early.
	if s[0] != 0 {
		t.Fatalf("bell starts at %d, want 0 (click)", s[0])
	}
	head, tail := 0, 0
	for _, v := range s[:len(s)/8] {
		if int(v) > head {
			head = int(v)
		}
	}
	for _, v := range s[len(s)/2:] {
		if int(v) > tail {
			tail = int(v)
		}
	}
	if tail >= head/4 {
		t.Fatalf("bell does not decay: head peak %d, tail peak %d", head, tail)
	}
}

// TestBellMatchesMixerRate pins the wiring assumption: the bell is queued
// into the mixer as a raw voice with no resampling, so the two packages'
// sample rates must agree.
func TestBellMatchesMixerRate(t *testing.T) {
	if SampleRate != sound.SampleRate {
		t.Fatalf("bell synthesizes at %d Hz but the mixer runs at %d Hz", SampleRate, sound.SampleRate)
	}
}

// TestBellPCMIsCached pins the shared-buffer contract: repeated calls
// return the same backing bytes (the mixer treats voices as read-only, so
// overlapping rings can share one clip).
func TestBellPCMIsCached(t *testing.T) {
	a, b := PCM(), PCM()
	if &a[0] != &b[0] {
		t.Fatal("PCM must return the cached clip, not a fresh synthesis")
	}
}
