package page

import (
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

func olympiaProfileHeader(t *testing.T) (*typewriter.Profile, format.Header) {
	t.Helper()
	pkg, err := typewriter.Builtin(typewriter.OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := pkg.Profile()
	if err != nil {
		t.Fatal(err)
	}
	g := profile.Manifest.Geometry
	h := format.DefaultHeaderV2(42, 1, format.TypewriterRef{ID: profile.Ref.ID, Version: profile.Ref.Version, Digest: profile.Ref.Digest, EngineID: profile.Manifest.Engine.ID, EngineVersion: profile.Manifest.Engine.Version})
	h.Pitch = units.Pitch(g.PitchCPI)
	h.LineSpacing = units.LineSpacing(g.LineSpacing)
	h.Margins = units.Margins{Left: float64(g.DefaultMarginsTenthMM[0]) / 10, Right: float64(g.DefaultMarginsTenthMM[1]) / 10, Top: float64(g.DefaultMarginsTenthMM[2]) / 10, Bottom: float64(g.DefaultMarginsTenthMM[3]) / 10}
	return profile, h
}

func TestProfileBackedDocRefusesPitchOverride(t *testing.T) {
	profile, h := olympiaProfileHeader(t)
	d := NewWithProfile(h, profile)
	if res := d.Apply(format.Event{Op: format.OpSetPitch, Value: 12}); res.Applied {
		t.Fatal("fixed-Pica profile accepted Elite pitch")
	}
	if d.Pitch != units.Pica {
		t.Fatalf("pitch changed to %d", d.Pitch)
	}
	f := &format.File{Header: h, Events: []format.Event{{Op: format.OpSetPitch, Value: 12}}}
	if err := VerifyProfile(f, profile); err == nil {
		t.Fatal("profile verification accepted crafted pitch event")
	}
}

func TestProfileBellDistanceIsUsed(t *testing.T) {
	profile, h := olympiaProfileHeader(t)
	profile.Manifest.Geometry.BellSlotsBeforeMargin = 2
	d := NewWithProfile(h, profile)
	d.Apply(format.Event{Op: format.OpNewSheet})
	d.Col = d.MaxCol() - 3
	if d.InBellZone() {
		t.Fatal("entered two-slot bell zone too early")
	}
	if res := d.Apply(format.Event{Op: format.OpSpace}); !res.Bell {
		t.Fatal("did not ring on entering package-defined bell zone")
	}
}
