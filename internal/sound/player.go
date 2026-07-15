package sound

import (
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

// maxVoices bounds how many clips can sound at once. oto's mixer sums
// every concurrently-playing Player's samples with no limiter (confirmed
// by reading its internal/mux: `buf[i] += v * volume`) before handing the
// raw float32 to the OS - a fast typing burst firing one fire-and-forget
// Player per strike (the old approach) could sum enough near-full-scale
// voices to blow well past full scale, clipping hard. 32 concurrent
// voices is generous headroom above anything a human can actually
// produce; beyond that the oldest voice is dropped to make room.
const maxVoices = 32

// otoPlayerBufferSize keeps oto's own per-player prefetch buffer (bytes,
// mono int16) shallow. That buffer defaults to 0.5s of audio (see oto's
// mux.defaultBufferSize) and is topped up independently of real playback
// timing - it's filled once up front, then only refilled once it drains,
// which is a different thing from the 15ms OS-level BufferSize below. A
// deep default buffer meant a keystroke's voice sat unheard for up to
// ~1s, and several keystrokes queued during that wait all landed at the
// same instant in the next generated chunk instead of each getting its
// own timing. 20ms keeps both problems out of earshot.
const otoPlayerBufferSize = SampleRate * 2 * 20 / 1000

// deviceReadyTimeout bounds the wait for the audio device. A wedged audio
// server never closes oto's ready channel; without a timeout the caller
// (the GUI goroutine) would hang forever.
const deviceReadyTimeout = 5 * time.Second

// releaseK is the per-sample one-pole coefficient smoothing the headroom
// scale UPWARD (voices ending mid-mix): a ~6ms rise at 44.1kHz. Downward
// changes are instant instead: they coincide with an arriving hammer
// onset that masks them, and easing into them would clip. A rising scale
// has no masking transient - unsmoothed it stepped up to +3dB at a buffer
// boundary in the middle of a decay tail, an audible pump.
const releaseK = 1.0 / 256

// voice is one clip's audio in flight: gain-applied PCM16, and how far
// playback has gotten into it. The bytes are never written by the mixer,
// so one cached clip (the bell) can back many voices at once.
type voice struct {
	pcm []byte
	pos int
}

// Player owns the audio device and mixes all in-flight clips itself,
// through a single persistent oto.Player (Player implements io.Reader),
// instead of handing oto one fire-and-forget Player per strike - that's
// what let concurrent strikes sum past full scale with nothing to stop
// them. Read runs on oto's audio callback goroutine; Strike/PlayPCM run
// on the GUI goroutine; mu guards the shared voice list between them.
type Player struct {
	bankMu sync.RWMutex
	bank   *Bank

	// otoPlayer is never read after NewPlayer (except Close), but MUST be
	// kept: oto registers a GC cleanup on the *oto.Player that closes the
	// underlying stream once it becomes unreachable. A local variable
	// here would get collected (and playback silently killed) the first
	// time GC ran after NewPlayer returned.
	otoPlayer *oto.Player

	mu     sync.Mutex
	voices []*voice

	// Owned by Read (oto calls it from a single goroutine): the reused mix
	// accumulator and the smoothed headroom-scale state.
	acc        []int32
	scale      float64
	prevActive int
}

// NewPlayer opens the audio device, builds the sample bank, and starts
// the single persistent mixer player.
func NewPlayer() (*Player, error) {
	return newPlayer(NewBank())
}

func NewPlayerWithProfile(profile *typewriter.Profile) (*Player, error) {
	return newPlayer(NewBankWithProfile(profile))
}

func newPlayer(bank *Bank) (*Player, error) {
	op := &oto.NewContextOptions{
		SampleRate:   SampleRate,
		ChannelCount: 1,
		Format:       oto.FormatSignedInt16LE,
		// Small buffer: keystroke audio lives or dies on latency.
		BufferSize: 15 * time.Millisecond,
	}
	ctx, ready, err := oto.NewContext(op)
	if err != nil {
		return nil, err
	}
	select {
	case <-ready:
	case <-time.After(deviceReadyTimeout):
		// Abandoning the context is the only option (oto allows one per
		// process); the caller records the failure and never retries.
		return nil, errors.New("sound: audio device not ready after 5s")
	}
	p := &Player{bank: bank}
	p.otoPlayer = ctx.NewPlayer(p)
	p.otoPlayer.SetBufferSize(otoPlayerBufferSize)
	p.otoPlayer.Play()
	return p, nil
}

// Close stops the mixer's audio callback (oto v3.4 deprecated
// Player.Close; Pause stops the feed and oto's GC cleanup releases the
// stream). For app teardown only - the sound toggle just stops queueing
// strikes, because the underlying oto context cannot be closed or
// reopened within a process.
func (p *Player) Close() {
	if p.otoPlayer != nil {
		p.otoPlayer.Pause()
	}
}

// Strike queues the hammer sound for a strike at the given position with
// the given ink weight. Fire and forget; overlapping strikes mix in Read.
func (p *Player) Strike(page, row, col, nth int, ink float64) {
	p.bankMu.RLock()
	bank := p.bank
	p.bankMu.RUnlock()
	p.play(bank.Pick(page, row, col, nth, ink))
}

// SetProfile changes future strike sounds without reopening oto's singleton
// audio context. Voices already queued retain their own PCM and finish safely.
func (p *Player) SetProfile(profile *typewriter.Profile) {
	bank := NewBank()
	if !typewriter.IsLegacyClassic(profile) {
		bank = NewBankWithProfile(profile)
	}
	p.bankMu.Lock()
	p.bank = bank
	p.bankMu.Unlock()
}

// PlayPCM queues an arbitrary PCM16 mono clip at SampleRate (the margin
// bell) into the mix. The mixer never writes the bytes, so callers may
// share one cached clip across overlapping plays.
func (p *Player) PlayPCM(pcm []byte) {
	p.play(pcm)
}

func (p *Player) play(pcm []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.voices) >= maxVoices {
		p.voices[0] = nil // release the dropped voice; the array slot outlives the re-slice
		p.voices = p.voices[1:]
	}
	p.voices = append(p.voices, &voice{pcm: pcm})
}

