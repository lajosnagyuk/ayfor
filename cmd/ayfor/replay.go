package main

// Replay: play the open .strike file back in real time - every strike
// lands exactly where and how it did when typed (same model, same seed),
// at the rhythm it was typed. Long pauses are not served in real time:
// anything over gapShowMS is compressed into a fade-in/fade-out
// interstitial ("- 19 days pass -"), because the point is watching the
// hands work, not waiting out a weekend.
//
// Threading: the replay goroutine owns only pacing (sleeps, gap
// arithmetic). Every document mutation and canvas touch happens inside
// fyne.DoAndWait, so the replay doc is only ever accessed on the Fyne
// thread - the same thread that lays out the guides from it.

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/page"
)

// gapShowMS is the pause length above which replay stops waiting and
// shows the time-passes interstitial instead. Pauses below it play out in
// full: hesitation is part of the performance.
const gapShowMS = 8000

// interstitial fade timing.
const (
	gapFadeSteps = 12
	gapFadeStep  = 45 * time.Millisecond
	gapHold      = 900 * time.Millisecond
)

type replayRun struct {
	doc     *page.Doc
	img     *image.RGBA
	stamped int
	pageIdx int
	cancel  chan struct{}
	stopped bool // set on the Fyne thread; guards double-close of cancel

	// Dank state for this run, separate from the live page's cached remap:
	// dankImg mirrors img while dankSynced holds, maintained incrementally
	// (per-strike dirty boxes, like the live typing path) instead of
	// full-remapping ~4M pixels per event - imported files replay an event
	// every 40ms, and each full remap ran inside DoAndWait on the main
	// thread, which is what made Esc go unresponsive on dark replays.
	dankImg    *image.RGBA
	dankSynced bool
}

// startReplay begins playback of the session's file from its first event.
func (u *ui) startReplay() {
	if u.replay != nil {
		return
	}
	// The session buffers writes; the file on disk is up to 5 s stale
	// until flushed, and a replay must include the freshest keystrokes.
	if err := u.sess.Flush(); err != nil {
		u.showError(err)
		return
	}
	b, err := os.ReadFile(u.sess.Path)
	if err != nil {
		u.showError(err)
		return
	}
	f, err := format.Decode(b)
	if err != nil {
		u.showError(err)
		return
	}
	run := &replayRun{
		doc:     page.New(f.Header),
		pageIdx: -1,
		cancel:  make(chan struct{}),
	}
	u.replay = run
	u.setTitle("ayfor - replaying " + filepath.Base(u.sess.Path) + " - Esc stops")
	go u.replayLoop(run, f.Events)
}

// stopReplay cancels a running replay. Safe to call repeatedly; must run
// on the Fyne thread.
func (u *ui) stopReplay() {
	if u.replay == nil || u.replay.stopped {
		return
	}
	u.replay.stopped = true
	close(u.replay.cancel)
}

// endReplay restores the live document view. Runs on the Fyne thread.
// The replay run kept its own dank buffer, so the live page's cached
// remap (u.dankImg/u.dankPage) is still valid and serves immediately.
func (u *ui) endReplay() {
	u.replay = nil
	if u.closing {
		// The window is tearing down; the goroutine's deferred restore must
		// not touch a closing canvas or the just-closed session.
		return
	}
	u.dim.Hide()
	u.gapText.Hide()
	u.paper.Image = u.displayImage(u.sess.Doc.Current)
	u.paper.Refresh()
	u.layout.refresh()
	u.refreshTitle()
}

// replayLoop is the pacing goroutine.
func (u *ui) replayLoop(run *replayRun, events []format.Event) {
	defer fyne.DoAndWait(u.endReplay)

	wallKnown := false
	var wallNow int64

	for _, e := range events {
		if e.Op == format.OpSession {
			// Sessions carry wall time instead of a delta: the gap
			// between sessions is however long the machine sat idle.
			if wallKnown && e.WallUnixMS > wallNow+gapShowMS {
				if !u.showGap(run, time.Duration(e.WallUnixMS-wallNow)*time.Millisecond) {
					return
				}
			}
			wallNow = e.WallUnixMS
			wallKnown = true
		} else {
			wallNow += int64(e.DeltaMS)
			if e.DeltaMS > gapShowMS {
				if !u.showGap(run, time.Duration(e.DeltaMS)*time.Millisecond) {
					return
				}
			} else if e.DeltaMS > 0 {
				select {
				case <-run.cancel:
					return
				case <-time.After(time.Duration(e.DeltaMS) * time.Millisecond):
				}
			}
		}

		select {
		case <-run.cancel:
			return
		default:
		}
		fyne.DoAndWait(func() { u.replayApply(run, e) })
	}
}

