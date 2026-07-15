// Package format implements the STRIKE v1 and v2 file formats: append-only,
// timestamped keystroke log. See docs/DESIGN.md section 2.
//
// The file stores intent (keystrokes and timing), never appearance.
// Appearance is derived deterministically by internal/machine.
package format

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/lajosnagyuk/ayfor/internal/units"
)

const (
	Magic        = "STRK"
	Version      = 1
	Version2     = 2
	ModelVersion = 1
	// HeaderSize is 40 on disk; EncodeHeader writes 36 meaningful bytes
	// and the final 4 are reserved-zero for a future v1-compatible field.
	HeaderSize     = 40
	V2PreambleSize = 8

	// CheckInterval is how often writers emit a CHECK event.
	CheckInterval = 512

	// MaxFileBytes and MaxEvents bound hostile decode amplification while
	// retaining room for roughly 280 densely typed A4 pages. Existing files
	// beyond either limit remain untouched and receive an explicit error.
	MaxFileBytes = 128 << 20
	MaxEvents    = 1 << 20
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

// Header is the decoded header shared by the fixed-size v1 and JSON v2 codecs.
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

	// Typewriter is non-nil only for STRIKE v2. V1's FontID=1 and
	// ModelVersion=1 implicitly resolve to the immutable Ayfor Classic
	// package and deliberately remain byte-for-byte unchanged.
	Typewriter *TypewriterRef
}

