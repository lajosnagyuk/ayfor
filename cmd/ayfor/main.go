// Command ayfor is the typewriter: an A4 sheet, one font, no delete.
//
// The document/format/model logic lives in the tested internal packages
// (session, page, machine, render, export); this package is the Fyne
// shell around them - keyboard in, bitmap out, menus, plus the
// display-only state that belongs to a window and not a document: the
// rendered-sheet LRU cache, the dank-mode remap buffers, and the comfort
// chrome. Those parts have headless tests (main_test.go, dank_test.go,
// comfort_test.go); the window layer itself is verified on macOS by
// keystroke injection and the owner's eyes - it needs a GUI toolchain
// (Fyne/cgo) to even build.
package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"

	"github.com/lajosnagyuk/ayfor/internal/bell"
	"github.com/lajosnagyuk/ayfor/internal/export"
	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/keygate"
	"github.com/lajosnagyuk/ayfor/internal/machine"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/render"
	"github.com/lajosnagyuk/ayfor/internal/session"
	"github.com/lajosnagyuk/ayfor/internal/sound"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

const renderScale = 8.0 // px per mm; A4 -> 1680 x 2376 backing bitmap

// maxResidentBitmapBytes bounds the rendered-sheet cache by MEMORY, not
// sheet count, so a renderScale change cannot silently multiply the
// budget. Without a cap, a long manuscript keeps every page ever visited
// resident forever - a 300-page draft would pin ~4.8 GB just for bitmaps.
// Rendering is deterministic (same strikes -> same bitmap, see
// render.RenderPage), so evicting and re-rendering on the rare
// flip-back is invisible to the owner, a one-time ~35 ms hitch instead of
// permanent memory. 128 MB buys ~8 sheets at renderScale 8 - generous for
// an app about one sheet of paper.
const maxResidentBitmapBytes = 128 << 20

// maxResidentBitmaps is the byte budget expressed in whole sheets (RGBA,
// 4 bytes/px), floored at 2 so the current page and one neighbour always
// fit.
var maxResidentBitmaps = max(2, maxResidentBitmapBytes/
	(int(units.PaperWidthMM*renderScale)*int(units.PaperHeightMM*renderScale)*4))

type ui struct {
	win      fyne.Window
	sess     *session.Session
	renderer *render.Renderer

	bg       *canvas.Rectangle // fills the window behind the sheet (no hard border)
	paper    *canvas.Image
	baseline *canvas.Rectangle
	notch    *canvas.Rectangle
	dim      *canvas.Rectangle // replay interstitial scrim
	gapText  *canvas.Text      // replay "time passes" line
	comfort  *canvas.Image     // display-only top-margin chrome (page no, word count)
	layout   *paperLayout

	replay *replayRun // nil unless a replay is running

	mainMenu      *fyne.MainMenu // kept so checkmark toggles refresh in place instead of rebuilding
	menuSound     *fyne.MenuItem
	menuPageNo    *fyne.MenuItem
	menuWordCount *fyne.MenuItem
	menuDank      *fyne.MenuItem

	lastTitle string // last title actually set; setTitle skips native calls when unchanged

	bitmaps      map[int]*image.RGBA // page index -> rendered sheet
	stamped      map[int]int         // page index -> strikes already on the bitmap
	bitmapLRU    []int               // page indices, least- to most-recently used
	lastCRFull   bool                // previous Return hit the page bottom
	lastCRFullAt [2]int              // (page, yHalf) where it did, so a nav/flip since then voids it
	gate         *keygate.Gate       // held keys strike once, like hammers

	prefs      fyne.Preferences
	soundOn    bool
	player     *sound.Player // nil until hammer sound is first enabled
	soundErr   error         // set if opening the audio device failed; do not retry
	modalDepth int           // blocking dialogs open; > 0 swallows keystrokes and menu intents

	// closing: the window is tearing down; replay must not touch the UI.
	// A plain bool on purpose - it is written in the close intercept and
	// read in endReplay, both of which run on the Fyne thread (endReplay
	// only ever runs inside fyne.DoAndWait). An atomic here would imply a
	// cross-goroutine access that does not exist.
	closing bool

	comfortPageNo    bool          // show "- N -" page number in the top margin
	comfortWordCount bool          // show a running page/document word count
	comfortImg       *image.RGBA   // reused band bitmap for the chrome overlay
	comfortPage      int           // page index the band currently shows (-2 = none)
	comfortStop      chan struct{} // stops the word-count recalc ticker (nil = not running)
	comfortWC        [2]int        // (page, doc) counts last stamped; a tick that matches skips the restamp

	dankOn   bool        // dark view: remap paper+ink to a dark palette on the glass
	dankImg  *image.RGBA // reused dark-remapped copy of the shown page
	dankPage int         // page index dankImg currently holds (-1 = none/invalid)
}

