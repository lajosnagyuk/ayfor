# AYTW package format, schema 1

An `.aytw` file is a data-only ZIP archive containing one root directory. The
root name must equal the package ID. Packages cannot contain executable code,
scripts, symlinks, or references outside that directory.

## Required tree

```text
io.example.typewriters.machine/
  typewriter.json
  TYPEWRITER.LOCK
  fonts/face.ttf
  sounds/hammer-01.wav
  licenses/...
  provenance/README.md
```

`TYPEWRITER.SIG` is reserved. Ayfor ignores its contents until a trust-store
and publisher-identity design exists; its presence never makes a package
trusted.

## Safety limits

- archive and expanded content: at most 32 MiB;
- individual file: at most 8 MiB;
- entries: at most 128;
- compression ratio: at most 200:1;
- paths: relative UTF-8 NFC using `/`, with no `..`, backslash, NUL, absolute
  path, duplicate, or case collision;
- regular files only;
- one mono PCM16 WAV hammer bank at 44,100 Hz, no clip over two seconds;
- 1–32 hammer samples.

Opening a package performs no network access and extracts no untrusted file to
disk. Installation validates completely in memory, writes the canonical
archive atomically, and indexes it by ID, version, and SHA-256 content digest.
The registry atomically pins each ID/version to one digest before publication.
That pin survives uninstall, so an old version can never be rebound to
different bytes. A damaged unrelated archive is ignored while exact
resolution remains fail-closed for the package a document requested.
Built-in releases follow the same rule: every historical archive remains in
the executable and its digest is pinned, so an application upgrade cannot
orphan an older document.

## Manifest

The decoder rejects unknown and duplicate JSON fields. Physical values are
fixed-point integers.

```json
{
  "schema": 1,
  "id": "io.example.typewriters.machine",
  "version": "1.0.0",
  "name": "Example Machine",
  "publisher": "Example Publisher",
  "description": "Optional description.",
  "fidelity": "inspired",
  "engine": {
    "id": "classic-impact",
    "version": 1
  },
  "geometry": {
    "pitch_cpi": 10,
    "line_spacing_tenths": 10,
    "bell_slots_before_margin": 6,
    "default_margins_tenth_mm": [250, 200, 250, 250]
  },
  "typeface": {
    "family": "Example Mono",
    "path": "fonts/face.ttf",
    "em_um": 4233,
    "scale_x_permille": 1000,
    "baseline_shift_um": 0,
    "missing_glyph": "U+FFFD"
  },
  "mechanics": {
    "glyph_calibration": "calibration/glyphs.csv"
  },
  "sound": {
    "sample_rate_hz": 44100,
    "hammer": ["sounds/hammer-01.wav"],
    "pitch_spread_permille": 45,
    "gain_min_permille": 550,
    "gain_max_permille": 1000
  },
  "licenses": ["licenses/package.txt", "licenses/font.txt"],
  "provenance": "provenance/README.md"
}
```

Schema 1 reserves `ayfor-classic/1` and
`io.ayfor.typewriters.classic@1.0.0` for the immutable built-in compatibility
machine; calibrated packages use `classic-impact/1`. It deliberately
supports only 10/12 cpi, 1/1.5/2 line spacing, bundled fonts, and bundled WAV
audio. New capabilities require a schema or engine version rather than
silently changing these semantics.

### Normative manifest fields

Unknown fields and duplicate JSON keys are errors. Unless noted optional,
every field shown above is required.

