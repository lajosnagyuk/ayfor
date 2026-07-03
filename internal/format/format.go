// Package format implements the STRIKE v1 file format: an append-only,
// timestamped keystroke log. See docs/DESIGN.md section 2.
//
// The file stores intent (keystrokes and timing), never appearance.
// Appearance is derived deterministically by internal/machine.
package format

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"unicode"

	"github.com/lajosnagyuk/ayfor/internal/units"
)

const (
	Magic        = "STRK"
	Version      = 1
	ModelVersion = 1
	// HeaderSize is 40 on disk; EncodeHeader writes 36 meaningful bytes
	// and the final 4 are reserved-zero for a future v1-compatible field.
	HeaderSize = 40

	// CheckInterval is how often writers emit a CHECK event.
	CheckInterval = 512
)

// Event opcodes.
const (
	OpStrike         = 0x01
	OpSpace          = 0x02
	OpBack           = 0x03
	OpCR             = 0x04
	OpLF             = 0x05
	OpHalfUp         = 0x06
	OpHalfDown       = 0x07
	OpNewSheet       = 0x10
	OpPagePrev       = 0x11
	OpPageNext       = 0x12
	OpToss           = 0x13
	OpSetPitch       = 0x20
	OpSetLinespace   = 0x21
	OpSetMargins     = 0x22
	OpNewRibbon      = 0x23
	OpSetTouch       = 0x24
	OpSetDisposition = 0x25
	OpSetSobriety    = 0x26
	OpSetCondition   = 0x27
	OpSession        = 0x30
	OpCheck          = 0x3F
)

// Session origins.
const (
	OriginHuman    = 0
	OriginImported = 1
)

// Header is the fixed 40-byte file header.
type Header struct {
	FormatVersion uint8
	ModelVersion  uint8
	Flags         uint16
	Seed          uint64
	CreatedUnixMS int64
	Paper         uint8 // 1 = A4
	Pitch         units.Pitch
	LineSpacing   units.LineSpacing
	FontID        uint8 // 1 = Courier Prime
	Margins       units.Margins
}

// Event is one decoded log entry.
type Event struct {
	DeltaMS uint64
	Op      uint8

	Rune       rune          // OpStrike
	Value      uint8         // OpSetPitch/Linespace/Touch/Disposition/Sobriety/Condition (x100 for the human/machine dials)
	Margins    units.Margins // OpSetMargins
	WallUnixMS int64         // OpSession
	Origin     uint8         // OpSession
	Check      uint64        // OpCheck
}

// DeriveSeed spreads a wall-clock instant (any resolution) across the
// seed space with the SplitMix64 golden-ratio increment, so documents
// created moments apart still get well-separated machine personalities.
// One definition - the GUI and the CLI used to carry private copies of
// the magic constant.
func DeriveSeed(t int64) uint64 {
	return uint64(t) * 0x9E3779B97F4A7C15
}

// DefaultHeader returns a header for a fresh document.
func DefaultHeader(seed uint64, createdUnixMS int64) Header {
	return Header{
		FormatVersion: Version,
		ModelVersion:  ModelVersion,
		Seed:          seed,
		CreatedUnixMS: createdUnixMS,
		Paper:         1,
		Pitch:         units.Pica,
		LineSpacing:   units.Single,
		FontID:        1,
		Margins:       units.DefaultMargins(),
	}
}

// marginToU16 clamps before converting: Go's float-to-unsigned conversion
// of a negative or overlarge value is implementation-specific, and a GUI
// bug feeding a bad margin must not produce platform-dependent bytes in an
// append-only, hash-chained file.
func marginToU16(mm float64) uint16 {
	if mm <= 0 || mm != mm { // negative or NaN
		return 0
	}
	if mm >= 6553.5 {
		return 65535
	}
	return uint16(mm*10 + 0.5)
}
func u16ToMargin(v uint16) float64 { return float64(v) / 10 }

// EncodeHeader writes the fixed header.
func EncodeHeader(h Header) []byte {
	b := make([]byte, HeaderSize)
	copy(b[0:4], Magic)
	b[4] = h.FormatVersion
	b[5] = h.ModelVersion
	binary.LittleEndian.PutUint16(b[6:8], h.Flags)
	binary.LittleEndian.PutUint64(b[8:16], h.Seed)
	binary.LittleEndian.PutUint64(b[16:24], uint64(h.CreatedUnixMS))
	b[24] = h.Paper
	b[25] = uint8(h.Pitch)
	b[26] = uint8(h.LineSpacing)
	b[27] = h.FontID
	binary.LittleEndian.PutUint16(b[28:30], marginToU16(h.Margins.Left))
	binary.LittleEndian.PutUint16(b[30:32], marginToU16(h.Margins.Right))
	binary.LittleEndian.PutUint16(b[32:34], marginToU16(h.Margins.Top))
	binary.LittleEndian.PutUint16(b[34:36], marginToU16(h.Margins.Bottom))
	return b
}

