package main

import (
	"image/color"

	"fyne.io/fyne/v2"

	"github.com/lajosnagyuk/ayfor/internal/units"
)

// The strike guide: the acetate card-holder line of a real machine. A
// faint band under the current baseline and a firmer notch marking the
// slot centre where the next hammer lands. Drawn on the glass, never on
// the paper.
var (
	guideColor = color.NRGBA{R: 0x30, G: 0x50, B: 0x70, A: 0x12}
	notchColor = color.NRGBA{R: 0x30, G: 0x50, B: 0x70, A: 0x50}

	// Replay interstitial: a gentle warm scrim over the sheet and an
	// ink-coloured "time passes" line. Alpha is animated via withAlpha.
	dimColor     = color.NRGBA{R: 0xF7, G: 0xF3, B: 0xE9, A: 0xB4}
	gapTextColor = color.NRGBA{R: 0x1C, G: 0x1B, B: 0x22, A: 0xE6}
)

// withAlpha scales a colour's alpha by f (0..1), for fades.
func withAlpha(c color.NRGBA, f float64) color.NRGBA {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	c.A = uint8(float64(c.A)*f + 0.5)
	return c
}

// paperLayout keeps the sheet at A4 aspect, centred (contain fit), and
// positions the guide from the document's carriage state. Implementing
// fyne.Layout means we are re-laid-out on every resize for free.
type paperLayout struct {
	ui *ui
}

func (l *paperLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(350, 495)
}

func (l *paperLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	// Contain-fit the A4 aspect into the window.
	aspect := float32(units.PaperWidthMM / units.PaperHeightMM)
	w, h := size.Width, size.Height
	if w/h > aspect {
		w = h * aspect
	} else {
		h = w / aspect
	}
	ox := (size.Width - w) / 2
	oy := (size.Height - h) / 2

	u := l.ui
	if u.bg != nil {
		u.bg.Move(fyne.NewPos(0, 0))
		u.bg.Resize(size)
	}
	u.paper.Move(fyne.NewPos(ox, oy))
	u.paper.Resize(fyne.NewSize(w, h))

	// The comfort band overlays the sheet's top margin exactly. Its image has
	// the paper's aspect within that strip, so a plain stretch aligns it pixel
	// for pixel over the paper.
	if u.comfort != nil {
		if d := u.activeDoc(); d != nil {
			bandH := h * float32(d.Margins.Top/units.PaperHeightMM)
			u.comfort.Move(fyne.NewPos(ox, oy))
			u.comfort.Resize(fyne.NewSize(w, bandH))
		}
	}

	// The interstitial scrim covers the sheet; the line sits a third down,
	// where a title card would.
	u.dim.Move(fyne.NewPos(ox, oy))
	u.dim.Resize(fyne.NewSize(w, h))
	u.gapText.TextSize = w / 24
	u.gapText.Move(fyne.NewPos(ox, oy+h/3))
	u.gapText.Resize(fyne.NewSize(w, w/12))

	d := u.activeDoc()
	if d.Current < 0 {
		u.baseline.Hide()
		u.notch.Hide()
		return
	}
	u.baseline.Show()
	u.notch.Show()

	px := w / float32(units.PaperWidthMM) // screen px per mm

	baseY := oy + float32(d.YMM(d.YHalf))*px
	lineH := float32(units.BaseLineMM) * px
	u.baseline.Move(fyne.NewPos(ox, baseY-lineH*0.75))
	u.baseline.Resize(fyne.NewSize(w, lineH*0.95))

	slot := float32(d.Pitch.SlotMM()) * px
	cx := ox + float32(d.XMM(d.Col))*px
	u.notch.Move(fyne.NewPos(cx-slot/2, baseY+lineH*0.12))
	u.notch.Resize(fyne.NewSize(slot, lineH*0.14))
}

// refresh re-runs the layout with the current carriage state: this only
// repositions the baseline/notch guides. It deliberately does not go
// through Container.Refresh(), which would also call Refresh() on the
// paper canvas.Image and re-upload the whole page texture a second time -
// every caller already calls u.paper.Refresh() itself right before this,
// once, exactly when the bitmap content actually changed.
func (l *paperLayout) refresh() {
	c := l.ui.win.Canvas()
	if c == nil || c.Content() == nil {
		return
	}
	cont, ok := c.Content().(*fyne.Container)
	if !ok {
		return
	}
	l.Layout(cont.Objects, cont.Size())
}
