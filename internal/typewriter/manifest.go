// Package typewriter loads, validates, and installs declarative ayfor
// typewriter packages. Packages are data-only ZIP archives; they can never
// carry executable code or cause network access while being opened.
package typewriter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

const SchemaVersion = 1

var (
	// A portable reverse-DNS name: at least two dot-separated components;
	// lowercase ASCII alphanumerics with internal hyphens only.
	idPattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
	corePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// Ref is the immutable identity written into a STRIKE v2 header.
type Ref struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

func (r Ref) String() string {
	if r.Digest == "" {
		return r.ID + "@" + r.Version
	}
	return r.ID + "@" + r.Version + " (" + shortDigest(r.Digest) + ")"
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return d
}

// Manifest is schema 1 of typewriter.json. Values that affect layout and
// appearance use fixed-point physical units so third-party implementations do
// not get to invent rounding behaviour.
type Manifest struct {
	Schema      int       `json:"schema"`
	ID          string    `json:"id"`
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	Publisher   string    `json:"publisher"`
	Description string    `json:"description,omitempty"`
	Fidelity    string    `json:"fidelity"`
	Engine      Engine    `json:"engine"`
	Geometry    Geometry  `json:"geometry"`
	Typeface    Typeface  `json:"typeface"`
	Mechanics   Mechanics `json:"mechanics"`
	Sound       Sound     `json:"sound"`
	Licenses    []string  `json:"licenses"`
	Provenance  string    `json:"provenance"`
	Preview     string    `json:"preview,omitempty"`
}

type Engine struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type Geometry struct {
	PitchCPI              int    `json:"pitch_cpi"`
	LineSpacing           int    `json:"line_spacing_tenths"`
	BellSlotsBeforeMargin int    `json:"bell_slots_before_margin"`
	DefaultMarginsTenthMM [4]int `json:"default_margins_tenth_mm"`
}

type Typeface struct {
	Family          string `json:"family"`
	Path            string `json:"path"`
	FaceIndex       int    `json:"face_index,omitempty"`
	EMMicrometres   int    `json:"em_um"`
	ScaleXPermille  int    `json:"scale_x_permille"`
	BaselineShiftUM int    `json:"baseline_shift_um"`
	MissingGlyph    string `json:"missing_glyph"`
}

type Mechanics struct {
	GlyphCalibration string `json:"glyph_calibration,omitempty"`
}

type Sound struct {
	SampleRateHz int      `json:"sample_rate_hz"`
	Hammer       []string `json:"hammer"`
	PitchSpread  int      `json:"pitch_spread_permille"`
	GainMin      int      `json:"gain_min_permille"`
	GainMax      int      `json:"gain_max_permille"`
}

// GlyphCalibration is an additive fixed adjustment for one physical slug.
type GlyphCalibration struct {
	DXMicrometres int
	DYMicrometres int
	TiltMilliDeg  int
	InkPermille   int
	FillPermille  int
	AudioGroup    string
}

// Profile is the immutable, fully materialized package passed to mechanics,
// rendering, and sound. Those packages never inspect archives or registry
// paths themselves.
type Profile struct {
	Ref         Ref
	Manifest    Manifest
	Font        []byte
	Glyphs      map[rune]GlyphCalibration
	HammerPCM16 [][]byte
}

var (
	ErrInvalidPackage = errors.New("typewriter: invalid package")
	ErrNotFound       = errors.New("typewriter: package not found")
	ErrConflict       = errors.New("typewriter: package identity conflict")
)

func decodeManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return m, fmt.Errorf("%w: typewriter.json: %v", ErrInvalidPackage, err)
	}
	if err := requireManifestKeySpelling(data); err != nil {
		return m, fmt.Errorf("%w: typewriter.json: %v", ErrInvalidPackage, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("%w: typewriter.json: %v", ErrInvalidPackage, err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return m, fmt.Errorf("%w: typewriter.json: %v", ErrInvalidPackage, err)
	}
	if err := validateManifest(m); err != nil {
		return m, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	return m, nil
}

func requireExactObjectKeys(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key := range object {
		if !set[key] {
			return nil, fmt.Errorf("unknown field or noncanonical object key %q", key)
		}
	}
	return object, nil
}

func requireManifestKeySpelling(data []byte) error {
	top, err := requireExactObjectKeys(data, "schema", "id", "version", "name", "publisher", "description", "fidelity", "engine", "geometry", "typeface", "mechanics", "sound", "licenses", "provenance", "preview")
	if err != nil {
		return err
	}
	checks := []struct {
		field string
		keys  []string
	}{
		{"engine", []string{"id", "version"}},
		{"geometry", []string{"pitch_cpi", "line_spacing_tenths", "bell_slots_before_margin", "default_margins_tenth_mm"}},
		{"typeface", []string{"family", "path", "face_index", "em_um", "scale_x_permille", "baseline_shift_um", "missing_glyph"}},
		{"mechanics", []string{"glyph_calibration"}},
		{"sound", []string{"sample_rate_hz", "hammer", "pitch_spread_permille", "gain_min_permille", "gain_max_permille"}},
	}
	for _, check := range checks {
		if _, err := requireExactObjectKeys(top[check.field], check.keys...); err != nil {
			return fmt.Errorf("%s: %v", check.field, err)
		}
	}
	return nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var trailing any
	err := dec.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("trailing JSON value")
	}
	return err
}

func validateManifest(m Manifest) error {
	if m.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %d", m.Schema)
	}
	if !idPattern.MatchString(m.ID) || len(m.ID) > 160 || hasWindowsReservedIDComponent(m.ID) {
		return fmt.Errorf("invalid package id %q", m.ID)
	}
	if err := ValidateVersion(m.Version); err != nil {
		return err
	}
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > 120 || !safeDisplayText(m.Name) {
		return errors.New("name must be non-empty UTF-8 and at most 120 bytes")
	}
	if strings.TrimSpace(m.Publisher) == "" || len(m.Publisher) > 120 || !safeDisplayText(m.Publisher) {
		return errors.New("publisher must be non-empty and at most 120 bytes")
	}
	if len(m.Description) > 4096 || !safeDisplayText(m.Description) {
		return errors.New("description must be UTF-8 and at most 4096 bytes")
	}
	if m.Fidelity != "original" && m.Fidelity != "inspired" && m.Fidelity != "specimen" {
		return fmt.Errorf("unsupported fidelity %q", m.Fidelity)
	}
	if m.Engine.ID != "ayfor-classic" && m.Engine.ID != "classic-impact" {
		return fmt.Errorf("unsupported engine %q", m.Engine.ID)
	}
	if m.Engine.Version != 1 {
		return fmt.Errorf("unsupported engine %s/%d", m.Engine.ID, m.Engine.Version)
	}
	if m.Engine.ID == "ayfor-classic" && (m.ID != ClassicID || m.Version != "1.0.0") {
		return errors.New("ayfor-classic/1 is reserved for io.ayfor.typewriters.classic@1.0.0")
	}
	if m.ID == ClassicID && (m.Engine.ID != "ayfor-classic" || m.Version != "1.0.0") {
		return errors.New("the Ayfor Classic package identity is reserved")
	}
	if m.Geometry.PitchCPI != 10 && m.Geometry.PitchCPI != 12 {
		return fmt.Errorf("pitch must be 10 or 12 cpi, got %d", m.Geometry.PitchCPI)
	}
	if m.Geometry.LineSpacing != 10 && m.Geometry.LineSpacing != 15 && m.Geometry.LineSpacing != 20 {
		return fmt.Errorf("line spacing must be 10, 15, or 20, got %d", m.Geometry.LineSpacing)
	}
	if m.Geometry.BellSlotsBeforeMargin < 0 || m.Geometry.BellSlotsBeforeMargin > 20 {
		return errors.New("bell offset is outside 0..20 slots")
	}
	for _, v := range m.Geometry.DefaultMarginsTenthMM {
		if v < 0 || v > 1000 {
			return errors.New("a default margin is outside 0..100 mm")
		}
	}
	if err := safeRelativeFile(m.Typeface.Path); err != nil {
		return fmt.Errorf("typeface.path: %v", err)
	}
	if !validRolePath(m.Typeface.Path, "fonts", ".ttf") {
		return errors.New("typeface.path must name a .ttf file below fonts/")
	}
	if strings.TrimSpace(m.Typeface.Family) == "" || len(m.Typeface.Family) > 120 || !safeDisplayText(m.Typeface.Family) {
		return errors.New("typeface family must be non-empty UTF-8 and at most 120 bytes")
	}
	if m.Typeface.FaceIndex != 0 {
		return errors.New("font collections are not supported in schema 1")
	}
	if m.Typeface.EMMicrometres < 1000 || m.Typeface.EMMicrometres > 10000 {
		return errors.New("typeface em_um is outside 1000..10000")
	}
	if m.Typeface.ScaleXPermille < 500 || m.Typeface.ScaleXPermille > 2000 {
		return errors.New("typeface scale_x_permille is outside 500..2000")
	}
	if m.Typeface.BaselineShiftUM < -3000 || m.Typeface.BaselineShiftUM > 3000 {
		return errors.New("typeface baseline_shift_um is outside -3000..3000")
	}
	if m.Typeface.MissingGlyph != "U+FFFD" {
		return errors.New("schema 1 requires missing_glyph U+FFFD")
	}
	if m.Mechanics.GlyphCalibration != "" {
		if err := safeRelativeFile(m.Mechanics.GlyphCalibration); err != nil {
			return fmt.Errorf("mechanics.glyph_calibration: %v", err)
		}
		if !validRolePath(m.Mechanics.GlyphCalibration, "calibration", ".csv") {
			return errors.New("mechanics.glyph_calibration must name a .csv file below calibration/")
		}
	}
	if m.Sound.SampleRateHz != 44100 {
		return errors.New("schema 1 requires 44100 Hz audio")
	}
	if len(m.Sound.Hammer) == 0 || len(m.Sound.Hammer) > 32 {
		return errors.New("sound.hammer must contain 1..32 samples")
	}
	seen := make(map[string]bool)
	for _, p := range m.Sound.Hammer {
		if err := safeRelativeFile(p); err != nil {
			return fmt.Errorf("sound.hammer: %v", err)
		}
		if !validRolePath(p, "sounds", ".wav") {
			return errors.New("sound.hammer entries must name .wav files below sounds/")
		}
		if seen[p] {
			return fmt.Errorf("duplicate hammer sample %q", p)
		}
		seen[p] = true
	}
	if m.Sound.PitchSpread < 0 || m.Sound.PitchSpread > 200 {
		return errors.New("pitch spread is outside 0..200 permille")
	}
	if m.Sound.GainMin < 0 || m.Sound.GainMin > 1000 || m.Sound.GainMax < m.Sound.GainMin || m.Sound.GainMax > 2000 {
		return errors.New("invalid sound gain range")
	}
	if len(m.Licenses) == 0 || len(m.Licenses) > 16 {
		return errors.New("licenses must contain 1..16 paths")
	}
	licenseSeen := make(map[string]bool)
	for _, p := range m.Licenses {
		if err := safeRelativeFile(p); err != nil {
			return fmt.Errorf("licenses: %v", err)
		}
		if !validRolePath(p, "licenses", ".txt", ".md") {
			return errors.New("license entries must name .txt or .md files below licenses/")
		}
		if licenseSeen[p] {
			return fmt.Errorf("duplicate license %q", p)
		}
		licenseSeen[p] = true
	}
	if err := safeRelativeFile(m.Provenance); err != nil {
		return fmt.Errorf("provenance: %v", err)
	}
	if !validRolePath(m.Provenance, "provenance", ".txt", ".md") {
		return errors.New("provenance must name a .txt or .md file below provenance/")
	}
	if m.Preview != "" {
		if err := safeRelativeFile(m.Preview); err != nil {
			return fmt.Errorf("preview: %v", err)
		}
		if m.Preview != "preview.png" {
			return errors.New("schema 1 preview must be preview.png")
		}
	}
	return nil
}