var (
	ErrBadMagic   = errors.New("strike: bad magic, not a STRIKE file")
	ErrBadVersion = errors.New("strike: unsupported format version")
)

// DecodeHeader parses the fixed header.
func DecodeHeader(b []byte) (Header, error) {
	var h Header
	if len(b) < HeaderSize {
		return h, fmt.Errorf("strike: header too short: %d bytes", len(b))
	}
	if string(b[0:4]) != Magic {
		return h, ErrBadMagic
	}
	h.FormatVersion = b[4]
	if h.FormatVersion != Version {
		return h, fmt.Errorf("%w: %d", ErrBadVersion, h.FormatVersion)
	}
	h.ModelVersion = b[5]
	h.Flags = binary.LittleEndian.Uint16(b[6:8])
	h.Seed = binary.LittleEndian.Uint64(b[8:16])
	h.CreatedUnixMS = int64(binary.LittleEndian.Uint64(b[16:24]))
	h.Paper = b[24]
	h.Pitch = units.Pitch(b[25])
	h.LineSpacing = units.LineSpacing(b[26])
	h.FontID = b[27]
	h.Margins = units.Margins{
		Left:   u16ToMargin(binary.LittleEndian.Uint16(b[28:30])),
		Right:  u16ToMargin(binary.LittleEndian.Uint16(b[30:32])),
		Top:    u16ToMargin(binary.LittleEndian.Uint16(b[32:34])),
		Bottom: u16ToMargin(binary.LittleEndian.Uint16(b[34:36])),
	}
	// Reject a header carrying degenerate physical settings: a zero/unknown
	// pitch makes slot width +Inf, a zero/unknown line spacing stops the
	// carriage advancing, and only A4 / Courier Prime are defined in v1.
	// These come from DefaultHeader on real files, so a bad one is corruption
	// or a hostile craft, not a benign variant.
	if !h.Pitch.Valid() || !h.LineSpacing.Valid() || h.Paper != 1 || h.FontID != 1 {
		return h, fmt.Errorf("%w: invalid header (pitch=%d linespace=%d paper=%d font=%d)",
			ErrCorrupt, h.Pitch, h.LineSpacing, h.Paper, h.FontID)
	}
	return h, nil
}

func putUvarint(dst []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(dst, tmp[:n]...)
}

// EncodeEvent appends the wire form of e to dst.
func EncodeEvent(dst []byte, e Event) ([]byte, error) {
	dst = putUvarint(dst, e.DeltaMS)
	dst = append(dst, e.Op)
	switch e.Op {
	case OpStrike:
		dst = putUvarint(dst, uint64(e.Rune))
	case OpSpace, OpBack, OpCR, OpLF, OpHalfUp, OpHalfDown,
		OpNewSheet, OpPagePrev, OpPageNext, OpToss, OpNewRibbon:
		// no payload
	case OpSetPitch, OpSetLinespace, OpSetTouch,
		OpSetDisposition, OpSetSobriety, OpSetCondition:
		dst = append(dst, e.Value)
	case OpSetMargins:
		var tmp [8]byte
		binary.LittleEndian.PutUint16(tmp[0:2], marginToU16(e.Margins.Left))
		binary.LittleEndian.PutUint16(tmp[2:4], marginToU16(e.Margins.Right))
		binary.LittleEndian.PutUint16(tmp[4:6], marginToU16(e.Margins.Top))
		binary.LittleEndian.PutUint16(tmp[6:8], marginToU16(e.Margins.Bottom))
		dst = append(dst, tmp[:]...)
	case OpSession:
		var tmp [9]byte
		binary.LittleEndian.PutUint64(tmp[0:8], uint64(e.WallUnixMS))
		tmp[8] = e.Origin
		dst = append(dst, tmp[:]...)
	case OpCheck:
		var tmp [8]byte
		binary.LittleEndian.PutUint64(tmp[:], e.Check)
		dst = append(dst, tmp[:]...)
	default:
		return nil, fmt.Errorf("strike: unknown opcode 0x%02X", e.Op)
	}
	return dst, nil
}

// ErrTruncated marks a final partial event; everything before it is valid.
var ErrTruncated = errors.New("strike: truncated final event")

