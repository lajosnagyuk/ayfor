package main

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/render"
)

func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

// TestDankifyMapsAnchors pins the remap's endpoints and monotonicity: paper
// maps to the dark ground, full ink to near the light ink, and a mid tone
// lands strictly between - no clipping to either pole.
func TestDankifyMapsAnchors(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 1))
	src.SetRGBA(0, 0, render.PaperColor())      // blank paper
	src.SetRGBA(1, 0, color.RGBA{0, 0, 0, 255}) // full ink
	mid := uint8(paperLum / 2)                  // ~half luminance
	src.SetRGBA(2, 0, color.RGBA{R: mid, G: mid, B: mid, A: 255})

	dst := image.NewRGBA(src.Bounds())
	dankifyRect(dst, src, src.Bounds())

	paper := dst.RGBAAt(0, 0)
	if paper.R != dankBG.R || paper.G != dankBG.G || paper.B != dankBG.B {
		t.Errorf("paper mapped to %v, want dankBG %v", paper, dankBG)
	}
	ink := dst.RGBAAt(1, 0)
	if ink.R < dankInk.R-2 { // near the light ink (rounding tolerance)
		t.Errorf("full ink mapped to %v, want near dankInk %v", ink, dankInk)
	}
	m := dst.RGBAAt(2, 0)
	if !(m.R > dankBG.R && m.R < dankInk.R) {
		t.Errorf("mid tone R=%d not strictly between %d and %d", m.R, dankBG.R, dankInk.R)
	}
}

// TestDankifyDeterministic pins that the remap is a pure function of its input.
func TestDankifyDeterministic(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 40))
	fillRect(src, src.Bounds(), render.PaperColor())
	fillRect(src, image.Rect(5, 5, 30, 20), color.RGBA{40, 40, 40, 255})

	a := image.NewRGBA(src.Bounds())
	b := image.NewRGBA(src.Bounds())
	dankifyRect(a, src, src.Bounds())
	dankifyRect(b, src, src.Bounds())
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("dankify is not deterministic")
	}
}

// TestDankDirtyRectMatchesFullRemap pins that remapping only the changed box
// yields the same image as a full remap - the per-keystroke path is exact.
func TestDankDirtyRectMatchesFullRemap(t *testing.T) {
	canonical := image.NewRGBA(image.Rect(0, 0, 60, 60))
	fillRect(canonical, canonical.Bounds(), render.PaperColor())

	a := image.NewRGBA(canonical.Bounds())
	dankifyRect(a, canonical, canonical.Bounds()) // established dark copy

	// A new "strike" lands in a box; update only that box in a.
	box := image.Rect(20, 20, 35, 35)
	fillRect(canonical, box, color.RGBA{25, 25, 25, 255})
	dankifyRect(a, canonical, box)

	b := image.NewRGBA(canonical.Bounds())
	dankifyRect(b, canonical, canonical.Bounds()) // fresh full remap
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("dirty-rect remap diverged from a full remap")
	}
}

// TestReplayDankIncrementalMatchesFullRemap pins the replay dirty-rect
// path end to end: after events stamp incrementally into the run's own
// dank buffer, the shown frame must be byte-identical to a full remap of
// the canonical frame - the per-event ~4M-pixel remap it replaced. The
// renderer runs at the real renderScale because strikeBox is calibrated
// to it.
func TestReplayDankIncrementalMatchesFullRemap(t *testing.T) {
	r, err := render.New(renderScale)
	if err != nil {
		t.Fatal(err)
	}
	u := &ui{renderer: r, dankOn: true}
	run := &replayRun{doc: page.New(format.DefaultHeader(1, 0)), pageIdx: -1}

	events := []format.Event{
		{Op: format.OpSession, WallUnixMS: 1},
		{Op: format.OpNewSheet},
	}
	for _, ch := range "incremental dank" {
		op := format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: ch}
		if ch == ' ' {
			op = format.Event{DeltaMS: 100, Op: format.OpSpace}
		}
		events = append(events, op)
	}
	var shown image.Image
	for _, e := range events {
		shown = u.replayAdvance(run, e)
	}
	if shown == nil {
		t.Fatal("replay produced no frame")
	}
	got, ok := shown.(*image.RGBA)
	if !ok || got != run.dankImg {
		t.Fatal("dank replay must show the run's own dark buffer")
	}

	full := image.NewRGBA(run.img.Bounds())
	dankifyRect(full, run.img, run.img.Bounds())
	if !bytes.Equal(got.Pix, full.Pix) {
		t.Fatal("incremental dank replay frame diverged from a full remap")
	}
}

// TestDankifyDirtyChecksPageIdentity pins the idx guard: all pages share
// bounds, so a dirty-box update aimed at a page the dank buffer does NOT
// currently hold must be refused - otherwise a strike stamped right after
// a flip pollutes the old page's dark copy.
func TestDankifyDirtyChecksPageIdentity(t *testing.T) {
	pageA := image.NewRGBA(image.Rect(0, 0, 40, 40))
	fillRect(pageA, pageA.Bounds(), render.PaperColor())
	pageB := image.NewRGBA(pageA.Bounds())
	fillRect(pageB, pageB.Bounds(), color.RGBA{30, 30, 30, 255})

	u := &ui{dankOn: true, dankImg: image.NewRGBA(pageA.Bounds()), dankPage: 0}
	dankifyRect(u.dankImg, pageA, pageA.Bounds())
	snapshot := append([]uint8(nil), u.dankImg.Pix...)

	// A dirty box for page 1 while the buffer holds page 0: no effect.
	u.dankifyDirty(1, pageB, image.Rect(5, 5, 20, 20))
	if !bytes.Equal(u.dankImg.Pix, snapshot) {
		t.Fatal("dirty update for a different page polluted the dank buffer")
	}

	// The same box for the held page: applied.
	fillRect(pageA, image.Rect(5, 5, 20, 20), color.RGBA{25, 25, 25, 255})
	u.dankifyDirty(0, pageA, image.Rect(5, 5, 20, 20))
	if bytes.Equal(u.dankImg.Pix, snapshot) {
		t.Fatal("dirty update for the held page was refused")
	}
}

// TestDankifyPreservesCanonical pins that showing the dark view never mutates
// the LRU-cached page bitmap.
func TestDankifyPreservesCanonical(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()
	for _, r := range "hello" {
		if _, err := u.sess.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	canonical := u.bitmap(u.sess.Doc.Current)
	snapshot := append([]uint8(nil), canonical.Pix...)

	u.dankOn = true
	if _, ok := u.displayImage(u.sess.Doc.Current).(*image.RGBA); !ok {
		t.Fatal("displayImage did not return an image")
	}
	if !bytes.Equal(u.bitmap(u.sess.Doc.Current).Pix, snapshot) {
		t.Fatal("dank view mutated the canonical bitmap")
	}
}

// TestDankAppendsNothing pins that toggling and rendering the dark view never
// touches the strike file.
func TestDankAppendsNothing(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()
	if _, err := u.sess.Strike('a'); err != nil {
		t.Fatal(err)
	}
	if err := u.sess.Flush(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(u.sess.Path)
	before := sha256.Sum256(b)

	u.dankOn = true
	u.displayImage(u.sess.Doc.Current)
	u.dankOn = false
	u.displayImage(u.sess.Doc.Current)

	if err := u.sess.Flush(); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(u.sess.Path)
	if sha256.Sum256(b) != before {
		t.Fatal("dark view changed the strike file")
	}
}
