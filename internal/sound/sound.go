// Package sound plays the hammer strike sound with low latency. Base
// samples are real, not synthesized: a handful of clean single-key hits,
// picked and trimmed from a field recording of an Olivetti Lettera 22
// (Pixabay license - see samples/SOURCE.md for provenance and
// processing). A small base set reads as one consistent machine instead
// of a grab bag when strikes overlap during fast typing; the "Blizzard
// trick" variety comes back from pitch-shifting each base sample at
// strike time. Which base sample plays is hashed from the strike's
// position; how much its pitch shifts and how loud it plays both derive
// from the same ink weight, so a heavier strike is consistently both
// louder and a shade deeper - not two independently random dimensions.
package sound

import (
	"embed"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math"

	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

const SampleRate = 44100

// pitchSpread bounds how far ink shifts a base sample's pitch, as a
// fraction of its natural rate (>1 = shorter/higher, like faster tape).
// 6% matches the old synthesized bank's variant spread - audible variety
// without sounding like a different machine.
const pitchSpread = 0.06

//go:embed samples/*.pcm
var sampleFS embed.FS

// Bank holds the decoded base samples, ready to be pitch-shifted per
// strike. Decoding once at load time (rather than per strike) keeps the
// per-strike cost to one resample pass over ~6,174 samples.
type Bank struct {
	bases       [][]float64 // one per base sample, in [-1, 1]
	pitchSpread float64
	gainMin     float64
	gainMax     float64
}

// NewBank loads and decodes the embedded strike samples. Panics if the
// embed is missing or malformed - this is a build-time invariant, not a
// runtime condition the app can recover from.
func NewBank() *Bank {
	// ReadDir returns entries sorted by filename, so base-sample order
	// (and therefore what Pick's hash maps to) is stable across builds.
	entries, err := sampleFS.ReadDir("samples")
	if err != nil {
		panic(fmt.Sprintf("sound: embedded samples missing: %v", err))
	}
	b := &Bank{bases: make([][]float64, 0, len(entries)), pitchSpread: pitchSpread, gainMin: 0.55, gainMax: 1}
	for _, e := range entries {
		data, err := sampleFS.ReadFile("samples/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("sound: embedded sample %q unreadable: %v", e.Name(), err))
		}
		b.bases = append(b.bases, decodePCM16(data))
	}
	if len(b.bases) == 0 {
		panic("sound: no embedded strike samples found")
	}
	return b
}

// NewBankWithProfile builds a bank from package-owned, already validated
// PCM16. It makes private decoded copies so registry/package lifetimes cannot
// mutate audio in flight.
func NewBankWithProfile(profile *typewriter.Profile) *Bank {
	b := &Bank{
		bases:       make([][]float64, 0, len(profile.HammerPCM16)),
		pitchSpread: float64(profile.Manifest.Sound.PitchSpread) / 1000,
		gainMin:     float64(profile.Manifest.Sound.GainMin) / 1000,
		gainMax:     float64(profile.Manifest.Sound.GainMax) / 1000,
	}
	for _, pcm := range profile.HammerPCM16 {
		b.bases = append(b.bases, decodePCM16(pcm))
	}
	return b
}

// Pick returns the ready-to-play PCM16 for a strike, deterministically:
// the base sample is chosen by position, the pitch shift and gain both
// derive from the same ink weight, and the gain is already applied - a
// caller cannot forget it. The same strike in the same place always
// sounds the same. One allocation per call: the resample interpolates
// straight into the gain-applied int16 output.
func (b *Bank) Pick(page, row, col, nth int, ink float64) []byte {
	h := fnv.New64a()
	var buf [8]byte
	for _, v := range []int{page, row, col, nth} {
		binary.LittleEndian.PutUint64(buf[:], uint64(int64(v)))
		h.Write(buf[:])
	}
	idx := int(h.Sum64() % uint64(len(b.bases)))

	if ink != ink {
		ink = 1.0 // NaN would poison rate and gain; play a nominal strike
	}
	// Floored at 0 as well as capped: a negative ink would otherwise give
	// a rate outside the documented spread and a negative (phase-inverted)
	// gain.
	inkNorm := math.Max(0, math.Min(1.2, ink)) / 1.2 // 0 (lightest) .. 1 (heaviest)
	// Heavier strikes pitch down slightly (rate < 1, longer/deeper);
	// lighter strikes pitch up slightly (rate > 1, shorter/brighter).
	spread := b.pitchSpread
	if spread == 0 && b.gainMax == 0 { // zero-value Bank is not public, but keep it nominal in tests
		spread = pitchSpread
	}
	rate := 1 + spread*(1-2*inkNorm)
	gainMin, gainMax := b.gainMin, b.gainMax
	if gainMax == 0 {
		gainMin, gainMax = 0.55, 1
	}
	gain := gainMin + (gainMax-gainMin)*inkNorm

	return resampleScaled(b.bases[idx], rate, gain)
}

// resampleScaled stretches or shrinks src by rate (>1 = shorter and
// higher, like faster tape) with linear interpolation, applying gain and
// encoding to PCM16 in the same pass - the whole per-strike pipeline in
// one output allocation.
func resampleScaled(src []float64, rate, gain float64) []byte {
	n := int(float64(len(src)) / rate)
	out := make([]byte, n*2)
	for i := range n {
		pos := float64(i) * rate
		j := int(pos)
		if j+1 >= len(src) {
			break
		}
		f := pos - float64(j)
		v := (src[j]*(1-f) + src[j+1]*f) * gain
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(v*32767)))
	}
	return out
}

// decodePCM16 converts little-endian int16 bytes to float64 in [-1, 1].
func decodePCM16(src []byte) []float64 {
	out := make([]float64, len(src)/2)
	for i := range out {
		out[i] = float64(int16(binary.LittleEndian.Uint16(src[i*2:]))) / 32768.0
	}
	return out
}
