# Changelog

## v0.1.0 - 2026-07-03

First public release of ayfor: a typewriter emulator, not a text editor.
An A4 sheet, one font, no delete key. Every keystroke lands in an open,
replayable, hash-chained log (the `.strike` format) that proves a
human's rhythm typed it, and renders with the mechanical character of a
real machine - per-hammer bias, ink dynamics, ribbon wear, paper grain,
deboss. Exports to txt/md/docx/pdf/png; ships with the `strike` CLI
(info, verify, import, export, replay, text).

The personality model is v1 and still open: it freezes the day a real
manuscript begins, after which old files render identically forever.

Platform notes, honestly stated:

- **macOS** (Apple silicon and Intel): the platform ayfor is built and
  verified on. The `.app` is unsigned and unnotarized - first launch
  needs right-click -> Open, or
  `xattr -dr com.apple.quarantine ayfor.app`.
- **Linux** (AppImage + tarball, amd64): compiles and is shipped in
  good faith, untested by human hands. AppImages need FUSE2
  (`dnf install fuse fuse-libs` on Fedora), or run with
  `--appimage-extract-and-run`, or use the tarball.
- **Windows** (amd64): a wild but sincere stab. Compiles; never run by
  the author. Reports welcome.

Licensing: code under the Do Whatever The Fuck You Want But Don't
Impersonate Me Public License (see LICENSE, no warranty, not my house
fire); Courier Prime under the SIL OFL 1.1; hammer samples are
processed excerpts of a Pixabay field recording of an Olivetti
Lettera 22 (see `internal/sound/samples/SOURCE.md`).
