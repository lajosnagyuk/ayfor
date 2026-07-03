package main

// The Comfort menu: display-only creature comforts drawn on the glass, never
// in the file and never in an export. A page number and a running word count,
// both typed in the current machine's own hand (touch, disposition, sobriety,
// condition) via page.Doc.ChromeStrike, laid into the sheet's top margin.
//
// The overlay is a separate transparent canvas.Image above the paper. It is
// stamped with the real renderer, so nothing here touches the LRU-cached page
// bitmaps and the strike file stays byte-identical whether it is on or off.

import (
	"fmt"
	"image"
	"time"

	"fyne.io/fyne/v2"

	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

// comfortRecalcInterval is how often the word count is recomputed while
// typing on the current page (the page number updates immediately on a flip).
const comfortRecalcInterval = 2 * time.Second

func (u *ui) comfortEnabled() bool { return u.comfortPageNo || u.comfortWordCount }

// startComfortTicker recomputes the word count on an interval, so it stays
// roughly live without re-counting on every keystroke. It runs ONLY while
// the word count is on (toggling manages it; a disabled comfort must not
// wake the main loop every 2s), and a tick where the counts have not moved
// restamps nothing and uploads nothing - the band is already showing those
// exact pixels.
func (u *ui) startComfortTicker() {
	if u.comfortStop != nil {
		return
	}
	u.comfortStop = make(chan struct{})
	stop := u.comfortStop
	go func() {
		t := time.NewTicker(comfortRecalcInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				fyne.Do(func() {
					if !u.comfortWordCount {
						return
					}
					d := u.activeDoc()
					if d == nil {
						return
					}
					pg, tot := d.WordCounts() // cheap: pages cache their counts
					if [2]int{pg, tot} == u.comfortWC {
						return
					}
					u.updateComfort()
				})
			}
		}
	}()
}

// stopComfortTicker stops the recalc goroutine if running. Idempotent.
func (u *ui) stopComfortTicker() {
	if u.comfortStop != nil {
		close(u.comfortStop)
		u.comfortStop = nil
	}
}

// comfortAfterPaint refreshes the overlay when the shown page changes (a flip,
// a new sheet, a toss), keeping the page number immediate without re-counting
// words on every keystroke.
func (u *ui) comfortAfterPaint() {
	if !u.comfortEnabled() {
		return
	}
	if d := u.activeDoc(); d != nil && d.Current != u.comfortPage {
		u.updateComfort()
	}
}

// updateComfort rebuilds the top-margin chrome band and shows it. Runs on the
// Fyne thread.
func (u *ui) updateComfort() {
	if u.comfort == nil {
		return
	}
	d := u.activeDoc()
	if !u.renderComfortBand(d) {
		u.comfort.Hide()
		u.comfortPage = -2
		return
	}
	u.comfort.Image = u.comfortImg
	u.comfort.Refresh()
	u.comfort.Show()
	u.comfortPage = d.Current
	u.layout.refresh()
}

// renderComfortBand stamps the chrome into u.comfortImg (reallocating it only
// when the band size changes) and reports whether anything is to be shown. It
// touches no canvas, no page bitmap, and no session, so it is safe to exercise
// headlessly and can never alter the document or its file.
func (u *ui) renderComfortBand(d *page.Doc) bool {
	if !u.comfortEnabled() || d == nil || d.Current < 0 {
		return false
	}
	// The band spans the full sheet width and the top margin only. Because it
	// starts at page y = 0, absolute page-space YMM inside the top margin maps
	// straight to band-pixel y - no offset math.
	wf := units.PaperWidthMM * renderScale
	w := int(wf + 0.5)
	h := int(d.Margins.Top*renderScale + 0.5)
	if h < 1 {
		return false
	}
	if u.comfortImg == nil || u.comfortImg.Bounds().Dx() != w || u.comfortImg.Bounds().Dy() != h {
		u.comfortImg = image.NewRGBA(image.Rect(0, 0, w, h))
	} else {
		clear(u.comfortImg.Pix)
	}

	slot := d.Pitch.SlotMM()
	if u.comfortPageNo {
		text := fmt.Sprintf("- %d -", d.Current+1)
		n := len([]rune(text))
		firstX := units.PaperWidthMM/2 - float64(n-1)/2*slot
		u.stampChrome(d, text, firstX, d.Margins.Top*0.60, -1)
	}
	// Top-right corner, so the eye can drift away from it in flow. Degrades
	// silently in a shallow margin rather than crowding the text area.
	if u.comfortWordCount && d.Margins.Top >= 4.5 {
		pg, tot := d.WordCounts()
		u.comfortWC = [2]int{pg, tot}
		line1 := fmt.Sprintf("%d / %d", pg, tot)
		rightEdge := units.PaperWidthMM - d.Margins.Right
		firstX := func(s string) float64 {
			return rightEdge - slot/2 - float64(len([]rune(s))-1)*slot
		}
		u.stampChrome(d, line1, firstX(line1), d.Margins.Top*0.42, -2)
		if d.Margins.Top >= 8 {
			line2 := dashRule(len([]rune(line1)))
			u.stampChrome(d, line2, firstX(line2), d.Margins.Top*0.42+units.BaseLineMM/2, -3)
		}
	}
	if u.dankOn {
		// Remap the band's dark ink to light; the transparent background stays
		// transparent (alpha passthrough), so it composites over dark paper.
		dankifyRect(u.comfortImg, u.comfortImg, u.comfortImg.Bounds())
	}
	return true
}

// stampChrome types text left to right into the band, the first glyph centred
// on firstXMM, at baseline yMM, in the document's current hand.
func (u *ui) stampChrome(d *page.Doc, text string, firstXMM, yMM float64, row int) {
	slot := d.Pitch.SlotMM()
	for i, r := range []rune(text) {
		if r == ' ' {
			continue
		}
		rec := page.StrikeRec{
			Rune: r,
			XMM:  firstXMM + float64(i)*slot,
			YMM:  yMM,
			App:  d.ChromeStrike(r, row, i),
		}
		if err := u.renderer.StampFlat(u.comfortImg, rec, d.Pitch); err != nil {
			fyne.LogError("comfort stamp", err)
		}
	}
}

// dashRule builds an "- - - -" flourish of the given rune length.
func dashRule(n int) string {
	b := make([]rune, n)
	for i := range b {
		if i%2 == 0 {
			b[i] = '-'
		} else {
			b[i] = ' '
		}
	}
	return string(b)
}

// toggleComfortPageNo and toggleComfortWordCount flip the two comforts, persist
// the choice, and repaint.
func (u *ui) toggleComfortPageNo() {
	u.comfortPageNo = !u.comfortPageNo
	u.prefs.SetBool("comfortPageNo", u.comfortPageNo)
	u.updateComfort()
	u.refreshMenuChecks()
}

func (u *ui) toggleComfortWordCount() {
	u.comfortWordCount = !u.comfortWordCount
	u.prefs.SetBool("comfortWordCount", u.comfortWordCount)
	if u.comfortWordCount {
		u.startComfortTicker()
	} else {
		u.stopComfortTicker()
		u.comfortWC = [2]int{-1, -1} // next enable must render, whatever the counts
	}
	u.updateComfort()
	u.refreshMenuChecks()
}
