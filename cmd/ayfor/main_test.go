package main

import (
	"bytes"
	"image"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/render"
	"github.com/lajosnagyuk/ayfor/internal/session"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

// buildTestUI creates a ui with a real session and renderer but no window
// or canvas objects, so bitmap/LRU logic can be exercised headlessly (this
// package needs a real display to build the GUI parts, but bitmap(),
// touchBitmap() and evictOldBitmaps() only touch sess/renderer/maps).
func buildTestUI(t *testing.T) *ui {
	t.Helper()
	r, err := render.New(2) // small scale: fast, still a distinct bitmap per page
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.strike")
	sess, err := session.New(path, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &ui{
		sess:     sess,
		renderer: r,
		bitmaps:  map[int]*image.RGBA{},
		stamped:  map[int]int{},
	}
}

// typePage writes distinct text on the current sheet then starts a new
// one, so each page renders to a different bitmap.
func typePage(t *testing.T, u *ui, text string) {
	t.Helper()
	for _, r := range text {
		if _, err := u.sess.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := u.sess.NewSheet(); err != nil {
		t.Fatal(err)
	}
}

// TestBitmapLRUBoundsResidentSet is the headless check that the bitmap cache
// is bounded: flip through far more pages than maxResidentBitmaps and confirm
// the resident set never grows unbounded.
func TestBitmapLRUBoundsResidentSet(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()

	pages := maxResidentBitmaps * 3
	for i := range pages {
		typePage(t, u, string(rune('A'+i%26))+"page")
	}
	if got := len(u.sess.Doc.Pages); got != pages+1 {
		t.Fatalf("expected %d pages (trailing blank sheet), got %d", pages+1, got)
	}

	// Simulate flipping forward through every page, as a human paging
	// through a long manuscript would, and re-visiting the current page
	// after every flip the way after() does.
	for idx := 0; idx < len(u.sess.Doc.Pages); idx++ {
		u.bitmap(idx)
		u.bitmap(idx) // second touch: must not double-insert into the LRU

		if len(u.bitmaps) > maxResidentBitmaps {
			t.Fatalf("after visiting page %d: resident set is %d bitmaps, want <= %d", idx, len(u.bitmaps), maxResidentBitmaps)
		}
		if len(u.bitmapLRU) != len(u.bitmaps) {
			t.Fatalf("after visiting page %d: LRU list has %d entries but %d bitmaps are cached (stale/duplicate LRU entries)", idx, len(u.bitmapLRU), len(u.bitmaps))
		}
		seen := map[int]bool{}
		for _, v := range u.bitmapLRU {
			if seen[v] {
				t.Fatalf("bitmapLRU has duplicate entry for page %d after visiting page %d", v, idx)
			}
			seen[v] = true
		}
		if _, ok := u.bitmaps[idx]; !ok {
			t.Fatalf("page %d was just visited but is not resident (evicted the page being shown)", idx)
		}
	}
}

// TestBitmapReRenderIsByteIdentical confirms the premise the eviction
// policy relies on: re-rendering an evicted page produces the exact same
// bitmap as the first render, so evicting it is invisible to the owner.
func TestBitmapReRenderIsByteIdentical(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()
	typePage(t, u, "the quick brown fox")
	typePage(t, u, "jumps over the lazy dog")

	first := u.bitmap(0)
	firstBytes := append([]byte(nil), first.Pix...)

	// Force eviction of page 0 by visiting more pages than the cache
	// holds, then request page 0 again.
	for range maxResidentBitmaps + 1 {
		typePage(t, u, "filler")
		u.bitmap(len(u.sess.Doc.Pages) - 1)
	}
	if _, stillResident := u.bitmaps[0]; stillResident {
		t.Fatal("test setup bug: page 0 should have been evicted by now")
	}

	second := u.bitmap(0)
	if !bytes.Equal(firstBytes, second.Pix) {
		t.Fatal("re-rendered bitmap for an evicted page differs from the original render")
	}
}

// TestBitmapStampedResetsOnEviction guards against a narrower bug: after()
// uses u.stamped[idx] to know which strikes are already baked into the
// resident bitmap. If eviction drops the bitmap but not the stamped count,
// a re-rendered (blank-relative) bitmap would silently skip re-stamping
// strikes that are actually already fully rendered by RenderPage, or -
// worse - skip strikes that aren't. Confirm both maps evict together.
func TestBitmapStampedResetsOnEviction(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()
	typePage(t, u, "hello")
	u.bitmap(0)
	if _, ok := u.stamped[0]; !ok {
		t.Fatal("stamped[0] should be set after bitmap(0)")
	}

	for range maxResidentBitmaps + 1 {
		typePage(t, u, "filler")
		u.bitmap(len(u.sess.Doc.Pages) - 1)
	}

	_, bitmapResident := u.bitmaps[0]
	_, stampedResident := u.stamped[0]
	if bitmapResident || stampedResident {
		t.Fatalf("expected both bitmaps[0] and stamped[0] evicted together, got bitmap=%v stamped=%v", bitmapResident, stampedResident)
	}
}

// TestGuardBlocksMenuIntentsBehindModal pins the fix for the modal side
// door: every Cmd shortcut lives on a menu item, so the menu guard must
// refuse document-touching actions while a blocking dialog is up, exactly
// like the canvas key handlers do. Before the fix, Cmd+N behind a
// disk-full dialog fed a sheet into the append-only manuscript.
func TestGuardBlocksMenuIntentsBehindModal(t *testing.T) {
	u := &ui{}
	calls := 0
	action := u.guard(func() { calls++ })

	action()
	if calls != 1 {
		t.Fatal("guard must pass the action through with no modal and no replay")
	}

	u.pushModal()
	action()
	if calls != 1 {
		t.Fatal("guard let a menu intent through while a modal dialog was open")
	}

	// Nested dialogs: closing the inner one must not unguard the outer.
	u.pushModal()
	u.popModal()
	action()
	if calls != 1 {
		t.Fatal("closing a nested dialog unguarded the outer one (bool, not counter)")
	}

	u.popModal()
	action()
	if calls != 2 {
		t.Fatal("guard must unblock once every dialog is closed")
	}

	// popModal on a balanced state must not go negative and wedge the guard.
	u.popModal()
	action()
	if calls != 3 {
		t.Fatal("an unbalanced popModal wedged the guard shut")
	}

	u.replay = &replayRun{}
	action()
	if calls != 3 {
		t.Fatal("guard let a menu intent through during replay")
	}
}

// TestHumanGap pins the interstitial wording across the ranges.
func TestHumanGap(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{9 * time.Second, "9 seconds pass"},
		{1 * time.Second, "a moment passes"},
		{5 * time.Minute, "5 minutes pass"},
		{90 * time.Second, "90 seconds pass"},
		{3 * time.Hour, "3 hours pass"},
		{19 * 24 * time.Hour, "19 days pass"},
		{75 * 24 * time.Hour, "2 months pass"},
		{800 * 24 * time.Hour, "2 years pass"},
	}
	for _, c := range cases {
		if got := humanGap(c.d); got != c.want {
			t.Errorf("humanGap(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestA4WindowSizeForHeight(t *testing.T) {
	got := a4ContentSizeForHeight(1000)
	wantAspect := float64(units.PaperWidthMM / units.PaperHeightMM)
	gotAspect := float64(got.Width / got.Height)
	if math.Abs(gotAspect-wantAspect) > 0.0001 {
		t.Fatalf("A4 content aspect = %.6f, want %.6f", gotAspect, wantAspect)
	}
	if got.Height != 1000 {
		t.Fatalf("A4 content height = %v, want 1000", got.Height)
	}

	got = a4ContentSizeForHeight(0)
	if got.Height != defaultWindowHeight {
		t.Fatalf("A4 content fallback height = %v, want %v", got.Height, defaultWindowHeight)
	}
}

func TestScaleFromScreenFactorsChoosesLargestValidScale(t *testing.T) {
	got := scaleFromScreenFactors("eDP-1=2;HDMI-A-1=1.25;bad=x")
	if got != "2" {
		t.Fatalf("scaleFromScreenFactors() = %q, want 2", got)
	}
}

func TestScaleFromEnvRejectsInvalidValues(t *testing.T) {
	for _, v := range []string{"", " ", "nope", "0", "-1"} {
		if got := scaleFromEnv(v); got != "" {
			t.Fatalf("scaleFromEnv(%q) = %q, want empty", v, got)
		}
	}
	if got := scaleFromEnv(" 1.5 "); got != "1.5" {
		t.Fatalf("scaleFromEnv() = %q, want 1.5", got)
	}
}

func TestConfigureDesktopScale(t *testing.T) {
	tests := []struct {
		name string
		goos string
		env  []string
		want string
	}{
		{
			name: "explicit app override wins",
			goos: "linux",
			env:  []string{"AYFOR_UI_SCALE=2", "QT_SCALE_FACTOR=1.25"},
			want: "2",
		},
		{
			name: "existing fyne scale is respected",
			goos: "linux",
			env:  []string{"FYNE_SCALE=1.5", "AYFOR_UI_SCALE=2"},
			want: "",
		},
		{
			name: "linux qt scale factor is used",
			goos: "linux",
			env:  []string{"XDG_CURRENT_DESKTOP=KDE", "QT_SCALE_FACTOR=2"},
			want: "2",
		},
		{
			name: "kde screen factor is used",
			goos: "linux",
			env:  []string{"XDG_CURRENT_DESKTOP=KDE", "QT_SCREEN_SCALE_FACTORS=eDP-1=1.5;DP-1=2"},
			want: "2",
		},
		{
			name: "non-kde linux ignores qt scale factor",
			goos: "linux",
			env:  []string{"XDG_CURRENT_DESKTOP=GNOME", "QT_SCALE_FACTOR=2"},
			want: "auto",
		},
		{
			name: "linux fallback asks fyne for dpi auto detect",
			goos: "linux",
			env:  nil,
			want: "auto",
		},
		{
			name: "windows leaves platform scale alone",
			goos: "windows",
			env:  []string{"QT_SCALE_FACTOR=2"},
			want: "",
		},
		{
			name: "mac explicit app override still works",
			goos: "darwin",
			env:  []string{"AYFOR_UI_SCALE=1.5"},
			want: "1.5",
		},
		{
			name: "mac leaves platform scale alone by default",
			goos: "darwin",
			env:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			configureDesktopScaleForGOOS(tt.goos, tt.env, func(key, val string) error {
				if key == "FYNE_SCALE" {
					got = val
				}
				return nil
			})
			if got != tt.want {
				t.Fatalf("FYNE_SCALE = %q, want %q", got, tt.want)
			}
		})
	}
}
