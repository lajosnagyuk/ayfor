package machine

import (
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

func TestClassicProfileDoesNotChangeLegacyModel(t *testing.T) {
	pkg, err := typewriter.Builtin(typewriter.ClassicID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pkg.Profile()
	if err != nil {
		t.Fatal(err)
	}
	legacy := New(42)
	profiled := NewWithProfile(42, p)
	contexts := []Context{
		{Glyph: 'A', Page: 0, Row: 0, Col: 0},
		{Glyph: 'e', Prev: 'h', DeltaMS: 83, Page: 2, Row: 7, Col: 11, RibbonStrikes: 1234, Touch: .85},
		{Glyph: '?', DeltaMS: 7000, Page: 1, Row: 20, Col: 60, NthOnCell: 2, Condition: 1.8, Disposition: 1.4, Sobriety: 1.5},
	}
	for i, c := range contexts {
		if got, want := profiled.StrikeFor(c), legacy.StrikeFor(c); got != want {
			t.Fatalf("context %d changed classic model:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

func TestOlympiaCalibrationIsApplied(t *testing.T) {
	pkg, _ := typewriter.Builtin(typewriter.OlympiaDemoID)
	p, _ := pkg.Profile()
	base := New(42).StrikeFor(Context{Glyph: 'A', Page: 0, Row: 0, Col: 0})
	got := NewWithProfile(42, p).StrikeFor(Context{Glyph: 'A', Page: 0, Row: 0, Col: 0})
	if got.DX != base.DX-0.06 || got.DY != base.DY+0.04 || got.TiltDeg != base.TiltDeg-0.18 {
		t.Fatalf("fixed Olympia slug offsets not applied:\n got %+v\nbase %+v", got, base)
	}
}

func TestSplendidCalibrationIsApplied(t *testing.T) {
	pkg, _ := typewriter.Builtin(typewriter.OlympiaSplendidID)
	p, _ := pkg.Profile()
	base := New(66).StrikeFor(Context{Glyph: 'A', Page: 0, Row: 0, Col: 0})
	got := NewWithProfile(66, p).StrikeFor(Context{Glyph: 'A', Page: 0, Row: 0, Col: 0})
	if got.DX != base.DX-0.035 || got.DY != base.DY+0.055 || got.TiltDeg != base.TiltDeg-0.12 {
		t.Fatalf("fixed Splendid slug offsets not applied:\n got %+v\nbase %+v", got, base)
	}
}
