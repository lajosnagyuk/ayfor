package typewriter

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var glyphCSVHeader = []string{"codepoint", "dx_um", "dy_um", "tilt_mdeg", "ink_permille", "fill_permille", "audio_group"}

func parseGlyphCSV(data []byte) (map[rune]GlyphCalibration, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = len(glyphCSVHeader)
	r.ReuseRecord = true
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	for i, want := range glyphCSVHeader {
		if header[i] != want {
			return nil, fmt.Errorf("column %d is %q, want %q", i+1, header[i], want)
		}
	}
	out := make(map[rune]GlyphCalibration)
	for line := 2; ; line++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", line, err)
		}
		cp := rec[0]
		if len(cp) < 3 || !strings.HasPrefix(cp, "U+") {
			return nil, fmt.Errorf("line %d: invalid codepoint %q", line, cp)
		}
		v, err := strconv.ParseUint(cp[2:], 16, 32)
		if err != nil || v > unicode.MaxRune || v >= 0xD800 && v <= 0xDFFF {
			return nil, fmt.Errorf("line %d: invalid codepoint %q", line, cp)
		}
		if cp != fmt.Sprintf("U+%04X", v) {
			return nil, fmt.Errorf("line %d: codepoint %q is not canonical uppercase U+ notation", line, cp)
		}
		ru := rune(v)
		if _, exists := out[ru]; exists {
			return nil, fmt.Errorf("line %d: duplicate codepoint %s", line, cp)
		}
		parse := func(i, min, max int) (int, error) {
			n, err := strconv.Atoi(rec[i])
			if err != nil || n < min || n > max {
				return 0, fmt.Errorf("line %d: %s must be an integer in %d..%d", line, glyphCSVHeader[i], min, max)
			}
			return n, nil
		}
		dx, err := parse(1, -3000, 3000)
		if err != nil {
			return nil, err
		}
		dy, err := parse(2, -3000, 3000)
		if err != nil {
			return nil, err
		}
		tilt, err := parse(3, -15000, 15000)
		if err != nil {
			return nil, err
		}
		ink, err := parse(4, 100, 3000)
		if err != nil {
			return nil, err
		}
		fill, err := parse(5, 0, 1000)
		if err != nil {
			return nil, err
		}
		group := rec[6]
		if len(group) > 40 || !utf8.ValidString(group) || !validAudioGroup(group) {
			return nil, fmt.Errorf("line %d: invalid audio_group", line)
		}
		out[ru] = GlyphCalibration{DXMicrometres: dx, DYMicrometres: dy, TiltMilliDeg: tilt, InkPermille: ink, FillPermille: fill, AudioGroup: group}
	}
	return out, nil
}

func validAudioGroup(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
