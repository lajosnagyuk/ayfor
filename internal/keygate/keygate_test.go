package keygate

import "testing"

func TestSinglePressSingleStrike(t *testing.T) {
	g := New()
	g.KeyDown("E")
	if !g.AllowRune() {
		t.Fatal("first strike must pass")
	}
	// Autorepeat: typed events with no new KeyDown.
	for i := range 10 {
		if g.AllowRune() {
			t.Fatalf("autorepeat rune %d slipped through", i)
		}
	}
	g.KeyUp("E")
	g.KeyDown("E")
	if !g.AllowRune() {
		t.Fatal("a fresh press after release must strike again")
	}
}

func TestRolloverTyping(t *testing.T) {
	// Fast typing: t down, h down, t up, e down, h up, e up - three
	// keystrokes, three strikes, in order.
	g := New()
	g.KeyDown("T")
	g.KeyDown("H")
	if !g.AllowRune() {
		t.Fatal("t must strike")
	}
	g.KeyUp("T")
	g.KeyDown("E")
	if !g.AllowRune() {
		t.Fatal("h must strike")
	}
	if !g.AllowRune() {
		t.Fatal("e must strike")
	}
	g.KeyUp("H")
	g.KeyUp("E")
	if g.AllowRune() {
		t.Fatal("no press left to back a fourth rune")
	}
}

func TestHeldReturnDoesNotRepeat(t *testing.T) {
	g := New()
	g.KeyDown("Return")
	if !g.AllowKey("Return") {
		t.Fatal("first return must pass")
	}
	for range 5 {
		if g.AllowKey("Return") {
			t.Fatal("held return repeated")
		}
	}
	g.KeyUp("Return")
	g.KeyDown("Return")
	if !g.AllowKey("Return") {
		t.Fatal("fresh return must pass")
	}
}

func TestAllowKeyConsumesOnlyItsOwnPress(t *testing.T) {
	// A pending letter press must not feed a repeating Backspace.
	g := New()
	g.KeyDown("A")
	g.KeyDown("BackSpace")
	if !g.AllowKey("BackSpace") {
		t.Fatal("backspace must pass")
	}
	if g.AllowKey("BackSpace") {
		t.Fatal("backspace repeat consumed the letter's press")
	}
	if !g.AllowRune() {
		t.Fatal("the letter's press must still back its rune")
	}
}

func TestModifiersDoNotFeedRunes(t *testing.T) {
	// Holding shift then a letter: the shift press must not become a
	// spare token that lets a letter autorepeat through.
	g := New()
	g.KeyDown("LeftShift")
	g.KeyDown("A")
	if !g.AllowRune() {
		t.Fatal("shifted letter must strike")
	}
	if g.AllowRune() {
		t.Fatal("autorepeat leaked via the modifier's press")
	}
}

func TestShortcutChordPressIsDiscardedOnRelease(t *testing.T) {
	// Cmd+E: the E press fires KeyDown but the rune never arrives
	// (consumed as a shortcut). Releasing E must clear it so a later
	// autorepeat cannot use it.
	g := New()
	g.KeyDown("LeftSuper")
	g.KeyDown("E")
	g.KeyUp("E")
	g.KeyUp("LeftSuper")
	if g.AllowRune() {
		t.Fatal("stale shortcut press leaked a rune")
	}
	g.KeyDown("E")
	if !g.AllowRune() {
		t.Fatal("normal typing must work after a shortcut")
	}
}

func TestDriverLevelRepeatOfKeyDownIsIgnored(t *testing.T) {
	// Defensive: if a platform ever delivers repeated KeyDown for a
	// held key, it must not mint extra presses.
	g := New()
	g.KeyDown("A")
	g.KeyDown("A")
	g.KeyDown("A")
	if !g.AllowRune() {
		t.Fatal("first strike must pass")
	}
	if g.AllowRune() {
		t.Fatal("repeated KeyDown minted an extra strike")
	}
}

func TestResetClearsStuckState(t *testing.T) {
	g := New()
	g.KeyDown("A") // pressed, but the KeyUp is lost (focus stolen)
	g.Reset()
	// The stuck press is gone: an autorepeat-style typed event finds no
	// credit and is refused.
	if g.AllowRune() {
		t.Fatal("orphaned press survived Reset")
	}
	// And the key is no longer "held", so a fresh genuine press registers.
	g.KeyDown("A")
	if !g.AllowRune() {
		t.Fatal("a real press after Reset was wrongly refused")
	}
}
