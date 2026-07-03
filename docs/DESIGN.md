# ayfor — a typewriter, not a text editor

A4 sheet on screen. One font. No delete. Every keystroke is a timestamped
strike in an append-only log, replayable to show a human's hands did it.

This document is (1) a walk around the idea — what the brief missed and the
decisions taken, (2) the file format specification, (3) the machine
personality model, (4) architecture and key map.

---

## 1. Walking around the idea

Things the brief implies but does not decide. Each has a decision here; all
are cheap to change before v1 ships.

### 1.1 The file stores intent, not appearance

The single most important design decision. The file records *keystrokes and
timing only*. Where a glyph lands, how heavy it strikes, how it tilts — all
of that is **derived deterministically** from a per-machine seed plus the
event history. Consequences:

- Files are tiny (~4–6 bytes per keystroke; a 300-page novel ≈ 3 MB).
- The same file renders pixel-identically on every machine, forever — the
  model algorithm is versioned in the header, and renderers must implement
  the version they read.
- Export and screen can never disagree, because both re-run the same model.
- Replay is free: the file *is* the replay.

If we stored appearance (x, y, tilt, ink per glyph) the file would be 10×
larger and "replay" would just be an animation of stored numbers — much
weaker evidence of a human.

### 1.2 "Proof" a human typed it — honest framing

Timestamps are evidence, not proof; anyone can synthesize a plausible
rhythm. Two things make forgery annoying and editing detectable:

- **Hash chain**: periodic `CHECK` events carry a rolling hash of every
  byte so far. You cannot edit strike 500 without recomputing every
  checkpoint after it — trivially possible offline, but it proves the file
  wasn't *casually* tampered with and that events are strictly ordered.
- **Honest provenance**: imported text files are typed in by the machine at
  a deliberately robotic, uniform cadence, and the session header is
  flagged `origin=imported`. We never launder machine text as human.

### 1.3 Backspace already exists on a typewriter

The brief says "no backspace" — but real typewriters *have* a backspace
key: it moves the carriage left one slot without erasing. That is exactly
the overstrike mechanism. So: **Backspace moves the carriage left. Nothing
is ever deleted.** This is on-brief and period-accurate simultaneously.

### 1.4 The bell rings *before* the margin

On a real machine the bell warns you ~6 characters before the right margin
locks, so you can finish the word. Decision: **bell at margin −6, hard lock
at the margin, Return releases.** (Brief said bell at the end; the warning
bell is the behaviour typists' ears expect. Margin release key deferred to
v2.)

### 1.5 Overstrike semantics on export

MD and DOCX have no concept of two glyphs in one cell. Lossiness ladder,
worst to best:

- **MD**: plain text. Same glyph struck 2+ times in a cell → `**bold**`
  (double-striking *was* the typewriter's bold). A letter overstruck with
  `x`/`X`/`-` → struck-through (`~~word~~`, extended to the whole
  contiguous x-ed run). Otherwise: last glyph wins. Documented, lossy.
- **DOCX**: same policy, rendered as monospace runs with real bold /
  strikethrough, honouring page breaks.
- **PDF**: exact. Every strike placed with its own transform (offset +
  rotation) and its own ink density. Overstrikes physically overlap, just
  like ink.

### 1.6 Page re-insertion misalignment

When you flip back to an earlier page in the stack, a real typist pulled
the sheet out and rolled it back in — never perfectly aligned. Decision:
every `PAGE_PREV`/`PAGE_NEXT` that lands on a page adds a small
deterministic re-insertion offset (fractions of a mm, seeded) to all
subsequent strikes on that page. Typing over old text will be *almost*
aligned. This is the kind of character the brief asks for.

### 1.7 End of paper

The brief covers end of *line* but not end of *page*. Decision: last
writable line rings the bell twice on Return; a further Return feeds a new
sheet automatically (real machines just let you type off the paper; we are
allowed one mercy).

### 1.8 The platen is an axis too

Superscripts, signatures, and fixing a line's height were done by rolling
the platen. Key combos roll paper up/down by full and half line steps. This
also gives you vertical carriage freedom the brief asked for ("carriage
moved... type over previous letters").

### 1.9 Shift is mechanical

On a basket-shift machine, capitals strike with the type basket lifted —
capitals land with their own bias (slightly high or low, machine-specific)
and often a touch heavier. Modelled: uppercase glyphs get an extra seeded
vertical bias + weight factor.

### 1.10 Ribbon wear and replacement

Ink density decays slowly with strike count toward a floor (fresh ribbon ≈
1.0, tired ribbon ≈ 0.72), plus per-strike dynamics. The curve is tuned to
a real ribbon's 50–100k character rating: at ~50k strikes the ribbon is
noticeably softer, at ~100k it is close to the floor (still perfectly
readable — the floor is the point).

**Machine → Replace ribbon** (a `NEW_RIBBON` event, opcode 0x23) installs
a fresh spool: the wear counter resets and typing carries the heavier
baseline inking again. A fresh spool prints slightly *wet* for its first
couple of thousand strikes, and every spool has a small inking bias of its
own — replacing the ribbon is an event you can see on the page, not just a
counter reset.

### 1.11 What is deliberately NOT modelled (v1)

Tab stops, margin release, red/black ribbon switch, dead keys/diacritics
overstrike composition (Mac composed input is recorded as the composed
rune), escapement skips. Listed as v2 candidates so the format leaves
room (opcodes reserved).

**Jams** (two hammers tangling) are excluded *permanently*, by owner
decision. Ribbon replacement was originally excluded under the same "no
maintenance chores" ruling; the owner reversed that on 2026-07-02 — people
*want* to faff with the tape — with the important distinction that
replacement is entirely voluntary: a worn ribbon only ever fades to a
readable floor and never demands anything from the writer. Character,
never obligation.

### 1.12 Key mechanics: the press is the strike

- **Ink lands on key-down.** That is the moment the hammer hits; the
  timestamp and the ink belong to the press. On a real machine the type
  guide hides the fresh letter for a few tens of milliseconds, but
  emulating that delay reads as input lag, and a typist's eyes run ahead
  of the carriage anyway. Key-up is a non-event — the hammer falling
  back.
- **A held key strikes once.** Holding a key keeps the hammer pressed
  against the platen: one impression. OS autorepeat is suppressed for
  strikes, Space, Backspace and Return alike (`internal/keygate`: every
  typed event must be backed by an unconsumed physical press; repeats
  arrive without one and are refused; fast-typing rollover is unaffected
  because every real keystroke brings its own key-down).
- **Chords don't jam.** Simultaneous presses arrive serialized from the
  OS; first wins by arrival order, the rest follow normally. No jam
  simulation, ever.

### 1.13 Hammer sound (optional, default off)

Five real single-strike samples, extracted and cleaned from a Pixabay
field recording of an Olivetti Lettera 22 (see
`internal/sound/samples/SOURCE.md`). Each strike hashes its position to
pick a base sample (deterministic, like everything else) and resamples it
a few percent from the strike's ink weight — a heavier, more deliberate
strike shifts down (longer, deeper), a light one up (shorter, brighter) —
and its loudness follows that same ink weight, so pitch and gain move
together instead of as two independent random dimensions. Overlapping
strikes are mixed through a single persistent player with a cap on
concurrent voices and power-preserving headroom, so fast rollover typing
does not clip. Playback is via a low-latency audio context (oto/CoreAudio,
~15 ms buffer); the audio device is not touched until the first enable.
Toggle: Machine → Hammer sound, persisted as an app preference.

### 1.14 Font

**Courier Prime** (SIL OFL, embedded in the binary). It is a clean, warm
typewriter face — deliberately *not* a pre-distressed font like Special
Elite, because the distress is our job; a pre-grunged font would
double-distress and, worse, repeat identical grunge per glyph. Metrics
note: layout never uses font metrics — the *pitch* (10 or 12 cpi) dictates
the grid, as on the machine. The font is just the die face.

### 1.15 Comfort menu — display-only chrome (all default off)

Creature comforts drawn on the glass, never in the file and never in an
export. Each is a preference-backed toggle; the strike file is byte-identical
whether they are on or off.

- **Page number** — `- N -` centred in the top margin.
- **Word count** — a running page/document count in the top-right corner
  (the eye drifts from the corner in flow), recomputed every couple of
  seconds. The document total counts live sheets only; x-ed-out words still
  count, since nothing is ever deleted.
- **Dank mode** — a dark view. The canonical warm-paper-and-dark-ink bitmaps
  are luminance-remapped on the glass to a solarized-dark ground with
  near-white ink, preserving grain, deboss and ink-weight as detail. The
  renderer, the file and every export are untouched; only what reaches the
  screen is remapped.

Page number and word count are typed via `page.Doc.ChromeStrike` in a fixed
light, steady, sober hand on a factory-clean machine - chrome is meta-text, so
it does NOT inherit the document's touch, mood, sobriety or wear, and stays
light and legible even when the writing is furious or drunk. None of this is
ever written to the format — appearance stays derived (invariant 1).

---

## 2. File format: STRIKE v1 (`.strike`)

Binary, little-endian, append-only after the header. Open, documented,
no compression. Measured (2026-07-03, on a synthesized 60-page manuscript
with human-like timing jitter): ~3.6 bytes per event raw; gzip -9 reaches
60% of that, zstd -19 56%. So the stream is compressible - the redundancy
is in delta-time and letter distributions - but the absolute numbers make
it pointless: a 300-page novel is ~4 MB raw, ~2.4 MB compressed, and
buying that ~40% would cost the properties the format exists for - cheap
appends, crash-truncation tolerance at any byte, and a hash chain over
raw bytes. Gzip the file for archival if you like; it is not part of the
format.

### 2.1 Header (fixed 40 bytes)

| offset | size | field |
|---|---|---|
| 0  | 4 | magic `"STRK"` |
| 4  | 1 | format version = 1 |
| 5  | 1 | model version = 1 (personality algorithm) |
| 6  | 2 | flags (reserved, 0) |
| 8  | 8 | machine seed (u64) — the machine's soul |
| 16 | 8 | created, unix ms (i64) |
| 24 | 1 | paper = 1 (A4 210×297 mm) |
| 25 | 1 | pitch, chars per inch (10 = Pica, 12 = Elite) |
| 26 | 1 | line spacing ×10 (10, 15, 20) |
| 27 | 1 | font id (1 = Courier Prime) — advisory; grid comes from pitch |
| 28 | 2 | margin left, 0.1 mm units |
| 30 | 2 | margin right, 0.1 mm |
| 32 | 2 | margin top, 0.1 mm |
| 34 | 2 | margin bottom, 0.1 mm |
| 36 | 4 | reserved (0) |

### 2.2 Events

Every event: `varint delta-ms` (LEB128, time since previous event) followed
by `opcode u8` and an opcode-specific payload. Carriage/page position is
never stored — it is state, folded from the event stream.

| op | name | payload | meaning |
|---|---|---|---|
| 0x01 | STRIKE | rune (varint) | hammer strikes at carriage, carriage advances |
| 0x02 | SPACE | — | advance, no ink |
| 0x03 | BACK | — | carriage left one slot (never deletes) |
| 0x04 | CR | — | carriage return + line feed at current spacing |
| 0x05 | LF | — | platen up one line, carriage stays |
| 0x06 | HALF_UP | — | platen half-line up |
| 0x07 | HALF_DOWN | — | platen half-line down |
| 0x08 | *reserved* (TAB) | | |
| 0x10 | NEW_SHEET | — | feed a fresh page, becomes current |
| 0x11 | PAGE_PREV | — | flip to previous sheet (re-insertion offset applies) |
| 0x12 | PAGE_NEXT | — | flip to next sheet (re-insertion offset applies) |
| 0x13 | TOSS | — | scrunch current sheet into the bin (kept, flagged), feed new sheet |
| 0x20 | SET_PITCH | u8 | change pitch mid-document |
| 0x21 | SET_LINESPACE | u8 ×10 | change line spacing |
| 0x22 | SET_MARGINS | 4×u16, 0.1 mm | change margins |
| 0x23 | NEW_RIBBON | — | install a fresh ribbon spool (wear counter resets) |
| 0x24 | SET_TOUCH | u8 ×100 | the writer's touch: 85 light, 100 medium, 112 firm |
| 0x25 | SET_DISPOSITION | u8 ×100 | the writer's mood: 100 composed, 140 terse, 180 furious |
| 0x26 | SET_SOBRIETY | u8 ×100 | the writer's state: 100 sober, 140 merry, 185 legless |
| 0x27 | SET_CONDITION | u8 ×100 | the machine's wear: <100 serviced, 100 factory, >100 banged up |
| 0x30 | SESSION | i64 unix-ms, u8 origin (0 human, 1 imported) | session boundary; wall clock anchor; delta clock restarts |
| 0x3F | CHECK | u64 | FNV-1a 64 of all file bytes from offset 0 up to (not including) this event's delta-time varint |

A well-formed file begins its event stream with a `SESSION` and a
`NEW_SHEET`. Writers emit `CHECK` every 512 events and on close. Readers
must tolerate a truncated final event (a crash mid-write leaves at most
one partial event at the tail — the append-only log is also the
autosave). Since 2026-07-03 the live session buffers appends in memory
and flushes every 5 seconds and at structural moments (close, rename,
replay), so a hard kill loses at most the last 5 seconds of typing; the
header and opening events are flushed immediately so a fresh file is
never left unreadable.

### 2.3 Sizing

STRIKE of an ASCII rune with a sub-127ms gap = 3 bytes (1 delta + 1 op +
1 rune). Typical prose averages ~4 bytes/keystroke including returns and
checkpoints.

---

## 3. The machine personality model (model version 1)

Everything below is a pure function of `(seed, event history)` — no RNG at
render time. Hashes are FNV-1a 64 over labelled tuples; each hash maps to a
uniform float in a stated range. Units: mm and degrees.

Per strike, compose:

1. **Hammer bias** (the soul of each key): `H(seed,"bias",glyph)` →
   constant `dx ∈ ±0.15`, `dy ∈ ±0.18`, `tilt ∈ ±2.2°`, ink-gradient axis
   `θ ∈ [0,2π)`, gradient strength `g ∈ [0.05,0.35]`. Every `e` on this
   machine leans the same way, forever.
2. **Basket shift**: uppercase adds `H(seed,"shift")` → machine-wide
   `dy ∈ ±0.22` and weight ×`[1.0,1.15]`.
3. **Pair slack** (what the linkage does after the previous letter):
   `H(seed,"pair",prev,cur)` → `dx ∈ ±0.08`. This is "which side it
   prefers after certain letters".
4. **Rhythm dynamics** from the *recorded* delta-time: `dt < 90 ms` →
   ink ×0.78..0.95 and pulled up to 0.10 mm toward the previous strike
   (flying start); `dt > 1400 ms` → ink ×1.05..1.15 (deliberate,
   heavy first strike after a pause).
5. **Per-strike jitter**: `H(seed,"jit",page,row,col,strikeIndexOnCell)` →
   `dx,dy ∈ ±0.05`, `tilt ∈ ±0.6°`. Ensures overstrikes never align
   perfectly. The same hash also yields the strike's **texture seed**
   (`Tex`), which drives its ink speckle at render time.
6. **Die fouling**: `H(seed,"fill",glyph)` → `Fill ∈ [0, 0.30]`, squared
   toward zero so most hammers are near clean. A fouled die prints a few
   percent fatter and its counters (the bowls of e, o, a) close up.
7. **Ribbon wear**: with `n` = strikes since the ribbon was installed
   (`NEW_RIBBON` resets it), `t` = touch, and `r` = spool index — a firm
   touch transfers more ink per strike, so the effective count is `n·t²`:
   ink ×`(0.72 + 0.28·exp(−n·t²/45000))` ×`(1 + 0.05·exp(−n·t²/1200))`
   ×`(1 ± 0.03 from H(seed,"ribbon",r))` — wear toward the floor, a
   wet-fresh boost, and per-spool character.
8. **Touch** (`SET_TOUCH`, default 1.0): ink ×`t`. A lighter touch also
   widens the per-strike force jitter (step 5 span ×`(2−t)`): a relaxed
   hand is a less consistent one, which is why a light typist's page
   looks *less* uniform, not just lighter.
9. **Disposition** (`SET_DISPOSITION`, default 1.0 composed): the writer's
   mood. ink ×`(1 + 0.34·(d−1))`, force scatter ×`(1 + 1.05·(d−1))`, tilt
   jitter ×`(1 + 0.55·(d−1))`. A furious hand hammers the keys harder and
   less evenly; because sound loudness rides ink, a furious page also
   *sounds* harder for free.
10. **Sobriety** (`SET_SOBRIETY`, default 1.0 sober): the writer's state.
    Adds a low-frequency baseline wander (amplitude `0.55·(s−1)` mm,
    smoothstep-interpolated every 7 columns so the line undulates rather
    than shakes), and loosens placement (jitter ×`(1 + 1.3·(s−1))`) and
    rotation. Distinct fingerprint from fury: loose and wandering, not
    heavy and violent.
11. **Machine condition** (`SET_CONDITION`, default 1.0 factory, clamped
    [0.3, 2.2]): the typewriter's own wear, kept separate from the writer.
    Scales the machine's inconsistency family — hammer position bias,
    tilt bias, per-strike jitter, per-die ink flatness, die fouling,
    ink-gradient unevenness. Below 1.0 a serviced machine prints uniform
    letters; above 1.0 a barn find prints a wreck. The GUI's "Bash / Fix
    the machine" steps this ±0.2 at a time.

Composition note: touch/disposition/sobriety are the *writer* (hand, mood,
state); condition is the *machine*. They multiply independently, and every
dial defaults to identity (1.0), so a file carrying none of these events
renders exactly as it did before they existed. All four are per-strike
inputs derived from `SET_` events in the stream, so — like everything else
— they are intent, deterministic, and honest on replay.
9. **Re-insertion offset** per page per re-insertion count:
   `H(seed,"insert",page,n)` → `dx ∈ ±0.4`, `dy ∈ ±0.5`, applied to all
   strikes made on that page after its n-th re-insertion.

Rendering a strike: rasterize the glyph at the pitch's nominal size
(enlarged by the fouling factor), apply tilt rotation about the glyph
centre, multiply alpha by ink, then by a linear gradient along axis `θ`
with strength `g` scaled by dynamics (the hammer face not landing flat),
then by the **ink transfer factor** (0.85): cloth ribbon never transfers
at full saturation, and without this headroom every strike with ink ≥ 1.0
would clamp to identical maximum black — measured on a real session, that
was ~60% of all strikes, and the page read as uniform because the model's
force variation was being crushed against the alpha ceiling. Overstrikes
still compound toward true black via source-over. Then by the
**impression texture**: per-strike speckle hashed from (`Tex`, pixel) —
patchier the lighter the strike, since a light strike means partial face
contact — and the paper's fibre tooth sampled from the grain field, both
mean-compensated so texture redistributes ink without fading the page. Finally the **deboss relief**: the impression is pressed
INTO the paper, so under the page's fixed light (upper left) stroke flanks
facing the light get a whisper of shadow and the far flanks a whisper of
lifted-paper highlight — computed as a directional derivative of the glyph
mask, scaled by ink weight. Stamp onto the page bitmap with the composed
offset. The page is a bitmap because paper is a bitmap — nothing reflows,
ever.

**Paper**: each sheet is grained at feed time — a tileable multi-octave
noise field (fine fibre tooth, slightly stretched along the paper's
machine direction, plus a soft pulp mottle), sampled at a per-sheet offset
from `H(seed,"paper",pageIndex)`, amplitude ±3 RGB levels: 50 gsm
typewriter stock, flat with a gentle tooth, never parchment. Same seed,
same sheet, forever; no two sheets alike.

**Export note**: PNG (and the GUI, which shares the raster path) carry
the full impression texture. PDF remains exact-but-vector: per-strike
position, rotation and ink density, no grain/relief/speckle. MD and DOCX
were always lossy-by-policy (§1.5).

---

## 4. Geometry

- A4: 210 × 297 mm. Model space is mm (float64); raster scale is a render
  parameter (GUI ~5.67 px/mm ≈ 144 dpi backing, retina ×2; PDF is vector).
- Pitch: Pica 10 cpi → 2.54 mm/slot; Elite 12 cpi → 2.1167 mm/slot.
- Line: 6 lines per inch base → 4.2333 mm; ×1 / ×1.5 / ×2 spacing.
- Default margins: L 25 mm, R 20 mm, T 25 mm, B 25 mm (menu-adjustable).
- Bell at (right margin − 6 slots); carriage locks at margin.

## 5. Architecture

```
internal/units     mm, pitch, paper constants
internal/format    STRIKE codec: header, events, varints, hash chain
internal/machine   personality model: strike -> {dx, dy, tilt, ink, grad}
internal/page      state machine: events -> pages of cells (strikes per cell)
internal/render    glyph rasterizer + page bitmap stamping (x/image)
internal/importer  text (.txt/.md/...) -> event stream (robotic cadence, origin=imported)
internal/export    txt / md / docx (hand-rolled zip+xml) / pdf (hand-rolled, vector)
cmd/strike         CLI: info, verify, import, export, replay (terminal), text
cmd/ayfor          Fyne GUI: canvas, native Mac menu bar, bell via afplay
```

GUI is Fyne (native Mac menu bar, single window, `canvas.Image` backed by
the page bitmap). Bell is a synthesized WAV played with `afplay` on macOS —
zero audio dependencies.

## 6. Key map (Mac)

| keys | action |
|---|---|
| printable keys | strike |
| Space | advance |
| Backspace | carriage left (no delete — see §1.3) |
| Return | carriage return + line feed (releases margin lock) |
| Cmd+← / Cmd+→ | carriage left / right one slot (same as Backspace/Space but silent, no time pressure) |
| Cmd+↑ / Cmd+↓ | platen: paper up / down one line |
| Shift+Cmd+↑ / ↓ | platen half-line |
| Cmd+N | feed new sheet |
| Cmd+[ / Cmd+] | previous / next sheet in the stack |
| Cmd+Delete | scrunch & toss current sheet (kept in the bin) |
| Cmd+O | load a .strike, or import any text file (.txt/.md/…) as machine typing |
| Cmd+S | save as — names the always-saved draft (a rename, nothing more) |
| Cmd+E | export (txt / md / docx / pdf) |
| Cmd+R | replay the open file in real time (Esc stops) |
| Cmd+Q | quit (log is already on disk; quit is always safe) |

Menus: **File** (Load, Save As, Export, Replay, Quit) · **Paper**
(New/Prev/Next sheet, Scrunch and toss) · **Carriage** (left/right, paper
down a line, half-line up/down) · **Machine** (Pitch 10/12, Line spacing
1/1.5/2, Margins…, Bash/Fix the machine, Replace ribbon, Hammer sound) ·
**Human** (Touch, Disposition, Sobriety). The machine holds the
typewriter's settings and wear; the Human menu holds the writer's hand,
mood and state. Touch and machine condition are remembered as app
preferences and applied to new documents (as SET_ events — the file stays
self-contained); disposition and sobriety are transient and reset per
document, because you do not sit down furious every morning. Every action
shows its shortcut in the menu; on macOS these are native key equivalents.

## 8. Replay

The file *is* the replay (§1.1): Cmd+R re-performs the open document from
its first event on a fresh in-memory doc — every strike lands with the
same position, tilt and ink it had originally, at the rhythm it was typed,
with the hammer sound and margin bell if enabled. The keyboard is the
machine's while it performs; Esc hands it back (the live document is
untouched either way — replay is a performance, not a state change).

Pauses up to 8 seconds play out in full: hesitation is part of the
watching. Anything longer — a coffee, a night, a weekend off — is
compressed into a fade-in/fade-out interstitial over the dimmed sheet
("- 19 days pass -", worded by `humanGap`), because the point is watching
the hands work, not waiting out real time. Session boundaries carry wall
clocks, so the gap between two sittings shows its true length even though
the delta clock restarts.

The bin is part of the stack: tossed sheets stay in place and Cmd+[ /
Cmd+] flip through them like any other page — the title says
"in the bin" while you are looking at one. Ink on a binned page is kept
forever; it just does not export.

Saving model: there is no save button and no save anxiety — every
keystroke is appended to the session log before it is applied, and the
log is flushed to disk every 5 seconds and on every structural moment
(quit, rename, replay). The worst a crash can cost is the last few
seconds of typing. New documents start as drafts in
`~/Documents/ayfor/drafts/<timestamp>.strike`; Cmd+S renames the file to
its proper name (same-volume rename keeps the open handle; cross-volume
falls back to copy-and-reopen). Save As never overwrites an existing
file.

## 7. The strike guide

A faint horizontal line at the current baseline and a small translucent
notch at the next slot centre — the acetate card-holder guide on a real
machine. Drawn on the glass (overlay), never on the paper.