func main() {
	a := app.NewWithID("io.ayfor.app")
	w := a.NewWindow("ayfor")

	r, err := render.New(renderScale)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ayfor:", err)
		os.Exit(1)
	}

	sess, err := newUntitledSession()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ayfor:", err)
		os.Exit(1)
	}

	u := &ui{
		win:      w,
		sess:     sess,
		renderer: r,
		bitmaps:  map[int]*image.RGBA{},
		stamped:  map[int]int{},
		gate:     keygate.New(),
		prefs:    a.Preferences(),
	}
	if u.prefs.BoolWithFallback("hammerSound", false) {
		u.enableSound()
	}
	u.comfortPageNo = u.prefs.BoolWithFallback("comfortPageNo", false)
	u.comfortWordCount = u.prefs.BoolWithFallback("comfortWordCount", false)
	u.dankOn = u.prefs.BoolWithFallback("dankMode", false)
	u.dankPage = -1
	// The writer's hand and the machine's wear carry between documents
	// (mood and sobriety do not). Apply the remembered values to the fresh
	// draft as SET_ events, so the file stays self-contained and honest.
	if t := u.prefs.IntWithFallback("touch", 100); t != 100 && t > 0 && t < 256 {
		if _, err := u.sess.SetTouch(uint8(t)); err != nil {
			fyne.LogError("apply remembered touch", err)
		}
	}
	if c := u.prefs.IntWithFallback("condition", 100); c != 100 && c > 0 && c < 256 {
		if _, err := u.sess.SetCondition(uint8(c)); err != nil {
			fyne.LogError("apply remembered condition", err)
		}
	}

	u.bg = canvas.NewRectangle(lightGround)
	u.paper = canvas.NewImageFromImage(u.displayImage(u.sess.Doc.Current))
	u.paper.FillMode = canvas.ImageFillContain
	u.baseline = canvas.NewRectangle(guideColor)
	u.notch = canvas.NewRectangle(notchColor)
	u.dim = canvas.NewRectangle(withAlpha(dimColor, 0))
	u.dim.Hide()
	u.gapText = canvas.NewText("", withAlpha(gapTextColor, 0))
	u.gapText.TextStyle = fyne.TextStyle{Monospace: true}
	u.gapText.Alignment = fyne.TextAlignCenter
	u.gapText.Hide()
	u.comfort = canvas.NewImageFromImage(nil)
	u.comfort.FillMode = canvas.ImageFillStretch
	u.comfort.Hide()
	u.comfortPage = -2
	u.layout = &paperLayout{ui: u}

	w.SetContent(container.New(u.layout, u.bg, u.paper, u.comfort, u.baseline, u.notch, u.dim, u.gapText))
	// Unpadded: Fyne's default padded canvas insets the content by the theme
	// padding and shows the THEME background in the exposed ring - a hard
	// dark border around the paper-coloured ground on a dark system theme.
	// The ground fill (u.bg) should reach the window edge.
	w.SetPadded(false)
	// A touch wider than A4-contain needs: the extra width is breathing
	// room for the file dialogs (which are portrait-cramped otherwise) and
	// costs only a little grey margin either side of the sheet.
	w.Resize(fyne.NewSize(820, 1000))
	u.refreshTitle()

	u.buildMenu()
	u.bindKeys()
	u.applyDankChrome()
	u.comfortWC = [2]int{-1, -1}
	u.updateComfort()
	if u.comfortWordCount {
		u.startComfortTicker()
	}

	// Losing focus (Cmd+Tab away) can drop a KeyUp for a held key; clear the
	// gate so no key is left stuck "held" with an orphaned press.
	a.Lifecycle().SetOnExitedForeground(func() {
		fyne.Do(u.gate.Reset)
	})

	w.SetCloseIntercept(func() {
		u.stopReplay()
		u.stopComfortTicker()
		// A failed final flush (full disk) keeps the session live: surface it
		// and abort the quit so the user can recover (Save As to a working
		// volume rebuilds a fresh writer) instead of losing the buffered tail
		// silently.
		if err := u.sess.Close(); err != nil {
			u.showError(fmt.Errorf("could not finish saving - your last few seconds are unsaved; try Save As to another disk, then quit: %w", err))
			return
		}
		// Committed to closing: a replay goroutine still winding down must
		// now skip its UI restore (endReplay) rather than touch a dead canvas.
		u.closing = true
		if u.player != nil {
			u.player.Close()
		}
		w.Close()
	})
	w.ShowAndRun()
}

