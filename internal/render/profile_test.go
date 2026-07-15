package render

import (
	"bytes"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

func TestOlympiaProfileProducesStableDistinctRender(t *testing.T) {
	pkg, err := typewriter.Builtin(typewriter.OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pkg.Profile()
	if err != nil {
		t.Fatal(err)
	}
	h := format.DefaultHeaderV2(42, 0, format.TypewriterRef{ID: p.Ref.ID, Version: p.Ref.Version, Digest: p.Ref.Digest, EngineID: p.Manifest.Engine.ID, EngineVersion: p.Manifest.Engine.Version})
	d := page.NewWithProfile(h, p)
	d.Apply(format.Event{Op: format.OpNewSheet})
	for _, r := range "A specimen from Olympia" {
		e := format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: r}
		if r == ' ' {
			e = format.Event{DeltaMS: 100, Op: format.OpSpace}
		}
		d.Apply(e)
	}
	r1, err := NewWithProfile(4, p)
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := NewWithProfile(4, p)
	a, err := r1.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := r2.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("profile render is not deterministic")
	}
	legacy, _ := New(4)
	c, _ := legacy.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if bytes.Equal(a.Pix, c.Pix) {
		t.Fatal("Olympia profile did not change the typeface")
	}
}

func TestSplendidProfileProducesStableDistinctRender(t *testing.T) {
	pkg, err := typewriter.Builtin(typewriter.OlympiaSplendidID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pkg.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.Geometry.PitchCPI != 12 {
		t.Fatalf("Splendid pitch = %d CPI, want 12", p.Manifest.Geometry.PitchCPI)
	}
	h := format.DefaultHeaderV2(66, 0, format.TypewriterRef{ID: p.Ref.ID, Version: p.Ref.Version, Digest: p.Ref.Digest, EngineID: p.Manifest.Engine.ID, EngineVersion: p.Manifest.Engine.Version})
	d := page.NewWithProfile(h, p)
	d.Apply(format.Event{Op: format.OpNewSheet})
	for _, r := range "Splendid Elite specimen" {
		e := format.Event{DeltaMS: 90, Op: format.OpStrike, Rune: r}
		if r == ' ' {
			e = format.Event{DeltaMS: 90, Op: format.OpSpace}
		}
		d.Apply(e)
	}
	r1, err := NewWithProfile(4, p)
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := NewWithProfile(4, p)
	a, err := r1.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := r2.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("Splendid profile render is not deterministic")
	}
	sm3pkg, _ := typewriter.Builtin(typewriter.OlympiaDemoID)
	sm3, _ := sm3pkg.Profile()
	other, _ := NewWithProfile(4, sm3)
	c, _ := other.RenderPage(d.Pages[0], d.Pitch, d.PaperSeed(0))
	if bytes.Equal(a.Pix, c.Pix) {
		t.Fatal("Splendid profile did not change the SM3 appearance")
	}
}
