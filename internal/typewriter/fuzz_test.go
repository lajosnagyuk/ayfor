package typewriter

import (
	"testing"

	"golang.org/x/image/font/opentype"
)

func FuzzLoadArchiveBytes(f *testing.F) {
	p, err := Builtin(ClassicID)
	if err != nil {
		f.Fatal(err)
	}
	valid, err := p.Archive()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte("not a zip"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxArchiveBytes+1 {
			return
		}
		p, err := LoadArchiveBytes(data)
		if err == nil {
			_, _ = p.Profile()
		}
	})
}

func FuzzDecodeWAVPCM16(f *testing.F) {
	p, err := Builtin(ClassicID)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(p.Files[p.Manifest.Sound.Hammer[0]])
	f.Add([]byte("RIFF"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) <= maxFileBytes {
			_, _ = decodeWAVPCM16(data, 44100)
		}
	})
}

func FuzzParseGlyphCSV(f *testing.F) {
	p, err := Builtin(OlympiaDemoID)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(p.Files[p.Manifest.Mechanics.GlyphCalibration])
	f.Add([]byte("codepoint,dx_um\nU+0041,0\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) <= maxFileBytes {
			_, _ = parseGlyphCSV(data)
		}
	})
}

func FuzzFontGeometry(f *testing.F) {
	p, err := Builtin(ClassicID)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(p.Files[p.Manifest.Typeface.Path])
	f.Add([]byte("not a font"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxFileBytes {
			return
		}
		parsed, err := opentype.Parse(data)
		if err == nil {
			_ = validateFontGeometry(parsed)
		}
	})
}