// Read fills buf (PCM16 mono LE) with the mix of all in-flight voices,
// called repeatedly by oto's audio callback. Never returns an error or a
// short read: with no voices in flight it fills silence, keeping the
// single persistent Player continuously playing.
func (p *Player) Read(buf []byte) (int, error) {
	n := len(buf) / 2

	// Snapshot under the lock, mix outside it: Read is the only mutator
	// of voice positions and oto calls it from one goroutine, so mixing
	// from the snapshot cannot race Strike/PlayPCM - those only append to
	// (or re-slice) the list, never touch a queued voice's bytes. The GUI
	// goroutine never waits on a mix in progress.
	p.mu.Lock()
	if cap(p.acc) < n {
		p.acc = make([]int32, n)
	}
	acc := p.acc[:n]
	// Copy the pointers, not merely the slice header. At the voice cap play
	// clears/reuses slots in the shared backing array; a header-only snapshot
	// could observe a nil or replacement midway through this mix.
	voices := append([]*voice(nil), p.voices...)
	p.mu.Unlock()

	for i := range acc {
		acc[i] = 0
	}
	active := 0
	for _, v := range voices {
		if v.pos < len(v.pcm) {
			active++
		}
		take := min(n, (len(v.pcm)-v.pos)/2)
		for i := range take {
			acc[i] += int32(int16(binary.LittleEndian.Uint16(v.pcm[v.pos+i*2:])))
		}
		v.pos += take * 2
	}

	// Power-preserving headroom: summing N independent voices raises RMS
	// by roughly sqrt(N), so the mix is scaled by 1/sqrt(N) to stay near
	// single-voice loudness instead of clipping harder the more strikes
	// overlap. The hard clamp below is the rare last resort, not the
	// normal path. The scale eases upward (see releaseK) and steps
	// downward; after silence it snaps to target - there is no seam to
	// smooth when nothing was playing.
	target := 1.0
	if active > 1 {
		target = 1 / math.Sqrt(float64(active))
	}
	scale := p.scale
	if scale > target || p.prevActive == 0 {
		scale = target
	}
	for i, s := range acc {
		scale += (target - scale) * releaseK
		m := int32(float64(s) * scale)
		if m > 32767 {
			m = 32767
		} else if m < -32768 {
			m = -32768
		}
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(m)))
	}
	p.scale, p.prevActive = scale, active

	// Compact under the lock, dropping voices consumed by THIS read too -
	// a spent voice must not occupy a cap slot into the next Strike.
	p.mu.Lock()
	alive := p.voices[:0]
	for _, v := range p.voices {
		if v.pos < len(v.pcm) {
			alive = append(alive, v)
		}
	}
	for i := len(alive); i < len(p.voices); i++ {
		p.voices[i] = nil
	}
	p.voices = alive
	p.mu.Unlock()

	return n * 2, nil
}