// ValidateVersion applies schema-1's canonical package/release SemVer rules.
// Lowercase identifiers avoid distinct identities colliding on default macOS
// and Windows filesystems.
func ValidateVersion(v string) error {
	if !validSemVer(v) || v != strings.ToLower(v) || len(v) > 80 {
		return fmt.Errorf("invalid canonical semantic version %q", v)
	}
	return nil
}

func validRolePath(name, dir string, extensions ...string) bool {
	if !strings.HasPrefix(name, dir+"/") || strings.Count(name, "/") != 1 {
		return false
	}
	ext := strings.ToLower(path.Ext(name))
	for _, allowed := range extensions {
		if ext == allowed {
			return true
		}
	}
	return false
}

func safeDisplayText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

func validSemVer(v string) bool {
	mainAndBuild := strings.SplitN(v, "+", 2)
	if len(mainAndBuild) == 2 && !validSemVerIdentifiers(mainAndBuild[1], false) {
		return false
	}
	mainAndPre := strings.SplitN(mainAndBuild[0], "-", 2)
	if !corePattern.MatchString(mainAndPre[0]) {
		return false
	}
	return len(mainAndPre) == 1 || validSemVerIdentifiers(mainAndPre[1], true)
}

func validSemVerIdentifiers(s string, rejectLeadingZeroNumeric bool) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		numeric := true
		for _, r := range part {
			if !('0' <= r && r <= '9') {
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

func compareSemVer(a, b string) int {
	stripBuild := func(s string) string { return strings.SplitN(s, "+", 2)[0] }
	as, bs := strings.SplitN(stripBuild(a), "-", 2), strings.SplitN(stripBuild(b), "-", 2)
	ac, bc := strings.Split(as[0], "."), strings.Split(bs[0], ".")
	for i := range 3 {
		if c := compareNumericString(ac[i], bc[i]); c != 0 {
			return c
		}
	}
	if len(as) == 1 && len(bs) == 1 {
		return 0
	}
	if len(as) == 1 {
		return 1
	}
	if len(bs) == 1 {
		return -1
	}
	ap, bp := strings.Split(as[1], "."), strings.Split(bs[1], ".")
	for i := 0; i < min(len(ap), len(bp)); i++ {
		an, bn := allDigits(ap[i]), allDigits(bp[i])
		if an && bn {
			if c := compareNumericString(ap[i], bp[i]); c != 0 {
				return c
			}
		} else if an != bn {
			if an {
				return -1
			}
			return 1
		} else if ap[i] < bp[i] {
			return -1
		} else if ap[i] > bp[i] {
			return 1
		}
	}
	if len(ap) < len(bp) {
		return -1
	}
	if len(ap) > len(bp) {
		return 1
	}
	return 0
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func compareNumericString(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func safeRelativeFile(name string) error {
	if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, '\\') || strings.ContainsRune(name, 0) {
		return fmt.Errorf("unsafe path %q", name)
	}
	if !norm.NFC.IsNormalString(name) {
		return fmt.Errorf("path is not Unicode NFC: %q", name)
	}
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("path contains control or formatting characters: %q", name)
		}
	}
	if path.IsAbs(name) || path.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("unsafe path %q", name)
	}
	return nil
}

