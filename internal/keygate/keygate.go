// Package keygate makes held keys strike once, like hammers.
//
// A held key on a manual typewriter keeps its hammer pressed against the
// platen: one impression. The OS disagrees and autorepeats. Fyne's
// desktop driver gives us the seam: genuine presses fire KeyDown and
// releases fire KeyUp, while autorepeat events skip both and arrive only
// as typed events. The gate therefore admits a typed event only when an
// unconsumed physical press backs it.
//
// Fast typing is unaffected: rollover means presses overlap, but every
// keystroke still begins with its own KeyDown, so every strike has a
// press to consume. Chorded presses arrive serialized from the OS -
// first wins by arrival order, and nothing jams.
package keygate

// isModifier reports keys that never produce typed events themselves;
// enqueueing their presses would hand them to some later rune and let one
// autorepeat through. A switch, not a package-level map: there is nothing
// to mutate, so there is no mutable state to protect or misuse.
func isModifier(name string) bool {
	switch name {
	case "LeftShift", "RightShift",
		"LeftControl", "RightControl",
		"LeftAlt", "RightAlt",
		"LeftSuper", "RightSuper",
		"CapsLock":
		return true
	}
	return false
}

// Gate tracks physical key state. Not safe for concurrent use; drive it
// from the single GUI event thread.
type Gate struct {
	held    map[string]bool
	pending []string // unconsumed presses, oldest first
}

func New() *Gate {
	return &Gate{held: make(map[string]bool)}
}

// Reset clears all key state. Call it when the window loses and regains
// focus: a KeyUp dropped while focus was elsewhere (Cmd+Tab away with a key
// held) would otherwise leave a key stuck "held" (its next real KeyDown a
// no-op) and an orphaned pending press a later autorepeat could consume.
func (g *Gate) Reset() {
	clear(g.held)
	g.pending = g.pending[:0]
}

// KeyDown records a genuine physical press (the driver has already
// filtered repeats out of this path).
func (g *Gate) KeyDown(name string) {
	if isModifier(name) || g.held[name] {
		return
	}
	g.held[name] = true
	g.pending = append(g.pending, name)
}

// KeyUp releases a key and discards its press if nothing consumed it
// (shortcut chords, dead keys).
func (g *Gate) KeyUp(name string) {
	delete(g.held, name)
	for i, n := range g.pending {
		if n == name {
			g.pending = append(g.pending[:i], g.pending[i+1:]...)
			return
		}
	}
}

// AllowRune reports whether a typed rune is backed by a physical press,
// consuming the oldest pending one. Autorepeat runes find the queue
// empty and are refused.
func (g *Gate) AllowRune() bool {
	if len(g.pending) == 0 {
		return false
	}
	g.pending = g.pending[1:]
	return true
}

// AllowKey is AllowRune for named keys (Return, Backspace): it consumes
// that key's own pending press, so a repeat of a held Return is refused
// even while other presses are pending.
func (g *Gate) AllowKey(name string) bool {
	for i, n := range g.pending {
		if n == name {
			g.pending = append(g.pending[:i], g.pending[i+1:]...)
			return true
		}
	}
	return false
}