// ayforDir is ~/Documents/ayfor, the home for saved and exported work.
func ayforDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Documents", "ayfor")
}

// draftsDir is where new documents are durably written until named.
func draftsDir() string {
	if base := ayforDir(); base != "" {
		return filepath.Join(base, "drafts")
	}
	return ""
}

// textImportExts are the file kinds Load will type in as a machine (see
// importer): plain text in any of its common dresses. A typewriter has
// no notion of markup, so a .md file is typed verbatim - hashes,
// asterisks and all - which is exactly the honest thing to do.
var textImportExts = []string{".txt", ".md", ".markdown", ".text", ".rst", ".org"}

func isTextImport(ext string) bool {
	ext = strings.ToLower(ext)
	for _, e := range textImportExts {
		if ext == e {
			return true
		}
	}
	return false
}

// newUntitledSession starts a draft. The file is durably written from
// the first keystroke; "saving" later just renames it out of drafts/.
func newUntitledSession() (*session.Session, error) {
	dir := draftsDir()
	if dir == "" {
		return nil, fmt.Errorf("cannot locate home directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now()
	path := filepath.Join(dir, now.Format("2006-01-02-150405")+".strike")
	return session.New(path, format.DeriveSeed(now.UnixNano()), nil)
}

// isDraft reports whether the session still lives in the drafts folder.
func isDraft(path string) bool {
	return filepath.Base(filepath.Dir(path)) == "drafts"
}

func (u *ui) refreshTitle() {
	name := filepath.Base(u.sess.Path)
	title := "ayfor - " + name
	if isDraft(u.sess.Path) {
		title = "ayfor - Draft (" + strings.TrimSuffix(name, ".strike") + ") - Cmd+S to name it"
	}
	d := u.sess.Doc
	if len(d.Pages) > 1 && d.Current >= 0 {
		title += fmt.Sprintf(" - sheet %d/%d", d.Current+1, len(d.Pages))
	}
	if p := currentPage(d); p != nil && p.Tossed {
		title += " - in the bin"
	}
	u.setTitle(title)
}

// setTitle sets the window title only when it changed: refreshTitle runs
// after every intent, and the title only actually moves on a flip, a
// page-count change, a toss or a rename - not per keystroke.
func (u *ui) setTitle(title string) {
	if title == u.lastTitle {
		return
	}
	u.lastTitle = title
	u.win.SetTitle(title)
}

// saveAsDialog names the always-saved draft: a rename, nothing more.
func (u *ui) saveAsDialog() {
	fd := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil || wc == nil {
			return
		}
		target := wc.URI().Path()
		wc.Close()
		if strings.ToLower(filepath.Ext(target)) != ".strike" {
			target += ".strike"
		}
		// The save dialog created an empty placeholder at the chosen
		// path; remove it so Rename's no-overwrite guard sees the
		// user's real intent.
		if fi, statErr := os.Stat(target); statErr == nil && fi.Size() == 0 {
			os.Remove(target)
		}
		if err := u.sess.Rename(target); err != nil {
			u.showError(err)
			return
		}
		u.refreshTitle()
	}, u.win)
	fd.SetFileName(strings.TrimSuffix(filepath.Base(u.sess.Path), ".strike") + ".strike")
	u.locateDialog(fd, ayforDir())
	fd.Show()
}

// locateDialog gives a file dialog a comfortable size (most of the
// window, which by default is portrait and would otherwise show a cramped
// file list) and starts it in dir, so the owner's work is not several
// clicks away from wherever the process happened to launch.
func (u *ui) locateDialog(fd *dialog.FileDialog, dir string) {
	if sz := u.win.Canvas().Size(); sz.Width > 0 {
		fd.Resize(fyne.NewSize(sz.Width*0.95, sz.Height*0.92))
	}
	if dir == "" {
		return
	}
	// Best-effort: an unreadable dir just leaves the default location.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if uri, err := storage.ListerForURI(storage.NewFileURI(dir)); err == nil {
		fd.SetLocation(uri)
	}
}