// ErrCorrupt marks a structurally invalid event mid-stream: an overflowing
// varint or an out-of-range rune. Unlike ErrTruncated this is never a benign
// crash tail - the bytes are wrong, not merely missing - so Decode surfaces
// it as a hard error instead of silently dropping everything after it.
var ErrCorrupt = errors.New("strike: corrupt event stream")

// decodeEvent parses one event from b, returning bytes consumed.
//
// binary.Uvarint returns n == 0 when the buffer ran out (a genuine truncated
// tail) and n < 0 when the value overflows 64 bits (corruption). These must
// stay distinct: collapsing them lets a corrupt varint masquerade as a crash
// tail, which the session repair then "fixes" by discarding the real tail.
func decodeEvent(b []byte) (Event, int, error) {
	var e Event
	delta, n := binary.Uvarint(b)
	if n == 0 {
		return e, 0, ErrTruncated
	}
	if n < 0 {
		return e, 0, fmt.Errorf("%w: overflowing delta varint", ErrCorrupt)
	}
	e.DeltaMS = delta
	pos := n
	if pos >= len(b) {
		return e, 0, ErrTruncated
	}
	e.Op = b[pos]
	pos++
	rest := b[pos:]
	switch e.Op {
	case OpStrike:
		r, rn := binary.Uvarint(rest)
		if rn == 0 {
			return e, 0, ErrTruncated
		}
		if rn < 0 {
			return e, 0, fmt.Errorf("%w: overflowing rune varint", ErrCorrupt)
		}
		if r > unicode.MaxRune || (r >= 0xD800 && r <= 0xDFFF) {
			return e, 0, fmt.Errorf("%w: rune out of range U+%X", ErrCorrupt, r)
		}
		e.Rune = rune(r)
		pos += rn
	case OpSpace, OpBack, OpCR, OpLF, OpHalfUp, OpHalfDown,
		OpNewSheet, OpPagePrev, OpPageNext, OpToss, OpNewRibbon:
	case OpSetPitch, OpSetLinespace, OpSetTouch,
		OpSetDisposition, OpSetSobriety, OpSetCondition:
		if len(rest) < 1 {
			return e, 0, ErrTruncated
		}
		e.Value = rest[0]
		pos++
	case OpSetMargins:
		if len(rest) < 8 {
			return e, 0, ErrTruncated
		}
		e.Margins = units.Margins{
			Left:   u16ToMargin(binary.LittleEndian.Uint16(rest[0:2])),
			Right:  u16ToMargin(binary.LittleEndian.Uint16(rest[2:4])),
			Top:    u16ToMargin(binary.LittleEndian.Uint16(rest[4:6])),
			Bottom: u16ToMargin(binary.LittleEndian.Uint16(rest[6:8])),
		}
		pos += 8
	case OpSession:
		if len(rest) < 9 {
			return e, 0, ErrTruncated
		}
		e.WallUnixMS = int64(binary.LittleEndian.Uint64(rest[0:8]))
		e.Origin = rest[8]
		pos += 9
	case OpCheck:
		if len(rest) < 8 {
			return e, 0, ErrTruncated
		}
		e.Check = binary.LittleEndian.Uint64(rest[:8])
		pos += 8
	default:
		// No offset here: decodeEvent does not know it, and Decode wraps
		// this error with the event index and byte offset that matter.
		return e, 0, fmt.Errorf("strike: unknown opcode 0x%02X", e.Op)
	}
	return e, pos, nil
}

// File is a fully decoded STRIKE file.
type File struct {
	Header Header
	Events []Event

	// Truncated is true when the file ended mid-event (crash during
	// append). Everything in Events is still valid.
	Truncated bool
}

// maxDecodePresize caps the eager event-slice allocation so a hostile file
// with a huge byte length cannot force a multi-gigabyte allocation before a
// single event is validated. Beyond the cap the slice simply grows as events
// are appended, exactly as it did before pre-sizing. 1<<20 events covers a
// ~280-page draft without capping.
const maxDecodePresize = 1 << 20

// presizeHint estimates event count from remaining bytes (smallest event is
// 2 bytes) so the slice grows a couple of times instead of ~20, bounded so
// the estimate tracks validated events rather than raw file length.
func presizeHint(remaining int) int {
	if remaining <= 0 {
		return 0
	}
	if n := remaining / 2; n < maxDecodePresize {
		return n
	}
	return maxDecodePresize
}