| Field | Schema-1 contract |
| --- | --- |
| `schema` | Integer `1`. |
| `id` | Portable lowercase reverse-DNS identifier with at least two dot-separated components; components contain ASCII letters/digits with internal hyphens only, at most 160 bytes. Windows device basenames (`con`, `aux`, `com1`…`com9`, `lpt1`…`lpt9`, `nul`, `prn`) are forbidden as components. It must equal the ZIP root/source directory name. |
| `version` | Canonical lowercase SemVer, at most 80 bytes. Numeric prerelease identifiers have no leading zero. Lowercase is required so distinct identities cannot collide on default macOS/Windows filesystems. |
| `name`, `publisher` | Non-empty display-safe UTF-8, at most 120 bytes each. |
| `description` | Display-safe UTF-8, at most 4096 bytes; may be empty. |
| `fidelity` | `original`, `inspired`, or `specimen`. Claims must agree with provenance. |
| `engine.id` / `version` | `classic-impact` / `1`; `ayfor-classic` / `1` is reserved exclusively for the built-in Classic identity/version. |
| `geometry.pitch_cpi` | `10` or `12`. |
| `geometry.line_spacing_tenths` | `10`, `15`, or `20`. |
| `geometry.bell_slots_before_margin` | Integer `0..20`. |
| `geometry.default_margins_tenth_mm` | Four integers (left, right, top, bottom), each `0..1000`. |
| `typeface.family` | Non-empty display-safe UTF-8, at most 120 bytes. |
| `typeface.path` | One `.ttf` file directly below `fonts/`. Collections are not supported. |
| `typeface.face_index` | Optional; when present it must be `0`. |
| `typeface.em_um` | Integer `1000..10000`. |
| `typeface.scale_x_permille` | Integer `500..2000`. |
| `typeface.baseline_shift_um` | Integer `-3000..3000`. |
| `typeface.missing_glyph` | Exactly `U+FFFD`. |
| `mechanics.glyph_calibration` | Optional; one `.csv` file directly below `calibration/`. |
| `sound.sample_rate_hz` | Exactly `44100`. |
| `sound.hammer` | `1..32` unique `.wav` files directly below `sounds/`; mono PCM16, 44.1 kHz, one frame through two seconds. |
| `sound.pitch_spread_permille` | Integer `0..200`. |
| `sound.gain_min_permille` | Integer `0..1000`. |
| `sound.gain_max_permille` | Integer from the minimum through `2000`. |
| `licenses` | `1..16` unique `.txt`/`.md` files directly below `licenses/`; each must be non-empty readable UTF-8 text. Include the package licence itself and every redistributed asset licence. |
| `provenance` | One non-empty readable UTF-8 `.txt`/`.md` file directly below `provenance/`. Record immutable sources/hashes and honest measurement/audio limitations. |
| `preview` | Optional; when present the path is exactly `preview.png`. It is metadata only in engine 1. |

Every referenced role has its own directory, so a font/audio/binary file cannot
masquerade as a licence or provenance record. Every archive content file must
be referenced by the manifest; undeclared payloads are rejected.

## Glyph calibration CSV

```csv
codepoint,dx_um,dy_um,tilt_mdeg,ink_permille,fill_permille,audio_group
U+0041,-60,40,-180,1030,20,wide
```

- code points use uppercase `U+` hexadecimal notation;
- offsets and tilt are additive;
- ink is multiplicative (`1000` is neutral);
- fill is additive and clamped;
- duplicate code points are errors;
- omitted code points receive no fixed adjustment.

`audio_group` is validated and reserved for grouped sample selection; engine
1 currently uses the complete hammer bank.

## Content digest and lock

Every content file except `TYPEWRITER.LOCK` and `TYPEWRITER.SIG` is hashed
with SHA-256. Sorted relative paths are folded into the package digest as:

```text
path UTF-8 || NUL || uint64-big-endian(length) || raw SHA-256(file)
```

`TYPEWRITER.LOCK` contains:

```json
{
  "schema": 1,
  "digest": "sha256:...",
  "files": {
    "typewriter.json": "sha256:..."
  }
}
```

The lock must list every content file exactly. ZIP timestamps and compression
settings do not affect package identity. Ayfor's packer emits sorted entries
with a fixed 1980 timestamp; archives are byte-reproducible with ayfor's pinned
release Go toolchain. The canonical content digest—not compressed ZIP bytes—is
the stable identity across toolchain versions.

An ID and semantic version are immutable. Installing different content under
an already installed ID/version is a conflict, even when both archives are
otherwise valid.

## STRIKE binding

Ayfor Classic continues writing STRIKE v1. The tuple `format=1, model=1,
font=1` permanently means the original built-in machine.

Other packages write STRIKE v2. Its immutable JSON header records package ID,
version, digest, engine ID/version, seed, and realized starting geometry. A
new ayfor resolves that exact reference; it never substitutes a newer package
release. Old ayfor releases are expected to reject v2 cleanly.

## CLI

```text
strike typewriter list
strike typewriter inspect machine.aytw
strike typewriter pack ./io.example.typewriters.machine machine.aytw
strike typewriter install machine.aytw
strike typewriter remove io.example.typewriters.machine 1.0.0 sha256:...
strike typewriter export-builtin io.ayfor.typewriters.classic classic.aytw
```

All output commands refuse to replace an existing destination. Choose a new
name (or explicitly remove the old artifact) so a concurrent file can never be
silently overwritten.