// bitmap returns (rendering if needed) the sheet image for a page index.
func (u *ui) bitmap(idx int) *image.RGBA {
	if img, ok := u.bitmaps[idx]; ok {
		u.touchBitmap(idx)
		return img
	}
	var img *image.RGBA
	if idx >= 0 && idx < len(u.sess.Doc.Pages) {
		p := u.sess.Doc.Pages[idx]
		var err error
		img, err = u.renderer.RenderPage(p, u.sess.Doc.Pitch, u.sess.Doc.PaperSeed(idx))
		if err != nil {
			// Fall through to a blank sheet rather than crash the
			// typewriter; the failure is in the log, the manuscript on
			// disk is untouched.
			fyne.LogError("render page", err)
			img = nil
		} else {
			u.stamped[idx] = len(p.Strikes)
		}
	}
	if img == nil {
		img = u.renderer.NewPage(u.sess.Doc.PaperSeed(idx))
		u.stamped[idx] = 0
	}
	u.bitmaps[idx] = img
	u.touchBitmap(idx)
	u.evictOldBitmaps()
	return img
}

// touchBitmap marks idx as most recently used.
func (u *ui) touchBitmap(idx int) {
	for i, v := range u.bitmapLRU {
		if v == idx {
			u.bitmapLRU = append(u.bitmapLRU[:i], u.bitmapLRU[i+1:]...)
			break
		}
	}
	u.bitmapLRU = append(u.bitmapLRU, idx)
}

// evictOldBitmaps drops the least-recently-used rendered sheets once the
// resident set exceeds maxResidentBitmaps. Evicted pages simply re-render
// (deterministically, byte-identically) the next time they're shown.
func (u *ui) evictOldBitmaps() {
	for len(u.bitmapLRU) > maxResidentBitmaps {
		oldest := u.bitmapLRU[0]
		u.bitmapLRU = u.bitmapLRU[1:]
		delete(u.bitmaps, oldest)
		delete(u.stamped, oldest)
	}
}

// applyResult processes the result of any intent: sound, ink, guide.
func (u *ui) applyResult(res page.Result, err error) {
	if err != nil {
		u.showError(err)
		return
	}
	if res.Bell {
		u.ring()
	}
	d := u.sess.Doc
	// Stamp any strikes not yet on the current page's bitmap.
	if p := currentPage(d); p != nil {
		img := u.bitmap(d.Current)
		for i := u.stamped[d.Current]; i < len(p.Strikes); i++ {
			rec := p.Strikes[i]
			if err := u.renderer.Stamp(img, rec, d.Pitch); err != nil {
				// The strike is durably logged either way; a glyph that
				// cannot rasterize must not take the session down.
				fyne.LogError("stamp strike", err)
			}
			// Keep the dark view current for just the stamped area, so the
			// per-keystroke path never full-remaps the page.
			u.dankifyDirty(d.Current, img, strikeBox(rec))
			if u.soundOn && u.player != nil {
				u.player.Strike(d.Current, rec.Cell.YHalf, rec.Cell.Col, i, rec.App.Ink)
			}
		}
		u.stamped[d.Current] = len(p.Strikes)
	}
	u.paper.Image = u.displayImage(d.Current)
	u.paper.Refresh()
	u.layout.refresh()
	u.refreshTitle()
	u.comfortAfterPaint() // repaint chrome when the shown page changed
}

func currentPage(d *page.Doc) *page.Page {
	if d.Current < 0 || d.Current >= len(d.Pages) {
		return nil
	}
	return d.Pages[d.Current]
}

// activeDoc is the document the glass shows right now: the replay's while
// one is performing, the live session's otherwise. The guides follow it.
func (u *ui) activeDoc() *page.Doc {
	if u.replay != nil {
		return u.replay.doc
	}
	return u.sess.Doc
}