// replayApply folds one event into the replay doc and paints the result.
// Runs on the Fyne thread.
func (u *ui) replayApply(run *replayRun, e format.Event) {
	if img := u.replayAdvance(run, e); img != nil {
		u.paper.Image = img
		u.paper.Refresh()
		u.layout.refresh()
	}
}

// replayAdvance folds one event into the replay doc, maintains the frame
// (and its incremental dank copy), and returns what the glass should show
// - nil when there is nothing to paint. Touches no canvas, so it is
// exercised headlessly by tests.
func (u *ui) replayAdvance(run *replayRun, e format.Event) image.Image {
	res := run.doc.Apply(e)
	if res.Bell {
		u.ring()
	}
	if e.Op == format.OpSetPitch {
		// Pitch changes the die every glyph on the page is rendered with, so
		// force a full re-render rather than stamping new strikes at the new
		// pitch onto a bitmap whose earlier glyphs used the old one - that
		// would diverge from how open/export render the same file.
		run.img = nil
	}
	d := run.doc
	if d.Current < 0 {
		return nil
	}
	fresh := false
	if d.Current != run.pageIdx || run.img == nil {
		img, err := u.renderer.RenderPage(d.Pages[d.Current], d.Pitch, d.PaperSeed(d.Current))
		if err != nil {
			fyne.LogError("replay render", err) // parity with the stamp path below
			return nil
		}
		run.img = img
		run.pageIdx = d.Current
		run.stamped = len(d.Pages[d.Current].Strikes)
		fresh = true
	} else if p := currentPage(d); p != nil {
		for i := run.stamped; i < len(p.Strikes); i++ {
			rec := p.Strikes[i]
			if err := u.renderer.Stamp(run.img, rec, d.Pitch); err != nil {
				fyne.LogError("replay stamp", err)
			}
			// Keep the run's dark copy current for just the stamped box, the
			// same bargain the live path makes.
			if u.dankOn && run.dankSynced && run.dankImg != nil && run.dankImg.Bounds() == run.img.Bounds() {
				dankifyRect(run.dankImg, run.img, strikeBox(rec))
			}
			if u.soundOn && u.player != nil {
				u.player.Strike(d.Current, rec.Cell.YHalf, rec.Cell.Col, i, rec.App.Ink)
			}
		}
		run.stamped = len(p.Strikes)
	}
	return u.replayDisplay(run, fresh)
}

// showGap fades the time-passes interstitial in and out, honouring
// cancellation. Returns false when the replay was cancelled mid-fade.
func (u *ui) showGap(run *replayRun, gap time.Duration) bool {
	label := "- " + humanGap(gap) + " -"
	fade := func(from, to float64) bool {
		for s := 0; s <= gapFadeSteps; s++ {
			f := from + (to-from)*float64(s)/gapFadeSteps
			select {
			case <-run.cancel:
				return false
			case <-time.After(gapFadeStep):
			}
			fyne.DoAndWait(func() { u.setGapOverlay(label, f) })
		}
		return true
	}
	if !fade(0, 1) {
		return false
	}
	select {
	case <-run.cancel:
		return false
	case <-time.After(gapHold):
	}
	if !fade(1, 0) {
		return false
	}
	fyne.DoAndWait(func() {
		u.dim.Hide()
		u.gapText.Hide()
	})
	return true
}

// setGapOverlay drives the interstitial at fade level f (0..1). Runs on
// the Fyne thread.
func (u *ui) setGapOverlay(label string, f float64) {
	u.gapText.Text = label
	u.gapText.Color = withAlpha(u.gapTextBase(), f)
	u.dim.FillColor = withAlpha(u.dimBase(), f)
	u.dim.Show()
	u.gapText.Show()
	u.dim.Refresh()
	u.gapText.Refresh()
	u.layout.refresh()
}

// humanGap words a pause the way a person would say it.
func humanGap(d time.Duration) string {
	n := func(v int64, one, many string) string {
		if v == 1 {
			return one
		}
		return fmt.Sprintf(many, v)
	}
	switch {
	case d < 2*time.Minute:
		return n(int64(d.Round(time.Second)/time.Second), "a moment passes", "%d seconds pass")
	case d < 2*time.Hour:
		return n(int64(d.Round(time.Minute)/time.Minute), "a minute passes", "%d minutes pass")
	case d < 48*time.Hour:
		return n(int64(d.Round(time.Hour)/time.Hour), "an hour passes", "%d hours pass")
	case d < 60*24*time.Hour:
		return n(int64(d/(24*time.Hour)), "a day passes", "%d days pass")
	case d < 730*24*time.Hour:
		return n(int64(d/(30*24*time.Hour)), "a month passes", "%d months pass")
	default:
		return n(int64(d/(365*24*time.Hour)), "a year passes", "%d years pass")
	}
}