func hasWindowsReservedIDComponent(id string) bool {
	for _, component := range strings.Split(id, ".") {
		upper := strings.ToUpper(component)
		if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" {
			return true
		}
		if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
			return true
		}
	}
	return false
}

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
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("unterminated object")
			}
		case '[':
			for dec.More() {
				if err := value(); err != nil {
					return err
				}
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("unterminated array")
			}
		default:
			return fmt.Errorf("unexpected delimiter %q", delim)
		}
		return nil
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

func materialize(pkg *Package) (*Profile, error) {
	read := func(name string) ([]byte, error) {
		b, ok := pkg.Files[name]
		if !ok {
			return nil, fmt.Errorf("%w: manifest references missing %q", ErrInvalidPackage, name)
		}
		return b, nil
	}
	fontBytes, err := read(pkg.Manifest.Typeface.Path)
	if err != nil {
		return nil, err
	}
	parsed, err := opentype.Parse(fontBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid font %q: %v", ErrInvalidPackage, pkg.Manifest.Typeface.Path, err)
	}
	if err := validateFontGeometry(parsed); err != nil {
		return nil, fmt.Errorf("%w: unsafe font %q: %v", ErrInvalidPackage, pkg.Manifest.Typeface.Path, err)
	}
	for _, p := range pkg.Manifest.Licenses {
		b, err := read(p)
		if err != nil {
			return nil, err
		}
		if !validPackageText(b) {
			return nil, fmt.Errorf("%w: license %q must be non-empty readable UTF-8 text", ErrInvalidPackage, p)
		}
	}
	provenance, err := read(pkg.Manifest.Provenance)
	if err != nil {
		return nil, err
	}
	if !validPackageText(provenance) {
		return nil, fmt.Errorf("%w: provenance %q must be non-empty readable UTF-8 text", ErrInvalidPackage, pkg.Manifest.Provenance)
	}
	if pkg.Manifest.Preview != "" {
		if _, err := read(pkg.Manifest.Preview); err != nil {
			return nil, err
		}
	}
	glyphs := map[rune]GlyphCalibration{}
	if pkg.Manifest.Mechanics.GlyphCalibration != "" {
		b, err := read(pkg.Manifest.Mechanics.GlyphCalibration)
		if err != nil {
			return nil, err
		}
		glyphs, err = parseGlyphCSV(b)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidPackage, pkg.Manifest.Mechanics.GlyphCalibration, err)
		}
	}
	hammers := make([][]byte, 0, len(pkg.Manifest.Sound.Hammer))
	for _, p := range pkg.Manifest.Sound.Hammer {
		b, err := read(p)
		if err != nil {
			return nil, err
		}
		pcm, err := decodeWAVPCM16(b, pkg.Manifest.Sound.SampleRateHz)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidPackage, p, err)
		}
		hammers = append(hammers, pcm)
	}
	return &Profile{
		Ref:         Ref{ID: pkg.Manifest.ID, Version: pkg.Manifest.Version, Digest: pkg.Digest},
		Manifest:    pkg.Manifest,
		Font:        bytes.Clone(fontBytes),
		Glyphs:      glyphs,
		HammerPCM16: hammers,
	}, nil
}