func (u *ui) bindKeys() {
	c := u.win.Canvas()

	// Physical key state feeds the gate: genuine presses arrive here,
	// OS autorepeat does not (Fyne's driver skips KeyDown for repeats),
	// so a typed event with no pending press is a repeat and is refused
	// - a held key keeps its hammer on the platen, one impression.
	if dc, ok := c.(desktop.Canvas); ok {
		dc.SetOnKeyDown(func(e *fyne.KeyEvent) { u.gate.KeyDown(string(e.Name)) })
		dc.SetOnKeyUp(func(e *fyne.KeyEvent) { u.gate.KeyUp(string(e.Name)) })
	}

	c.SetOnTypedRune(func(r rune) {
		if u.replay != nil || u.modalDepth > 0 {
			return // performing, or a dialog is up; hands off the platen
		}
		if r < ' ' {
			return
		}
		if !u.gate.AllowRune() {
			return // autorepeat of a held key
		}
		if r == ' ' {
			u.applyResult(u.sess.Space())
			return
		}
		u.applyResult(u.sess.Strike(r))
	})
	c.SetOnTypedKey(func(e *fyne.KeyEvent) {
		if u.replay != nil {
			if e.Name == fyne.KeyEscape {
				u.stopReplay()
			}
			return
		}
		if u.modalDepth > 0 {
			return // a dialog is up; do not append to the document behind it
		}
		switch e.Name {
		case fyne.KeyReturn, fyne.KeyEnter:
			if !u.gate.AllowKey(string(e.Name)) {
				return
			}
			res, err := u.sess.Return()
			if err == nil && res.PageFull {
				pos := [2]int{u.sess.Doc.Current, u.sess.Doc.YHalf}
				if u.lastCRFull && u.lastCRFullAt == pos {
					// Second Return against the same bottom, with nothing
					// moved between: feed a sheet.
					u.lastCRFull = false
					u.applyResult(u.sess.NewSheet())
					return
				}
				// First hit here (or the carriage moved / document changed
				// since the last one): warn, do not feed.
				u.lastCRFull = true
				u.lastCRFullAt = pos
				u.ring() // the second, end-of-paper bell
			} else {
				u.lastCRFull = false
			}
			u.applyResult(res, err)
		case fyne.KeyBackspace, fyne.KeyDelete:
			if !u.gate.AllowKey(string(e.Name)) {
				return
			}
			u.applyResult(u.sess.Back())
		}
	})

	// All Cmd shortcuts live on the menu items (buildMenu): macOS turns
	// them into native key equivalents that both display in the menu
	// and trigger the action, so nothing is bound on the canvas twice.
}

// pushModal/popModal track open blocking dialogs. A depth counter, not a
// bool: dialogs can nest (an error surfacing over a confirm), and closing
// the inner one must not unguard the outer.
func (u *ui) pushModal() { u.modalDepth++ }
func (u *ui) popModal() {
	if u.modalDepth > 0 {
		u.modalDepth--
	}
}

// guard wraps a document-touching action so it is refused while a replay is
// performing or a blocking dialog is up. The canvas key handlers check the
// same two conditions; every Cmd shortcut lives on a menu item, so without
// this the menu would be a side door around the modal (a reflexive Cmd+N
// behind a disk-full dialog would append to the manuscript).
func (u *ui) guard(f func()) func() {
	return func() {
		if u.replay != nil || u.modalDepth > 0 {
			return
		}
		f()
	}
}

// showError displays a blocking error dialog and marks a modal open so the
// canvas key handlers and menu intents swallow input while it is up (Fyne's
// confirm/error dialogs do not focus a widget, so typing would otherwise
// reach the typewriter and mutate the document behind the modal).
func (u *ui) showError(err error) {
	u.pushModal()
	d := dialog.NewError(err, u.win)
	d.SetOnClosed(u.popModal)
	d.Show()
}

// confirm displays a blocking yes/no dialog with the same modal guard.
func (u *ui) confirm(title, message string, onYes func()) {
	u.pushModal()
	dialog.ShowConfirm(title, message, func(yes bool) {
		u.popModal()
		if yes {
			onYes()
		}
	}, u.win)
}

func (u *ui) confirmToss() {
	u.confirm("Scrunch this sheet?",
		"It goes to the bin inside the file - kept, never deleted.",
		func() { u.applyResult(u.sess.Toss()) })
}

// ensurePlayer opens the audio device on first use (the bell and the
// hammer sound share it) and remembers a failure forever: oto permits
// exactly one context per process, so a retry would fail with a
// misleading "context can only be created once" regardless of whether
// the device recovered. report says how a failure surfaces - a dialog
// when the user just asked for sound, a single log line when the bell
// tried to ring (a typewriter with a broken bell still types; it does
// not nag).
func (u *ui) ensurePlayer(report bool) *sound.Player {
	if u.player != nil {
		return u.player
	}
	if u.soundErr == nil {
		p, err := sound.NewPlayer()
		if err == nil {
			u.player = p
			return p
		}
		u.soundErr = err
		fyne.LogError("open audio device", err)
	}
	if report {
		u.showError(fmt.Errorf("hammer sound is unavailable for this session: %w", u.soundErr))
	}
	return nil
}

