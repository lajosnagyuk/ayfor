# ayfor

A typewriter emulator, not a text editor. An A4 sheet, one font, no
delete key. Every keystroke is appended to an open, replayable log -- the
`.strike` file -- that records typing cadence and makes casual edits visible,
without claiming cryptographic proof of human authorship. It renders with
the mechanical character of a real machine: every hammer has its own
lean, fast typing strikes light, a pause strikes heavy, the ribbon wears
out (and can be replaced), the paper has grain, the letters press into
it. Hammer sounds are real: an Olivetti Lettera 22, pitch-bent by strike
force. Cmd+R replays the manuscript being typed, at the rhythm it was
typed, with a "- 19 days pass -" title card where you took a weekend off.

- `docs/DESIGN.md` - the idea walked around, the STRIKE file format,
  the personality model, key map.
- `docs/TYPEWRITER-PACKAGE-FORMAT.md` - the exact `.aytw` package contract.
- `docs/TYPEWRITER-PACKAGES.md` - package architecture and compatibility model.

## Layout

```
cmd/ayfor          the typewriter (Fyne GUI)
cmd/strike         CLI: files plus typewriter list/inspect/pack/install/remove
internal/format    STRIKE v1/v2 codec (append-only, hash-chained)
internal/typewriter strict package loader, builder, registry and built-ins
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
assets/typewriters authoring sources for built-in typewriters
assets/typewriter-releases immutable, digest-pinned built-in `.aytw` archives
```

## Platform

The GUI (`cmd/ayfor`) needs cgo
(Fyne/OpenGL, oto for audio), so building it needs a C toolchain: the
Xcode command line tools on macOS, `gcc libgl1-mesa-dev xorg-dev
libasound2-dev` on Linux, MinGW gcc on Windows. The `strike` CLI is
plain Go and runs anywhere. All audio - hammer strikes and the margin
bell - plays through one in-process mixer; nothing shells out and
nothing touches the temp directory.

## Downloads

Each release ships prebuilt artifacts: macOS `.app` bundles (Apple
silicon and Intel; unsigned, so first launch is right-click, Open), a
Linux tarball (amd64), and a Windows zip (amd64). CI runs the
complete suite on each release platform and portable security-sensitive tests
on every change. macOS remains the platform tested hands-on by the maintainer;
reports from Linux and Windows users are especially welcome.

## Quick start

```bash
go build ./cmd/strike
./strike import -seed 1 some.txt some.strike
./strike import -typewriter machine.aytw some.txt olympia.strike
./strike replay some.strike        # watch it type, in the terminal
./strike export some.strike out.pdf
go build ./cmd/ayfor && ./ayfor
```

For a double-clickable app, use the version of the Fyne packager pinned by
the release workflow, then `make app` produces `ayfor.app`.

## Typewriter packages

Ayfor Classic 1.0.0 is the original ayfor implementation packaged and frozen
for compatibility. Existing and new Classic manuscripts remain STRIKE v1 and
render exactly as before. Two explicitly `inspired` proof machines are also
built in: Olympia SM3 (1957) Pica demo 0.1.0 uses Cutive Mono at 10 CPI, while
Olympia Splendid 66 (1967) Elite demo 0.1.0 uses Special Elite and a 12 CPI
escapement. Each has package-bound glyph calibration, geometry, and sound
tuning. Historical Olympia names identify the machines that inspired these
unofficial packages; no affiliation or endorsement is claimed.

Choose a machine under **New Document Typewriter**. Because a manuscript's
machine is immutable, choosing a different one offers **Save and start new**
or **Cancel**; the new package is committed only after saving succeeds and a
fresh document opens. The menu also shows package details and installs or
removes external `.aytw` files. A manuscript records the exact package ID,
version, digest and engine; Ayfor never silently substitutes another release.

To build and install a package from an authoring directory:

```bash
make cli
./strike typewriter pack ./my.machine.package ./my-machine.aytw
./strike typewriter inspect ./my-machine.aytw
./strike typewriter install ./my-machine.aytw
./strike typewriter list
```

The source directory name must equal the reverse-DNS package ID and contain:

```text
my.machine.package/
  typewriter.json
  fonts/face.ttf
  sounds/hammer-01.wav
  calibration/glyphs.csv       # optional
  licenses/package.txt
  provenance/README.md
```

Start by copying one of the complete authoring examples under
`assets/typewriters/`, give it a new ID and SemVer version, then replace its
font, WAV samples, calibration, licence and provenance. `pack` validates every
asset and generates `TYPEWRITER.LOCK`; do not author that lock by hand. Package
versions are immutable: changed content requires a new version. The exact
manifest fields, units, limits and CSV columns are in
[`docs/TYPEWRITER-PACKAGE-FORMAT.md`](docs/TYPEWRITER-PACKAGE-FORMAT.md).

To inspect the shipped proof package as a standalone distributable archive:

```bash
./strike typewriter export-builtin io.ayfor.typewriters.olympia-sm3-pica-1957 olympia-sm3.aytw
./strike typewriter inspect olympia-sm3.aytw
```

Typing: keys strike, Space advances, Backspace moves the carriage left
(nothing is ever deleted), Return releases the margin lock. Cmd+N feeds a
sheet, Cmd+[ / Cmd+] flip the stack, Cmd+Backspace scrunches the page
into the bin (still saved), Cmd+R replays the file (Esc stops). The bell
rings six slots before the margin. The Machine menu sets pitch, spacing,
margins, replaces the ribbon, toggles the hammer sound, and lets you Bash
or Fix the machine (rougher or cleaner letters). The Human menu holds the
writer: your Touch (light/medium/firm), Disposition (composed -> furious:
heavier, angrier ink) and Sobriety (sober -> legless: a wandering
baseline). The machine's wear and your touch are remembered; your mood
and sobriety reset with each new sheet.

The Comfort menu is display-only chrome, typed into the top margin in
the machine's own hand and never written to the file or an export: a
page number, a running page/document word count, and Dank mode - a warm
dark view of the same sheet. (ayfor does not have a dark mode. It has
Dank mode.) All three are off by default; the strike file is
byte-identical whether they are on or off.

Importing: Cmd+O opens a `.strike` file, or takes any plain text file
(`.txt`, `.md`, and friends) and types it in as a machine - a uniform
40 ms cadence, flagged `origin=imported`, so it is visibly not human on
replay and never launders machine text as your own. Markup is typed
verbatim: a Markdown file lands with its `#` and `*` as struck keys,
because a typewriter has no idea what Markdown is. Imported text is limited
to 1 MiB and its expanded event stream is bounded; oversized input is rejected
before a destination is published rather than silently truncated.

Saving: there is no save button and no save anxiety - every keystroke is
appended to the session log before it is applied, and the log is flushed
and fsynced to disk every 5 seconds and at every structural moment (quit, rename,
replay). New documents are drafts in `~/Documents/ayfor/drafts/`; Cmd+S
gives the draft its proper name. Save As, exports, imports and package builds
never replace an existing destination; choose a new name explicitly.

Compatibility is byte-preserving: opening a legacy STRIKE v1 manuscript never
migrates or rewrites it, and Classic continues to create v1. The decoder's
128 MiB / 1,048,576-event safety ceilings apply to hostile or damaged files;
files over a ceiling are refused intact and are not modified. Non-Classic
machines create STRIKE v2, and older Ayfor versions are expected to reject
those files cleanly rather than misrender them.

## Licensing

- Code and original package definitions: BSD-3-Clause - see `LICENSE`.
- Fonts: Courier Prime and Cutive Mono under SIL OFL 1.1; Special Elite under
  Apache-2.0.
- Hammer samples: processed excerpts of `typewriter-olivetti-lettra-22` by
  keithpeter (Freesound), under the Pixabay Content License.

Exact upstream revisions, SHA-256 hashes, attribution, processing details and
licence locations are collected in `THIRD_PARTY_NOTICES.md`. Release archives
include that notice, complete redistributed font licence texts, and stable
links to the binding Pixabay terms for the processed recording excerpts. They
also contain a generated, manifest-indexed bundle of every licence/notice found
in the exact Go modules linked into the shipped commands.
