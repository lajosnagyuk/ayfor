// Package bell synthesizes the margin bell: a struck brass cup, a
// fundamental and two inharmonic partials with a fast attack and a long
// decay, generated once - no audio files, no audio libraries. Playback is
// the caller's job: the GUI feeds PCM() through the app's shared mixer
// (internal/sound), which replaced the old shell-out to afplay - that
// path wrote a fixed, predictable file in the shared temp directory
// (symlink-followable by any local user on Linux), forked an unbounded
// player process per ring, went silent forever on its first write
// failure, and added ~80ms of fork/exec latency to a warning that exists
// to be timely.
package bell

import (
	"encoding/binary"
	"math"
	"sync"
)

// SampleRate must match the mixer's rate (sound.SampleRate); the bell is
// queued as a raw voice with no resampling. Pinned by a test.
const SampleRate = 44100

const duration = 0.9 // seconds

var (
	once sync.Once
	pcm  []byte
)

// PCM returns the bell strike as 16-bit little-endian mono PCM at
// SampleRate, synthesized on first use and cached. The buffer is shared
// across calls (the mixer never writes voice bytes); callers must not
// mutate it.
func PCM() []byte {
	once.Do(func() {
		n := int(SampleRate * duration)
		pcm = make([]byte, n*2)
		for i := range n {
			t := float64(i) / SampleRate
			// Fundamental and two inharmonic partials, like a real bell cup.
			v := 0.6*math.Sin(2*math.Pi*1560*t) +
				0.35*math.Sin(2*math.Pi*2412*t) +
				0.15*math.Sin(2*math.Pi*3921*t)
			// Fast attack, exponential decay.
			env := math.Exp(-t * 6)
			if t < 0.002 {
				env *= t / 0.002
			}
			binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(v*env*0.55*32767)))
		}
	})
	return pcm
}