// ring sounds the margin bell through the shared mixer. Best effort.
func (u *ui) ring() {
	if p := u.ensurePlayer(false); p != nil {
		p.PlayPCM(bell.PCM())
	}
}

// enableSound turns strikes audible, opening the audio device if needed.
func (u *ui) enableSound() {
	if u.ensurePlayer(true) != nil {
		u.soundOn = true
	}
}

func (u *ui) toggleSound() {
	if u.soundOn {
		u.soundOn = false
	} else {
		u.enableSound()
	}
	u.prefs.SetBool("hammerSound", u.soundOn)
	u.refreshMenuChecks()
}

// menuItem builds a menu item carrying a keyboard shortcut, which macOS shows
// and triggers natively.
func menuItem(label string, key fyne.KeyName, mod fyne.KeyModifier, action func()) *fyne.MenuItem {
	item := fyne.NewMenuItem(label, action)
	item.Shortcut = &desktop.CustomShortcut{KeyName: key, Modifier: mod}
	return item
}

func (u *ui) buildMenu() {
	super := fyne.KeyModifierSuper

	// g blocks document-touching actions while a replay is performing or a
	// modal dialog is up; the keyboard is guarded the same way in bindKeys.
	g := u.guard

	file := fyne.NewMenu("File",
		menuItem("Load or import text...", fyne.KeyO, super, g(u.loadDialog)),
		menuItem("Save As...", fyne.KeyS, super, g(u.saveAsDialog)),
		menuItem("Export...", fyne.KeyE, super, g(u.exportDialog)),
		fyne.NewMenuItemSeparator(),
		menuItem("Replay", fyne.KeyR, super, g(u.startReplay)),
	)

	paperMenu := fyne.NewMenu("Paper",
		menuItem("New sheet", fyne.KeyN, super, g(func() { u.applyResult(u.sess.NewSheet()) })),
		menuItem("Previous sheet", fyne.KeyLeftBracket, super, g(func() { u.applyResult(u.sess.PagePrev()) })),
		menuItem("Next sheet", fyne.KeyRightBracket, super, g(func() { u.applyResult(u.sess.PageNext()) })),
		fyne.NewMenuItemSeparator(),
		menuItem("Scrunch and toss", fyne.KeyBackspace, super, g(u.confirmToss)),
	)

	carriageMenu := fyne.NewMenu("Carriage",
		menuItem("Carriage left", fyne.KeyLeft, super, g(func() { u.applyResult(u.sess.Back()) })),
		menuItem("Carriage right", fyne.KeyRight, super, g(func() { u.applyResult(u.sess.Space()) })),
		fyne.NewMenuItemSeparator(),
		menuItem("Paper down one line", fyne.KeyDown, super, g(func() { u.applyResult(u.sess.LineFeed()) })),
		menuItem("Paper down half line", fyne.KeyDown, super|fyne.KeyModifierShift, g(func() { u.applyResult(u.sess.HalfDown()) })),
		menuItem("Paper up half line", fyne.KeyUp, super|fyne.KeyModifierShift, g(func() { u.applyResult(u.sess.HalfUp()) })),
	)

	soundItem := fyne.NewMenuItem("Hammer sound", g(u.toggleSound))
	soundItem.Checked = u.soundOn

	pitch := func(v uint8) func() {
		return g(func() {
			u.applyResult(u.sess.SetPitch(v))
			u.rerenderAll()
		})
	}
	spacing := func(v uint8) func() {
		return g(func() { u.applyResult(u.sess.SetLineSpacing(v)) })
	}
	// Touch: the writer's hand. Affects future strikes only (already-typed
	// strikes keep the touch they were struck with), so no rerender. The
	// choice is remembered and applied to future new documents.
	touch := func(v uint8) func() {
		return g(func() {
			u.applyResult(u.sess.SetTouch(v))
			u.prefs.SetInt("touch", int(v))
		})
	}
	// Disposition and sobriety are transient states of the writer, not
	// remembered across documents - you do not sit down furious every
	// morning. Future strikes only, like touch.
	disposition := func(v uint8) func() {
		return g(func() { u.applyResult(u.sess.SetDisposition(v)) })
	}
	sobriety := func(v uint8) func() {
		return g(func() { u.applyResult(u.sess.SetSobriety(v)) })
	}
	machineMenu := fyne.NewMenu("Machine",
		fyne.NewMenuItem("Pica (10 cpi)", pitch(10)),
		fyne.NewMenuItem("Elite (12 cpi)", pitch(12)),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Single spacing", spacing(10)),
		fyne.NewMenuItem("1 1/2 spacing", spacing(15)),
		fyne.NewMenuItem("Double spacing", spacing(20)),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Margins: normal", g(u.marginsPreset(25, 20, 25, 25))),
		fyne.NewMenuItem("Margins: wide", g(u.marginsPreset(40, 35, 30, 30))),
		fyne.NewMenuItem("Margins: narrow", g(u.marginsPreset(15, 12, 15, 15))),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Bash the machine (rougher)", g(u.bashMachine)),
		fyne.NewMenuItem("Fix the machine (cleaner)", g(u.fixMachine)),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Replace ribbon", g(func() { u.applyResult(u.sess.NewRibbon()) })),
		fyne.NewMenuItemSeparator(),
		soundItem,
	)

	humanMenu := fyne.NewMenu("Human",
		fyne.NewMenuItem("Touch: light", touch(85)),
		fyne.NewMenuItem("Touch: medium", touch(100)),
		fyne.NewMenuItem("Touch: firm", touch(112)),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Disposition: composed", disposition(100)),
		fyne.NewMenuItem("Disposition: terse", disposition(140)),
		fyne.NewMenuItem("Disposition: furious", disposition(180)),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Sobriety: sober", sobriety(100)),
		fyne.NewMenuItem("Sobriety: merry", sobriety(140)),
		fyne.NewMenuItem("Sobriety: legless", sobriety(185)),
	)

	// Comfort: display-only chrome. Safe during replay (it never touches the
	// document), so it is deliberately not wrapped in the replay guard g.
	pageNoItem := fyne.NewMenuItem("Page number", u.toggleComfortPageNo)
	pageNoItem.Checked = u.comfortPageNo
	wordCountItem := fyne.NewMenuItem("Word count", u.toggleComfortWordCount)
	wordCountItem.Checked = u.comfortWordCount
	dankItem := fyne.NewMenuItem("Dank mode", u.toggleDank)
	dankItem.Checked = u.dankOn
	comfortMenu := fyne.NewMenu("Comfort", pageNoItem, wordCountItem, dankItem)

	u.menuSound, u.menuPageNo, u.menuWordCount, u.menuDank = soundItem, pageNoItem, wordCountItem, dankItem
	u.mainMenu = fyne.NewMainMenu(file, paperMenu, carriageMenu, machineMenu, humanMenu, comfortMenu)
	u.win.SetMainMenu(u.mainMenu)
}