func validPackageText(b []byte) bool {
	if !utf8.Valid(b) || len(bytes.TrimSpace(b)) == 0 {
		return false
	}
	for _, r := range string(b) {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

const (
	maxFontGlyphs              = 4096
	fontValidationPPEM         = fixed.Int26_6(128 * 64)
	maxValidatedGlyphDimension = 1024
	maxValidatedGlyphPixels    = 1 << 20
)

// validateFontGeometry forces every glyph through the non-rasterizing outline
// parser at a deliberately large ppem. This turns malformed or absurd outline
// bounds into an installation error instead of a deferred allocation bomb when
// a particular rune is first typed.
func validateFontGeometry(f *opentype.Font) error {
	if units := int(f.UnitsPerEm()); units < 16 || units > 16384 {
		return fmt.Errorf("units-per-em %d is outside 16..16384", units)
	}
	if n := f.NumGlyphs(); n < 1 || n > maxFontGlyphs {
		return fmt.Errorf("glyph count %d is outside 1..%d", n, maxFontGlyphs)
	}
	var buf sfnt.Buffer
	for i := 0; i < f.NumGlyphs(); i++ {
		bounds, _, err := f.GlyphBounds(&buf, sfnt.GlyphIndex(i), fontValidationPPEM, font.HintingNone)
		if err != nil {
			return fmt.Errorf("glyph %d bounds: %v", i, err)
		}
		w := bounds.Max.X.Ceil() - bounds.Min.X.Floor()
		h := bounds.Max.Y.Ceil() - bounds.Min.Y.Floor()
		if w < 0 || h < 0 || w > maxValidatedGlyphDimension || h > maxValidatedGlyphDimension || int64(w)*int64(h) > maxValidatedGlyphPixels {
			return fmt.Errorf("glyph %d has unsafe %dx%d bounds", i, w, h)
		}
	}
	return nil
}

// AssetPaths returns every manifest-referenced file, sorted and unique.
func (m Manifest) AssetPaths() []string {
	set := map[string]bool{m.Typeface.Path: true, m.Provenance: true}
	if m.Mechanics.GlyphCalibration != "" {
		set[m.Mechanics.GlyphCalibration] = true
	}
	if m.Preview != "" {
		set[m.Preview] = true
	}
	for _, p := range m.Sound.Hammer {
		set[p] = true
	}
	for _, p := range m.Licenses {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
