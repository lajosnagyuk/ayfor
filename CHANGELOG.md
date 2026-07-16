# Changelog

## v0.2.0 - 2026-07-15

Typewriter packages arrive as an immutable, validated extension system.

- Ayfor Classic 1.0.0 is now the package form of the legacy machine; existing
  STRIKE v1 files remain on the original rendering path.
- Added built-in Olympia SM3 (1957) Pica and Olympia Splendid 66 (1967) Elite
  engineering-demo packages, both honestly marked `fidelity: inspired`.
- Added strict `.aytw` pack, inspect, install, list, remove and built-in export
  commands, content digests, deterministic locks, archive limits, semantic
  versions, package-bound fonts, WAV samples, geometry, calibration and engine
  identity.
- STRIKE v2 documents bind the exact package ID, version, digest and engine.
  Missing or conflicting packages fail closed; no lookalike is substituted.
- Choosing a different machine now requires Save and start new or Cancel, so
  typewriter identity never changes halfway through a manuscript.
- Save As and Export choose paths without opening or truncating destinations;
  every whole-file output uses atomic no-replace publication.
- Hardened crash recovery, partial writes, final-close reporting, atomic
  no-overwrite renames and Windows file-handle replacement behavior.
- Hardened package parsing against traversal, symlink races, undeclared files,
  archive ambiguity, pathological fonts, malformed WAVs and built-in identity
  conflicts; added direct parser fuzz targets.
- Replaced the bespoke project licence with OSI-standard BSD-3-Clause and
  added complete standalone package licences, asset hashes, third-party
  provenance notices and a generated linked-Go-module licence bundle.
- Release automation now pins actions and packaging tools, scopes credentials,
  runs full GUI tests/builds on Linux and Windows, scans the call graph for
  known vulnerabilities, smoke-checks packaged artifacts, verifies release
  notes against the tag, and publishes SHA-256 checksums.

## v0.1.2 - 2026-07-05

- Improved desktop scaling and A4 fullscreen/window behavior, including KDE.

## v0.1.1 - 2026-07-05

- Improved desktop scaling and A4 fullscreen/window behavior, including KDE.

## v0.1.0 - 2026-07-03

First release. ayfor is a typewriter emulator, not a text editor: an A4
sheet, one font, no delete key. Every keystroke lands in an open,
replayable, hash-chained log (the .strike format), and the page renders
with the mechanical character of a real machine: per-hammer bias, ink
dynamics, ribbon wear, paper grain, deboss. Exports to
txt/md/docx/pdf/png. The strike CLI does info, verify, import, export,
replay and text.

The personality model is v1 and still open. It freezes the day a real
manuscript begins; after that, old files render the same forever.

Platforms:

- macOS (Apple silicon and Intel): this is where ayfor is built and
  used. The .app is unsigned, so the first launch is right-click, Open.
  Or run `xattr -dr com.apple.quarantine ayfor.app` once.
- Linux (AppImage and tarball, amd64): builds cleanly, untested on real
  hardware. AppImages need FUSE2 (`dnf install fuse fuse-libs` on
  Fedora), or run with `--appimage-extract-and-run`, or use the
  tarball.
- Windows (amd64): builds cleanly, untested. Reports welcome.

Licensing: the code is under the license in LICENSE (no warranty).
Courier Prime is SIL OFL 1.1. The hammer samples are processed excerpts
of a Pixabay field recording of an Olivetti Lettera 22, see
internal/sound/samples/SOURCE.md.