// TypewriterRef binds a v2 document to one exact immutable package and
// deterministic engine implementation.
type TypewriterRef struct {
	ID            string `json:"id"`
	Version       string `json:"version"`
	Digest        string `json:"digest"`
	EngineID      string `json:"engine_id"`
	EngineVersion int    `json:"engine_version"`
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

// DefaultHeaderV2 returns a package-bound header. Geometry is copied from
// the selected package by the caller before the writer is created, so logical
// inspection remains possible if the package later goes missing.
func DefaultHeaderV2(seed uint64, createdUnixMS int64, ref TypewriterRef) Header {
	h := DefaultHeader(seed, createdUnixMS)
	h.FormatVersion = Version2
	h.FontID = 0
	h.ModelVersion = 0
	h.Typewriter = &ref
	return h
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

type headerV2JSON struct {
	Schema        int           `json:"schema"`
	CreatedUnixMS int64         `json:"created_unix_ms"`
	MachineSeed   string        `json:"machine_seed"`
	Typewriter    TypewriterRef `json:"typewriter"`
	Paper         uint8         `json:"paper"`
	Pitch         uint8         `json:"pitch_cpi"`
	LineSpacing   uint8         `json:"line_spacing_tenths"`
	Margins       [4]uint16     `json:"margins_tenth_mm"`
}

var (
	v2IDPattern     = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)+$`)
	v2CorePattern   = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	v2EnginePattern = regexp.MustCompile(`^[a-z0-9]+(?:[.-][a-z0-9]+)*$`)
)

// EncodeFileHeader encodes either supported header version. EncodeHeader is
// intentionally retained as the exact v1 codec for compatibility tests and
// third-party v1 callers.
func EncodeFileHeader(h Header) ([]byte, error) {
	if h.FormatVersion == Version {
		return EncodeHeader(h), nil
	}
	if h.FormatVersion != Version2 {
		return nil, fmt.Errorf("%w: %d", ErrBadVersion, h.FormatVersion)
	}
	if err := validateV2Header(h); err != nil {
		return nil, err
	}
	j := headerV2JSON{
		Schema: 1, CreatedUnixMS: h.CreatedUnixMS,
		MachineSeed: fmt.Sprintf("%016x", h.Seed), Typewriter: *h.Typewriter,
		Paper: h.Paper, Pitch: uint8(h.Pitch), LineSpacing: uint8(h.LineSpacing),
		Margins: [4]uint16{marginToU16(h.Margins.Left), marginToU16(h.Margins.Right), marginToU16(h.Margins.Top), marginToU16(h.Margins.Bottom)},
	}
	payload, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	if len(payload) > 65535 {
		return nil, fmt.Errorf("strike: v2 header is too large: %d", len(payload))
	}
	out := make([]byte, V2PreambleSize+len(payload))
	copy(out[:4], Magic)
	out[4] = Version2
	out[5] = 0
	binary.LittleEndian.PutUint16(out[6:8], uint16(len(payload)))
	copy(out[8:], payload)
	return out, nil
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

func decodeFileHeader(b []byte) (Header, int, []byte, error) {
	var h Header
	if len(b) < 5 {
		return h, 0, nil, fmt.Errorf("strike: header too short: %d bytes", len(b))
	}
	if string(b[:4]) != Magic {
		return h, 0, nil, ErrBadMagic
	}
	switch b[4] {
	case Version:
		h, err := DecodeHeader(b)
		if err != nil {
			return h, 0, nil, err
		}
		return h, HeaderSize, bytes.Clone(b[:HeaderSize]), nil
	case Version2:
		if len(b) < V2PreambleSize {
			return h, 0, nil, fmt.Errorf("strike: truncated v2 preamble")
		}
		if b[5] != 0 {
			return h, 0, nil, fmt.Errorf("%w: unsupported v2 flags 0x%02x", ErrCorrupt, b[5])
		}
		n := int(binary.LittleEndian.Uint16(b[6:8]))
		if n == 0 || len(b) < V2PreambleSize+n {
			return h, 0, nil, fmt.Errorf("strike: truncated v2 header: need %d bytes", V2PreambleSize+n)
		}
		payload := b[V2PreambleSize : V2PreambleSize+n]
		if err := rejectDuplicateJSONKeys(payload); err != nil {
			return h, 0, nil, fmt.Errorf("%w: v2 header JSON: %v", ErrCorrupt, err)
		}
		if err := requireV2KeySpelling(payload); err != nil {
			return h, 0, nil, fmt.Errorf("%w: v2 header JSON: %v", ErrCorrupt, err)
		}
		var j headerV2JSON
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&j); err != nil {
			return h, 0, nil, fmt.Errorf("%w: v2 header JSON: %v", ErrCorrupt, err)
		}
		var trailing any
		if err := dec.Decode(&trailing); err != io.EOF {
			if err == nil {
				err = errors.New("trailing JSON value")
			}
			return h, 0, nil, fmt.Errorf("%w: v2 header JSON: %v", ErrCorrupt, err)
		}
		if j.Schema != 1 {
			return h, 0, nil, fmt.Errorf("%w: unsupported v2 header schema %d", ErrCorrupt, j.Schema)
		}
		seedBytes, err := hex.DecodeString(j.MachineSeed)
		if err != nil || len(seedBytes) != 8 {
			return h, 0, nil, fmt.Errorf("%w: invalid v2 machine seed", ErrCorrupt)
		}
		h = Header{
			FormatVersion: Version2, Seed: binary.BigEndian.Uint64(seedBytes), CreatedUnixMS: j.CreatedUnixMS,
			Paper: j.Paper, Pitch: units.Pitch(j.Pitch), LineSpacing: units.LineSpacing(j.LineSpacing),
			Margins:    units.Margins{Left: u16ToMargin(j.Margins[0]), Right: u16ToMargin(j.Margins[1]), Top: u16ToMargin(j.Margins[2]), Bottom: u16ToMargin(j.Margins[3])},
			Typewriter: &j.Typewriter,
		}
		if err := validateV2Header(h); err != nil {
			return h, 0, nil, err
		}
		end := V2PreambleSize + n
		return h, end, bytes.Clone(b[:end]), nil
	default:
		return h, 0, nil, fmt.Errorf("%w: %d", ErrBadVersion, b[4])
	}
}

func validateV2Header(h Header) error {
	if h.Typewriter == nil {
		return fmt.Errorf("%w: v2 header has no typewriter", ErrCorrupt)
	}
	r := h.Typewriter
	if !v2IDPattern.MatchString(r.ID) || len(r.ID) > 160 || !validV2SemVer(r.Version) || len(r.Version) > 80 || !v2EnginePattern.MatchString(r.EngineID) || len(r.EngineID) > 80 || r.EngineVersion <= 0 || r.EngineVersion > 255 {
		return fmt.Errorf("%w: invalid v2 typewriter reference", ErrCorrupt)
	}
	if len(r.Digest) != 71 || r.Digest[:7] != "sha256:" {
		return fmt.Errorf("%w: invalid v2 package digest", ErrCorrupt)
	}
	if _, err := hex.DecodeString(r.Digest[7:]); err != nil {
		return fmt.Errorf("%w: invalid v2 package digest", ErrCorrupt)
	}
	if !h.Pitch.Valid() || !h.LineSpacing.Valid() || h.Paper != 1 {
		return fmt.Errorf("%w: invalid v2 geometry", ErrCorrupt)
	}
	return nil
}

func validV2SemVer(v string) bool {
	mainAndBuild := strings.SplitN(v, "+", 2)
	if len(mainAndBuild) == 2 && !validV2Identifiers(mainAndBuild[1], false) {
		return false
	}
	mainAndPre := strings.SplitN(mainAndBuild[0], "-", 2)
	return v2CorePattern.MatchString(mainAndPre[0]) && (len(mainAndPre) == 1 || validV2Identifiers(mainAndPre[1], true))
}

func validV2Identifiers(s string, rejectLeadingZeroNumeric bool) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		numeric := true
		for _, r := range part {
			if r < '0' || r > '9' {
				numeric = false
			}
			if !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-') {
				return false
			}
		}
		if rejectLeadingZeroNumeric && numeric && len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

// rejectDuplicateJSONKeys keeps header identity unambiguous across decoders.
// V2's schema uses lowercase keys exclusively, so accepting Go's usual
// case-insensitive struct aliases (for example both "id" and "ID") would
// make the signed/hashed bytes mean different things to different readers.
func rejectDuplicateJSONKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var value func() error
	value = func() error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := kt.(string)
				if !ok {
					return errors.New("object key is not a string")
				}
				fold := strings.ToLower(key)
				if seen[fold] {
					return fmt.Errorf("duplicate object key %q", key)
				}
				seen[fold] = true
				if err := value(); err != nil {
					return err
				}
			}
			_, err := dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := value(); err != nil {
					return err
				}
			}
			_, err := dec.Token()
			return err
		default:
			return fmt.Errorf("unexpected delimiter %q", delim)
		}
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func requireV2KeySpelling(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return err
	}
	allowed := map[string]bool{"schema": true, "created_unix_ms": true, "machine_seed": true, "typewriter": true, "paper": true, "pitch_cpi": true, "line_spacing_tenths": true, "margins_tenth_mm": true}
	for key := range top {
		if !allowed[key] {
			return fmt.Errorf("unknown field or noncanonical object key %q", key)
		}
	}
	var ref map[string]json.RawMessage
	if err := json.Unmarshal(top["typewriter"], &ref); err != nil {
		return err
	}
	refAllowed := map[string]bool{"id": true, "version": true, "digest": true, "engine_id": true, "engine_version": true}
	for key := range ref {
		if !refAllowed[key] {
			return fmt.Errorf("typewriter: unknown field or noncanonical object key %q", key)
		}
	}
	return nil
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
	if canonicalUvarintLen(delta) != n {
		return e, 0, fmt.Errorf("%w: noncanonical delta varint", ErrCorrupt)
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
		if canonicalUvarintLen(r) != rn {
			return e, 0, fmt.Errorf("%w: noncanonical rune varint", ErrCorrupt)
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

func canonicalUvarintLen(v uint64) int {
	var buf [binary.MaxVarintLen64]byte
	return binary.PutUvarint(buf[:], v)
}

// File is a fully decoded STRIKE file.
type File struct {
	Header Header
	Events []Event

	// Truncated is true when the file ended mid-event (crash during
	// append). Everything in Events is still valid.
	Truncated   bool
	headerBytes []byte
}

// HeaderBytes returns the exact header bytes accepted by Decode. Repair uses
// these bytes so a recoverable tail never rewrites package identity or changes
// the hash-chain prefix merely by canonicalizing JSON.
func (f *File) HeaderBytes() []byte { return bytes.Clone(f.headerBytes) }

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
	if len(b) > MaxFileBytes {
		return nil, fmt.Errorf("strike: file exceeds %d-byte safety limit", MaxFileBytes)
	}
	h, headerLen, rawHeader, err := decodeFileHeader(b)
	if err != nil {
		return nil, err
	}
	f := &File{Header: h, headerBytes: rawHeader}
	if remaining := len(b) - headerLen; remaining > 0 {
		f.Events = make([]Event, 0, presizeHint(remaining))
	}
	pos := headerLen
	for pos < len(b) {
		if len(f.Events) >= MaxEvents {
			return f, fmt.Errorf("strike: file exceeds %d-event safety limit", MaxEvents)
		}
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
	buf := f.headerBytes
	if len(buf) == 0 {
		var err error
		buf, err = EncodeFileHeader(f.Header)
		if err != nil {
			return res, err
		}
	}
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
	events     int
	bytes      int64
	dirty      bool   // payload has been written since the last CHECK
	buf        []byte // scratch encode buffer, reused across Append calls
}

// NewWriter creates a Writer for a brand new file and writes the header.
func NewWriter(w io.Writer, h Header) (*Writer, error) {
	fw := fnv.New64a()
	hdr, err := EncodeFileHeader(h)
	if err != nil {
		return nil, err
	}
	n, err := w.Write(hdr)
	if err != nil {
		return nil, err
	}
	if n != len(hdr) {
		return nil, io.ErrShortWrite
	}
	_, _ = fw.Write(hdr)
	return &Writer{w: w, hash: fw, bytes: int64(len(hdr))}, nil
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
	dirty := len(f.Events) > 0 && f.Events[len(f.Events)-1].Op != OpCheck
	return ResumeWriterValidated(w, existing, len(f.Events), dirty), nil
}

// ResumeWriterValidated builds a writer over bytes the caller has ALREADY
// decoded successfully and knows are not truncated: it skips the full
// re-Decode that ResumeWriter pays and only rebuilds the rolling hash.
// Session.Open decodes the file once to fold the document anyway; without
// this it decoded (and FNV-walked) the whole manuscript a second time
// just to resume appending.
func ResumeWriterValidated(w io.Writer, existing []byte, eventCount int, dirty bool) *Writer {
	fw := fnv.New64a()
	_, _ = fw.Write(existing)
	return &Writer{w: w, hash: fw, events: eventCount, bytes: int64(len(existing)), dirty: dirty}
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
	// Every payload reserves room for the final CHECK required by Close. If
	// this payload itself reaches the periodic boundary, reserve that CHECK as
	// well. Refusing before any write ensures Ayfor can never create a file its
	// own bounded decoder will reject on the next open.
	var checkScratch [16]byte
	checkBytes, err := EncodeEvent(checkScratch[:0], Event{Op: OpCheck})
	if err != nil {
		return err
	}
	neededEvents := 1
	neededBytes := int64(len(enc))
	if e.Op != OpCheck {
		neededEvents++
		neededBytes += int64(len(checkBytes))
	}
	if wr.events+neededEvents > MaxEvents {
		return fmt.Errorf("strike: writer would exceed %d-event safety limit", MaxEvents)
	}
	if wr.bytes+neededBytes > MaxFileBytes {
		return fmt.Errorf("strike: writer would exceed %d-byte safety limit", MaxFileBytes)
	}
	wr.buf = enc
	n, err := wr.w.Write(enc)
	if err != nil {
		return err
	}
	if n != len(enc) {
		return io.ErrShortWrite
	}
	_, _ = wr.hash.Write(enc)
	wr.events++
	wr.bytes += int64(len(enc))
	if e.Op == OpCheck {
		wr.dirty = false
		return nil
	}
	wr.dirty = true
	wr.sinceCheck++
	if wr.sinceCheck >= CheckInterval {
		return wr.Check()
	}
	return nil
}

// Check emits a CHECK event covering everything written so far.
func (wr *Writer) Check() error {
	if !wr.dirty {
		return nil
	}
	wr.sinceCheck = 0
	return wr.Append(Event{Op: OpCheck, Check: wr.hash.Sum64()})
}
