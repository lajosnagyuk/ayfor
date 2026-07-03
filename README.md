# ayfor

A typewriter emulator, not a text editor. An A4 sheet, one font, no
delete key. Every keystroke is appended to an open, replayable log — the
`.strike` file — that proves a human's rhythm typed it, and renders with
the mechanical character of a real machine: every hammer has its own
lean, fast typing strikes light, a pause strikes heavy, the ribbon wears
out (and can be replaced), the paper has grain, the letters press into
it. Hammer sounds are real: an Olivetti Lettera 22, pitch-bent by strike
force. Cmd+R replays the manuscript being typed, at the rhythm it was
typed, with a "- 19 days pass -" title card where you took a weekend off.

- `docs/DESIGN.md` — the idea walked around, the STRIKE v1 file format,
  the personality model, key map.

## Layout

```
cmd/ayfor          the typewriter (Fyne GUI, macOS)
cmd/strike         CLI: info, verify, import, export (txt/md/docx/pdf/png), replay, text
internal/format    STRIKE v1 codec (append-only, hash-chained)
internal/machine   deterministic personality model
internal/page      carriage/platen/paper state machine
internal/session   live session: buffered appends, crash repair
internal/importer  text (.txt/.md/...) -> events (robotic cadence, honest provenance)
internal/keygate   held keys strike once (autorepeat suppression)
internal/render    glyph stamping, paper grain, impression texture
internal/export    markdown / docx / pdf
internal/atomicfile  the one place whole files land on disk (write, fsync, rename)
internal/sound     hammer sample bank and mixer (the bell plays through it too)
internal/bell      margin bell synthesis
assets/fonts       Courier Prime (SIL OFL)
```

## Platform

The GUI (`cmd/ayfor`) is built and verified on macOS. It needs cgo
(Fyne/OpenGL, oto for audio), so building it needs a C toolchain: the
Xcode command line tools on macOS, `gcc libgl1-mesa-dev xorg-dev
libasound2-dev` on Linux, MinGW gcc on Windows. The `strike` CLI is
plain Go and runs anywhere. All audio — hammer strikes and the margin
bell — plays through one in-process mixer; nothing shells out and
nothing touches the temp directory.

## Downloads

Each release ships prebuilt artifacts: macOS `.app` bundles (Apple
silicon and Intel — unsigned, so first launch is right-click → Open), a
Linux AppImage and tarball (amd64), and a Windows zip (amd64). macOS is
the platform the author verifies by hand; the Linux and Windows builds
compile and are shipped in good faith — reports welcome.

## Quick start

```bash
go build ./cmd/strike
./strike import -seed 1 some.txt some.strike
./strike replay some.strike        # watch it type, in the terminal
./strike export some.strike out.pdf
go build ./cmd/ayfor && ./ayfor
```

For a double-clickable app: `go install fyne.io/tools/cmd/fyne@latest`
once, then `make app` produces `ayfor.app`.

Typing: keys strike, Space advances, Backspace moves the carriage left
(nothing is ever deleted), Return releases the margin lock. Cmd+N feeds a
sheet, Cmd+[ / Cmd+] flip the stack, Cmd+Backspace scrunches the page
into the bin (still saved), Cmd+R replays the file (Esc stops). The bell
rings six slots before the margin. The Machine menu sets pitch, spacing,
margins, replaces the ribbon, toggles the hammer sound, and lets you Bash
or Fix the machine (rougher or cleaner letters). The Human menu holds the
writer: your Touch (light/medium/firm), Disposition (composed → furious:
heavier, angrier ink) and Sobriety (sober → legless: a wandering
baseline). The machine's wear and your touch are remembered; your mood
and sobriety reset with each new sheet.

The Comfort menu is display-only chrome, typed into the top margin in
the machine's own hand and never written to the file or an export: a
page number, a running page/document word count, and Dank mode — a warm
dark view of the same sheet. (ayfor does not have a dark mode. It has
Dank mode.) All three are off by default; the strike file is
byte-identical whether they are on or off.

Importing: Cmd+O opens a `.strike` file, or takes any plain text file
(`.txt`, `.md`, and friends) and types it in as a machine — a uniform
40 ms cadence, flagged `origin=imported`, so it is visibly not human on
replay and never launders machine text as your own. Markup is typed
verbatim: a Markdown file lands with its `#` and `*` as struck keys,
because a typewriter has no idea what Markdown is.

Saving: there is no save button and no save anxiety — every keystroke is
appended to the session log before it is applied, and the log is flushed
to disk every 5 seconds and at every structural moment (quit, rename,
replay). New documents are drafts in `~/Documents/ayfor/drafts/`; Cmd+S
gives the draft its proper name (it is just a rename).

## Licensing

- Code: Do Whatever The Fuck You Want But Don't Impersonate Me Public
  License — see `LICENSE`. No warranty. Not my house fire.
- Font: Courier Prime, SIL Open Font License 1.1 —
  `assets/fonts/OFL.txt`.
- Hammer samples: processed excerpts of a Pixabay field recording of an
  Olivetti Lettera 22 — `internal/sound/samples/SOURCE.md`.