// Decode parses a whole file image.
func Decode(b []byte) (*File, error) {
	h, err := DecodeHeader(b)
	if err != nil {
		return nil, err
	}
	f := &File{Header: h}
	if remaining := len(b) - HeaderSize; remaining > 0 {
		f.Events = make([]Event, 0, presizeHint(remaining))
	}
	pos := HeaderSize
	for pos < len(b) {
		e, n, err := decodeEvent(b[pos:])
		if err != nil {
			if errors.Is(err, ErrTruncated) {
				f.Truncated = true
				return f, nil
			}
			// A hard error (corruption) mid-stream: return the valid prefix
			// alongside the error so a caller can recover it (with the
			// original preserved) instead of losing the whole manuscript.
			// Everything in f.Events up to here decoded cleanly.
			return f, fmt.Errorf("at byte %d: %w", pos, err)
		}
		f.Events = append(f.Events, e)
		pos += n
	}
	return f, nil
}

// VerifyResult reports on the hash chain.
type VerifyResult struct {
	Checks   int
	FirstBad int // index into Events of first failing CHECK, -1 if none
}

// Verify re-encodes the file and validates every CHECK event's rolling
// FNV-1a 64 hash. A CHECK covers all file bytes from offset 0 up to (not
// including) the CHECK event's own delta-time varint.
func Verify(f *File) (VerifyResult, error) {
	res := VerifyResult{FirstBad: -1}
	h := fnv.New64a()
	buf := EncodeHeader(f.Header)
	_, _ = h.Write(buf)
	enc := make([]byte, 0, 16)
	for i, e := range f.Events {
		if e.Op == OpCheck {
			res.Checks++
			if e.Check != h.Sum64() && res.FirstBad == -1 {
				res.FirstBad = i
			}
		}
		var err error
		enc, err = EncodeEvent(enc[:0], e)
		if err != nil {
			return res, err
		}
		_, _ = h.Write(enc)
	}
	return res, nil
}

// Writer appends events to an io.Writer (normally an *os.File opened with
// O_APPEND). It maintains the rolling hash and emits CHECK events every
// CheckInterval events. Writer is not safe for concurrent use.
//
// Hash writes throughout this file discard their error explicitly:
// hash.Hash's Write never returns one, by its documented contract.
type Writer struct {
	w          io.Writer
	hash       hash.Hash64 // the stdlib name for exactly "an io.Writer with Sum64"
	sinceCheck int
	buf        []byte // scratch encode buffer, reused across Append calls
}

// NewWriter creates a Writer for a brand new file and writes the header.
func NewWriter(w io.Writer, h Header) (*Writer, error) {
	fw := fnv.New64a()
	hdr := EncodeHeader(h)
	if _, err := w.Write(hdr); err != nil {
		return nil, err
	}
	_, _ = fw.Write(hdr)
	return &Writer{w: w, hash: fw}, nil
}

// ResumeWriter creates a Writer positioned after existing content. The
// caller must pass the full existing file image so the rolling hash can be
// reconstructed; the underlying writer must already be positioned at the
// end (O_APPEND).
func ResumeWriter(w io.Writer, existing []byte) (*Writer, error) {
	f, err := Decode(existing)
	if err != nil {
		return nil, err
	}
	if f.Truncated {
		return nil, errors.New("strike: cannot append to truncated file; repair first")
	}
	return ResumeWriterValidated(w, existing), nil
}

// ResumeWriterValidated builds a writer over bytes the caller has ALREADY
// decoded successfully and knows are not truncated: it skips the full
// re-Decode that ResumeWriter pays and only rebuilds the rolling hash.
// Session.Open decodes the file once to fold the document anyway; without
// this it decoded (and FNV-walked) the whole manuscript a second time
// just to resume appending.
func ResumeWriterValidated(w io.Writer, existing []byte) *Writer {
	fw := fnv.New64a()
	_, _ = fw.Write(existing)
	return &Writer{w: w, hash: fw}
}

// Append writes one event, followed by a CHECK when the interval is due.
// Only payload events count toward the interval - the CHECK itself does
// not, so checkpoints land every CheckInterval payload events exactly
// (counting the CHECK used to shave the effective interval to 511).
func (wr *Writer) Append(e Event) error {
	enc, err := EncodeEvent(wr.buf[:0], e)
	if err != nil {
		return err
	}
	wr.buf = enc
	if _, err := wr.w.Write(enc); err != nil {
		return err
	}
	_, _ = wr.hash.Write(enc)
	if e.Op == OpCheck {
		return nil
	}
	wr.sinceCheck++
	if wr.sinceCheck >= CheckInterval {
		return wr.Check()
	}
	return nil
}

// Check emits a CHECK event covering everything written so far.
func (wr *Writer) Check() error {
	wr.sinceCheck = 0
	return wr.Append(Event{Op: OpCheck, Check: wr.hash.Sum64()})
}
