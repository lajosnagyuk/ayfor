package main

// Dank mode: a display-only dark view. The canonical page bitmaps are warm
// paper with dark ink; dankify remaps them to a solarized-dark background with
// near-white ink, preserving grain, deboss and ink-weight variation as
// luminance detail. It is glass only - the renderer, the strike file and every
// export are untouched; only what reaches the screen is remapped, into a
// separate image that never mutates the LRU-cached bitmaps.

import (
	"image"
	"image/color"

	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/render"
)

// strikeBox is a generous pixel box around one strike, covering the widest
// glyph plus the machine's offsets and relief padding at any pitch.
func strikeBox(rec page.StrikeRec) image.Rectangle {
	cx := (rec.XMM + rec.App.DX) * renderScale
	cy := (rec.YMM + rec.App.DY) * renderScale
	const halfMM = 8.0
	half := halfMM * renderScale
	return image.Rect(int(cx-half), int(cy-half), int(cx+half+1), int(cy+half+1))
}

// Owner-facing palette: a warm, dark ground and a near-white warm ink.
var (
	dankBG  = color.RGBA{R: 0x1E, G: 0x15, B: 0x0E, A: 0xFF}
	dankInk = color.RGBA{R: 0xEE, G: 0xE8, B: 0xD5, A: 0xFF}
)

// lightGround is the window fill behind the sheet in normal (light) mode: the
// paper colour, so the letterbox blends into the page instead of framing it in
// a hard border.
var lightGround = color.NRGBA{R: render.PaperColor().R, G: render.PaperColor().G, B: render.PaperColor().B, A: 0xFF}

// paperLum is the luminance of blank paper: the "no ink" end of the scale.
var paperLum = lum(render.PaperColor().R, render.PaperColor().G, render.PaperColor().B)

func lum(r, g, b uint8) float64 {
	return 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
}

// dankifyRect remaps the pixels of src in rectangle r into dst. For each pixel
// t is how inked it is measured against paper luminance, and the output lerps
// dankBG -> dankInk by t, so grain and ink gradients survive as detail on the
// dark side. Alpha passes through unchanged (transparent chrome stays
// transparent). dst and src must share pixel geometry over r.
func dankifyRect(dst, src *image.RGBA, r image.Rectangle) {
	r = r.Intersect(src.Bounds()).Intersect(dst.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		si := src.PixOffset(r.Min.X, y)
		di := dst.PixOffset(r.Min.X, y)
		for x := r.Min.X; x < r.Max.X; x++ {
			sr, sg, sb, sa := src.Pix[si], src.Pix[si+1], src.Pix[si+2], src.Pix[si+3]
			t := clampUnit((paperLum - lum(sr, sg, sb)) / paperLum)
			dst.Pix[di] = lerpByte(dankBG.R, dankInk.R, t)
			dst.Pix[di+1] = lerpByte(dankBG.G, dankInk.G, t)
			dst.Pix[di+2] = lerpByte(dankBG.B, dankInk.B, t)
			dst.Pix[di+3] = sa
			si += 4
			di += 4
		}
	}
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func lerpByte(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5)
}

// displayImage returns what should be shown for page idx: the canonical bitmap
// when dank is off, or a dark-remapped copy when on. The dank copy is a single
// reused image; a page change re-remaps it in full.
func (u *ui) displayImage(idx int) image.Image {
	canonical := u.bitmap(idx)
	if !u.dankOn {
		return canonical
	}
	b := canonical.Bounds()
	if u.dankImg != nil && u.dankImg.Bounds() == b && u.dankPage == idx {
		return u.dankImg // already remapped for this page
	}
	if u.dankImg == nil || u.dankImg.Bounds() != b {
		u.dankImg = image.NewRGBA(b)
	}
	dankifyRect(u.dankImg, canonical, b)
	u.dankPage = idx
	return u.dankImg
}

// replayDisplay returns the image the glass shows for the current replay
// frame. The run owns its dank buffer (never the live page's cache), kept
// incrementally: per-strike dirty boxes are remapped in replayAdvance,
// and a full remap happens only when the canonical frame was re-rendered
// (page flip, pitch change) or dank came on mid-replay. fresh reports a
// re-rendered canonical frame.
func (u *ui) replayDisplay(run *replayRun, fresh bool) image.Image {
	if !u.dankOn {
		run.dankSynced = false // if dank comes back on, force a full remap
		return run.img
	}
	if fresh || !run.dankSynced || run.dankImg == nil || run.dankImg.Bounds() != run.img.Bounds() {
		if run.dankImg == nil || run.dankImg.Bounds() != run.img.Bounds() {
			run.dankImg = image.NewRGBA(run.img.Bounds())
		}
		dankifyRect(run.dankImg, run.img, run.img.Bounds())
		run.dankSynced = true
	}
	return run.dankImg
}

// Dark variants of the on-glass chrome, at the same alphas as the light ones.
var (
	dankGuideColor = color.NRGBA{R: 0x9A, G: 0x8C, B: 0x78, A: 0x18}
	dankNotchColor = color.NRGBA{R: 0x9A, G: 0x8C, B: 0x78, A: 0x60}
	dankDimColor   = color.NRGBA{R: 0x1E, G: 0x15, B: 0x0E, A: 0xB4}
	dankGapColor   = color.NRGBA{R: 0xEE, G: 0xE8, B: 0xD5, A: 0xE6}
)

// dimBase and gapTextBase give the active replay-interstitial colours, so the
// fade follows the palette instead of the fixed light constants.
func (u *ui) dimBase() color.NRGBA {
	if u.dankOn {
		return dankDimColor
	}
	return dimColor
}

func (u *ui) gapTextBase() color.NRGBA {
	if u.dankOn {
		return dankGapColor
	}
	return gapTextColor
}

// applyDankChrome recolours the window ground and guides (and, via the base
// helpers, the replay scrim) for the active palette.
func (u *ui) applyDankChrome() {
	if u.dankOn {
		u.bg.FillColor = dankBG
		u.baseline.FillColor = dankGuideColor
		u.notch.FillColor = dankNotchColor
	} else {
		u.bg.FillColor = lightGround
		u.baseline.FillColor = guideColor
		u.notch.FillColor = notchColor
	}
	u.bg.Refresh()
	u.baseline.Refresh()
	u.notch.Refresh()
}

// dankifyDirty remaps just the area around newly stamped strikes on page
// idx, so the per-keystroke path stays cheap (no full-page remap). The
// page-identity check matters: all pages share bounds, so without it a
// strike stamped right after a flip would remap boxes from the NEW page's
// canonical into a dank buffer still holding the OLD page - only masked
// today by displayImage's full remap running afterwards.
func (u *ui) dankifyDirty(idx int, canonical *image.RGBA, box image.Rectangle) {
	if !u.dankOn || u.dankImg == nil || u.dankPage != idx || u.dankImg.Bounds() != canonical.Bounds() {
		return
	}
	dankifyRect(u.dankImg, canonical, box)
}

// toggleDank flips the dark view, persists it, and repaints.
func (u *ui) toggleDank() {
	u.dankOn = !u.dankOn
	u.prefs.SetBool("dankMode", u.dankOn)
	u.dankImg = nil
	u.dankPage = -1
	u.applyDankChrome()
	u.paper.Image = u.displayImage(u.sess.Doc.Current)
	u.paper.Refresh()
	u.updateComfort()
	u.refreshMenuChecks()
}