// refreshMenuChecks updates the checkmarks in place. Rebuilding all six
// menus and re-registering the native menu bar per toggle wasted work and
// could visibly flicker an open menu.
func (u *ui) refreshMenuChecks() {
	if u.mainMenu == nil {
		return
	}
	u.menuSound.Checked = u.soundOn
	u.menuPageNo.Checked = u.comfortPageNo
	u.menuWordCount.Checked = u.comfortWordCount
	u.menuDank.Checked = u.dankOn
	u.mainMenu.Refresh()
}

// conditionStep is how much one Bash/Fix nudges the machine's wear.
const conditionStep = 0.2

// bashMachine and fixMachine nudge the machine's condition rougher or
// cleaner, stepping from wherever it currently sits and clamping to the
// model's range. Like every other trait this affects future strikes only
// - you cannot un-bang letters already on the page - and the setting is
// remembered so a new sheet keeps the machine as you left it.
func (u *ui) bashMachine() { u.stepCondition(+conditionStep) }
func (u *ui) fixMachine()  { u.stepCondition(-conditionStep) }

func (u *ui) stepCondition(delta float64) {
	next := machine.ClampCondition(u.sess.Doc.Condition() + delta)
	v := uint8(next*100 + 0.5)
	u.applyResult(u.sess.SetCondition(v))
	u.prefs.SetInt("condition", int(v))
}

func (u *ui) marginsPreset(l, r, t, b float64) func() {
	return func() {
		u.applyResult(u.sess.SetMargins(units.Margins{Left: l, Right: r, Top: t, Bottom: b}))
	}
}

// rerenderAll drops bitmap caches (pitch changes the die size).
func (u *ui) rerenderAll() {
	u.bitmaps = map[int]*image.RGBA{}
	u.stamped = map[int]int{}
	u.bitmapLRU = nil
	u.dankPage = -1 // bitmaps were cleared; the dark copy is stale
	u.paper.Image = u.displayImage(u.sess.Doc.Current)
	u.paper.Refresh()
	u.layout.refresh()
	u.comfortImg = nil // margins/pitch may have changed the band's size
	u.updateComfort()
}

func (u *ui) loadDialog() {
	fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil || rc == nil {
			return
		}
		path := rc.URI().Path()
		rc.Close()
		u.loadPath(path)
	}, u.win)
	fd.SetFilter(storage.NewExtensionFileFilter(append([]string{".strike"}, textImportExts...)))
	u.locateDialog(fd, draftsDir())
	fd.Show()
}

// sameFile reports whether a and b are the same file on disk (resolving
// symlinks and path-spelling differences via the inode). Returns false if
// either path cannot be stat'd (e.g. a not-yet-created import target).
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// uniqueStrikePath returns base if it is free, else base with a " (2)", " (3)"
// suffix that does not collide, so importing a text file whose ".strike" name
// already exists does not dead-end on the no-overwrite guard. The pick is
// Stat-based and so racy in principle; a file created in the gap loses to
// session.ImportText's O_EXCL open, which fails loudly rather than
// clobbering - the race costs an error dialog, never data.
func uniqueStrikePath(base string) string {
	if _, err := os.Stat(base); os.IsNotExist(err) {
		return base
	}
	stem := strings.TrimSuffix(base, ".strike")
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d).strike", stem, i)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func (u *ui) loadPath(path string) {
	// Loading the file that is already open would double-open it: session.Open
	// appends a fresh session marker on a second handle while the live
	// session's buffered tail is still unwritten, forking the file and its
	// hash chain. It is already on the glass, so just make sure it is flushed.
	if sameFile(path, u.sess.Path) {
		// The flush is the entire point of this branch; a failure (full
		// disk) must surface like every other flush failure does.
		if err := u.sess.Flush(); err != nil {
			u.showError(err)
		}
		return
	}

	ext := strings.ToLower(filepath.Ext(path))
	var next *session.Session
	var err error
	switch {
	case ext == ".strike":
		next, err = session.Open(path, nil)
	case isTextImport(ext):
		// Any plain-text format: type it in as a machine. The .strike is
		// written next to the source so the original text is untouched.
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			err = rerr
			break
		}
		out := uniqueStrikePath(strings.TrimSuffix(path, filepath.Ext(path)) + ".strike")
		seed := format.DeriveSeed(time.Now().UnixNano())
		next, err = session.ImportText(out, string(b), seed, nil)
	default:
		err = fmt.Errorf("cannot load %q: open a .strike file, or a text file (%s) to type in", filepath.Base(path), strings.Join(textImportExts, ", "))
	}
	if err != nil {
		u.showError(err)
		return
	}
	// The new document is open; close the old one. A failed final flush on
	// the old file (full disk) would otherwise lose its last few seconds
	// silently, so surface it - the switch still proceeds.
	if cerr := u.sess.Close(); cerr != nil {
		u.showError(fmt.Errorf("the previous document may not have fully saved: %w", cerr))
	}
	u.sess = next
	u.lastCRFull = false // fresh document: no pending end-of-paper Return
	u.rerenderAll()
	u.refreshTitle()
}

func (u *ui) exportDialog() {
	fd := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil || wc == nil {
			return
		}
		// Take the chosen path and close the dialog's own (empty) file: we
		// write atomically to that path ourselves, so a failed export leaves
		// no half-written file under the user's name.
		target := wc.URI().Path()
		wc.Close()
		d := u.sess.Doc
		var data []byte
		switch strings.ToLower(filepath.Ext(target)) {
		case ".txt":
			data = []byte(export.Text(d))
		case ".md":
			data = []byte(export.Markdown(d))
		case ".docx":
			data, err = export.DOCX(d)
		case ".pdf":
			data, err = export.PDF(d)
		default:
			err = fmt.Errorf("choose a .txt, .md, .docx or .pdf name")
		}
		if err == nil {
			err = export.AtomicWriteFile(target, data)
		}
		if err != nil {
			u.showError(err)
		}
	}, u.win)
	base := strings.TrimSuffix(filepath.Base(u.sess.Path), ".strike")
	fd.SetFileName(base + ".pdf")
	u.locateDialog(fd, ayforDir())
	fd.Show()
}
