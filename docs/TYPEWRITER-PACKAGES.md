# Typewriter packages

Status: architecture and roadmap. The initial schema-1 implementation is in
`internal/typewriter`; the exact implemented wire contract is documented in
`TYPEWRITER-PACKAGE-FORMAT.md`. Later sections in this document intentionally
describe follow-on capabilities such as system-font materialization and
publisher signatures that are not part of the first safe implementation.

## The short version

A typewriter is an immutable, declarative package with the extension
`.aytw`. It contains a manifest, one exact typeface, calibrated glyph data,
sound samples, licences, provenance, and a preview. It contains no Go code,
scripts, native libraries, or network hooks.

A document records the exact package identity, version, content digest, and
rendering-engine version in its immutable header. It does not record those
values on every strike and it does not silently follow package upgrades.
This preserves ayfor's defining rule: the event stream stores intent, while
appearance is derived deterministically.

The first proof package should be a **1957 Olympia SM3, Pica-inspired
engineering demo**. Olympia sold SM3s with several faces, including Pica and
Elite; a period machine is documented here as a useful reference, but the
demo must not claim specimen-level fidelity until its type slugs and sounds
have actually been sampled from one.

The demo can bundle Cutive Mono as its visibly different, redistributable
face. Google Fonts identifies Cutive Mono as a monospace face under the SIL
OFL. The current Olivetti samples may be reused only while the package is
clearly marked `fidelity: "inspired"` and their real provenance remains in
the package. A release described simply as “Olympia SM3” needs newly recorded
or properly licensed Olympia SM3 samples.

References:

- [1957 Olympia SM3 reference](https://john.wherry.com/newer-models/styled-51/)
- [Olympia SM3 operating instructions](https://www.londontypewriters.co.uk/wp-content/uploads/2022/01/London-Typewriters-Olympia-SM3-instruction-manual-English.pdf)
- [Cutive Mono in Google Fonts](https://github.com/google/fonts/tree/main/ofl/cutivemono)

A third built-in demonstrates that the format is not special-cased to one
proof machine: the **Olympia Splendid 66 (1967), Elite-inspired demo** uses a
different Apache-licensed face, 12 CPI escapement, margins, calibration, and
sound tuning. Its historical target is based on a dated reference specimen;
the geometry and sounds remain engineering interpretations, so the manifest
truthfully declares `fidelity: "inspired"`.

- [1967 Olympia Splendid 66 reference](https://typewriterdatabase.com/1967-olympia-splendid-66.3967.typewriter)
- [Special Elite in Google Fonts](https://github.com/google/fonts/tree/main/apache/specialelite)

## Compatibility strategy

The pre-package behavior is distributed as the built-in **Ayfor Classic
1.0.0** package. Existing documents keep their original STRIKE v1 bytes and
implicitly resolve to that exact package; no migration or rewrite occurs.
Selecting that exact built-in for a new document also keeps writing v1.
Every other package writes v2 and records its full immutable reference.

This makes the old solution the compatibility default without turning it
into a moving fallback. Classic's ID and engine namespace are reserved, and
an unavailable v2 package produces a resolution error instead of silently
rendering with Classic. Logical text and Markdown export remain available
from v2 files even when their appearance package is missing.

Built-ins are embedded as canonical `.aytw` release archives, not rebuilt
from a mutable source tree at startup. Every released archive has a pinned
digest in code and remains embedded after newer releases are added. Tests
also require the checked-in authoring source to match its current pinned
release, preventing accidental same-version republishing.

## The three things that must stay separate

There are three layers, with deliberately different lifetimes:

1. A **typewriter model package** says what an Olympia SM3 Pica is: its face,
   escapement, fixed slug alignment, mechanical ranges, sounds, and
   provenance. Packages are shareable and immutable.
2. A local **typewriter instance** says which virtual SM3 this user owns: its
   stable seed, condition, ribbon wear, and nickname. Instance state is
   mutable and private to the installation.
3. A **document snapshot** copies the package reference and the instance
   state required to replay the manuscript. It never depends on the mutable
   local instance after creation.

This gives the application a natural model. Installing a package adds a
kind of machine; choosing “Add to my typewriters” creates an instance;
feeding paper creates a document tied to the selected instance.

Writer state remains separate. Touch is a remembered property of the human.
Disposition and sobriety remain per-document events. Condition and ribbon
belong to a typewriter instance.

## Package layout

An `.aytw` is a deterministic ZIP containing one root directory:

```text
io.ayfor.demo.olympia-sm3-pica-1957/
  typewriter.json
  TYPEWRITER.LOCK
  preview.png
  calibration/
    glyphs.csv
  fonts/
    CutiveMono-Regular.ttf
  sounds/
    hammer-01.wav
    hammer-02.wav
    hammer-03.wav
    hammer-04.wav
    hammer-05.wav
    bell.wav
    carriage-return.wav
  licenses/
    package.txt
    OFL-Cutive-Mono.txt
    sounds.txt
  provenance/
    README.md
```

`typewriter.json` is the authored definition. `TYPEWRITER.LOCK` is generated
by the packer and contains a SHA-256 for every file plus the canonical
package digest. The digest is calculated over sorted, normalized paths,
file lengths, and file hashes, excluding the lock and signature. It is
therefore independent of ZIP timestamps and compression settings.

An optional `TYPEWRITER.SIG` is an Ed25519 signature over that content
digest. Signing proves publisher identity; it is not required for a local
package. A package is still safe without a signature because it is data,
not executable code.

The installer rejects absolute paths, `..`, symlinks, duplicate normalized
paths, case-colliding paths, oversized files, excessive expansion ratios,
unlisted files, malformed fonts, and audio outside the supported limits.
It installs by digest into a content-addressed directory and atomically
updates the registry only after validation succeeds.

## Manifest format

JSON is used because the Go standard library can decode it strictly into a
versioned struct, reject unknown fields, and emit one canonical shape for
validation and hashing. The builder can generate it from a friendlier tool
later; the distribution format should stay boring.

This is an illustrative proof manifest. Numeric values are deliberately
physical units rather than pixels.

```json
{
  "schema": 1,
  "id": "io.ayfor.demo.olympia-sm3-pica-1957",
  "version": "0.1.0",
  "name": "Olympia SM3 (1957) — Pica demo",
  "publisher": "ayfor",
  "description": "An engineering proof package inspired by a 1957 Olympia SM3.",
  "fidelity": "inspired",
  "engine": {
    "id": "classic-impact",
    "version": 1
  },
  "compatibility": {
    "minimum_ayfor": "0.2.0"
  },
  "specimen": {
    "maker": "Olympia",
    "model": "SM3",
    "year": 1957,
    "variant": "Pica",
    "notes": "Demo calibration; not measured from a single serial-numbered specimen."
  },
  "geometry": {
    "escapement_mm": 2.54,
    "line_advance_mm": 4.2333333333,
    "line_spacing_options": [1.0, 1.5, 2.0],
    "bell_slots_before_margin": 6,
    "default_margins_mm": {
      "left": 25.0,
      "right": 20.0,
      "top": 25.0,
      "bottom": 25.0
    }
  },
  "typeface": {
    "source": {
      "kind": "bundled",
      "path": "fonts/CutiveMono-Regular.ttf"
    },
    "face_index": 0,
    "em_mm": 4.2333333333,
    "scale_x": 1.0,
    "baseline_shift_mm": 0.0,
    "missing_glyph": "U+FFFD",
    "license": {
      "spdx": "OFL-1.1",
      "path": "licenses/OFL-Cutive-Mono.txt"
    }
  },
  "mechanics": {
    "profile": "manual-portable",
    "fixed_alignment_csv": "calibration/glyphs.csv",
    "machine_variation": {
      "x_mm": 0.10,
      "y_mm": 0.13,
      "tilt_deg": 0.65,
      "ink_gain": 0.04,
      "fill": 0.16
    },
    "strike_variation": {
      "xy_mm": 0.04,
      "tilt_deg": 0.20,
      "ink_gain": 0.09
    },
    "basket_shift": {
      "y_mm": 0.15,
      "ink_gain": 0.08
    },
    "ribbon": {
      "floor": 0.72,
      "decay_strikes": 45000,
      "fresh_boost": 0.05,
      "fresh_decay_strikes": 1200
    }
  },
  "sound": {
    "sample_rate_hz": 44100,
    "channels": 1,
    "selection": "position-fnv1a-v1",
    "hammer": {
      "samples": [
        "sounds/hammer-01.wav",
        "sounds/hammer-02.wav",
        "sounds/hammer-03.wav",
        "sounds/hammer-04.wav",
        "sounds/hammer-05.wav"
      ],
      "pitch_spread": 0.045,
      "gain_min": 0.55,
      "gain_max": 1.0
    },
    "events": {
      "bell": "sounds/bell.wav",
      "carriage_return": "sounds/carriage-return.wav"
    },
    "license": {
      "path": "licenses/sounds.txt"
    }
  },
  "preview": "preview.png"
}
```

The parser uses `DisallowUnknownFields`, finite-number checks, sensible
physical bounds, and explicit enum validation. Schema 1 is data for the
host-provided `classic-impact` engine; it is not a bag of arbitrary knobs
interpreted ad hoc by each caller.

### Glyph calibration

Per-slug calibration belongs in CSV because it is easy to measure, diff,
and generate from a specimen sheet:

```csv
codepoint,dx_mm,dy_mm,tilt_deg,ink_gain,fill,audio_group
U+0041,-0.06,0.04,-0.18,1.03,0.02,wide
U+0065,0.03,-0.02,0.10,0.97,0.05,normal
U+003F,0.08,0.06,0.24,1.07,0.01,narrow
```

Code points use `U+XXXX`, not literal characters, so commas, quotes,
control characters, and Unicode normalization cannot make the file
ambiguous. Missing rows use zero offsets and the default audio group.
Duplicate rows are errors.

The engine applies appearance in a fixed, versioned order:

1. package fixed per-slug calibration;
2. seeded per-machine variation within the package's ranges;
3. basket-shift and previous-letter linkage;
4. timing, touch, disposition, sobriety, and condition;
5. per-strike deterministic jitter;
6. ribbon wear and clamping;
7. page re-insertion offset.

Offsets are additive in millimetres/degrees. Ink gains are multiplicative.
Fill is additive and clamped. `audio_group` changes the eligible sample set;
the final sample is still selected deterministically from the strike's page,
row, column, and overstrike number.

A package measured from one exact specimen can set machine-variation ranges
to zero and put all measured character into the CSV. A model-level package
keeps non-zero variation so different instance seeds create different
machines of the same model.

## Font sources

The built package ultimately needs exact font bytes. A family name alone is
not reproducible: operating-system and Google Font releases can change.
There are three authoring sources:

- `bundled`: the manifest points at a TTF/OTF/TTC in the source tree. This is
  the preferred distribution form.
- `google-fonts`: the author gives a repository commit, file path, and
  expected SHA-256. `strike typewriter pack` downloads it at build time,
  verifies it, copies it and its licence into the package, and rewrites the
  built manifest to `bundled`. Opening a document never causes network I/O.
- `system`: for a font whose licence forbids redistribution. The manifest
  lists platform-specific PostScript names and an exact expected SHA-256.
  CoreText on macOS, DirectWrite on Windows, and fontconfig on Linux locate
  candidates; ayfor accepts only the exact bytes. Such a package is marked
  `portable: false`.

If a system-font package permits several different platform font binaries,
the chosen font hash becomes part of a **materialization digest** recorded
in the document. Another machine must find that exact materialization; it
must never silently substitute a metrically similar face. The UI can offer
“locate required font” or install a separate portable package variant.

Fonts are loaded once when the typewriter is resolved. The renderer uses the
package's physical `em_mm`, horizontal scale, and baseline shift. It no
longer assumes Courier Prime's `0.6 em` advance. The carriage grid still
comes from the physical escapement, never from font metrics.

## Sound format

Package authors provide mono PCM WAV files. WAV is pleasant to edit and
audit; the installer decodes and validates them, then the registry caches a
normalized PCM16 bank at ayfor's mixer rate. Initial limits should be:

- 44.1 or 48 kHz input, mono, 16/24-bit PCM;
- no clip longer than two seconds;
- no more than 32 hammer samples and 16 event samples;
- a bounded total decoded size per package.

The existing persistent oto mixer, voice cap, and headroom compensation stay
exactly where they are. `sound.Bank` changes from an embedded singleton to a
bank built from the resolved typewriter. Hammer pitch and gain continue to
follow the same strike force. The bell, carriage return, space bar, platen,
and ribbon controls can be event sounds, but absent entries are simply
silent. Sound must never affect document state or whether a strike lands.

Audio playback is repeatable in event/sample choice; byte-identical acoustic
output is not promised across operating-system audio devices.

## Package and document versioning

Four version numbers exist for four different reasons:

| Version | Example | Changes when |
|---|---:|---|
| STRIKE format | 2 | the document wire format changes |
| package schema | 1 | manifest fields or package rules change |
| package version | 1.2.0 | the publisher releases different assets or calibration |
| engine version | `classic-impact/1` | deterministic mechanics/rendering semantics change |

Package versions follow SemVer for discovery. Replay resolution is stricter:
the document stores `id + version + package digest + materialization digest`.
If the same ID and version arrives with another digest, installation fails as
a supply-chain conflict. Updating a package installs alongside old releases;
it never overwrites them and existing documents never upgrade themselves.

The host keeps old engine implementations. A package may use only an engine
ID/version ayfor implements. A future executable or WASM engine is explicitly
out of scope; declarative engines cover the requested extension without
turning a typeface archive into a code-execution mechanism.

## STRIKE v2 binding

STRIKE v1 has a fixed 40-byte header with a one-byte `FontID` and one global
model version. Its four reserved bytes cannot hold a durable package identity.
Do not squeeze an index or truncated hash into them.

STRIKE v2 should retain the compact v1 event encoding but replace the fixed
header with a length-prefixed JSON header:

```text
0..3   "STRK"
4      format version = 2
5      flags
6..7   header JSON length, little-endian uint16
8..N   UTF-8 header JSON
N..    existing varint-delta + opcode event stream
```

The generated JSON is compact and canonical. A typical header remains under
one kilobyte, paid once per document. The hash chain starts at byte zero and
covers the exact header bytes as it does today.

The relevant header section is:

```json
{
  "created_unix_ms": 1784102400000,
  "machine_seed": "6f1bd8e8246c907a",
  "typewriter": {
    "id": "io.ayfor.demo.olympia-sm3-pica-1957",
    "version": "0.1.0",
    "package_digest": "sha256:...",
    "materialization_digest": "sha256:...",
    "engine": {"id": "classic-impact", "version": 1}
  },
  "paper": "iso-a4",
  "geometry": {
    "escapement_um": 2540,
    "base_line_advance_um": 4233,
    "line_spacing_tenths": 10,
    "margins_tenth_mm": [250, 200, 250, 250]
  }
}
```

The realized starting geometry is copied into the document. Logical folding,
plain-text export, metadata inspection, and hash verification therefore still
work when the typewriter package is missing. Visual rendering, visual PDF/PNG
export, and audio replay require the exact package and fail with a useful
“install this typewriter” message rather than rendering inaccurately.

V1 remains readable forever by resolving its `FontID=1, ModelVersion=1` to a
built-in synthetic package reference for the current Courier Prime machine.
New builds implement both decoders; existing files are not rewritten.

The first package release should keep the current 10/12-cpi event payloads.
If arbitrary physical escapements are later required, add a v2
`SET_ESCAPEMENT_UM` opcode rather than overloading the old one-byte
`SET_PITCH`. The Olympia Pica proof uses 10 cpi and does not need that extra
format change.

## Runtime organization

Proposed package boundaries:

```text
internal/typewriter/
  manifest.go       strict schema types and validation
  package.go        .aytw read, lock verification, safe extraction
  registry.go       built-in/user package index and instances
  resolve.go        exact package + font materialization resolution
  profile.go        immutable runtime Profile consumed by other packages
internal/machine/
  classic_v1.go     current model refactored to accept Profile parameters
internal/render/
  renderer.go       Renderer constructed with a resolved Profile/font
internal/sound/
  bank.go           Bank constructed from Profile WAV assets
assets/typewriters/
  io.ayfor.legacy-courier/...
  io.ayfor.demo.olympia-sm3-pica-1957/...
```

`typewriter.Profile` is the only type the mechanics, renderer, sound system,
and exporters consume. They do not read manifests, inspect ZIPs, search fonts,
or know registry paths. Resolution happens at the application boundary.

The registry search order is:

1. exact content-addressed user installation;
2. exact built-in package;
3. missing-package error with the required identity.

There is no current-directory auto-loading. A development directory can be
enabled explicitly by a CLI flag, but a downloaded document must not be able
to make ayfor execute or load arbitrary nearby content.

## Wiring into ayfor

### Creating a document

1. The selected local instance resolves to an immutable `Profile`.
2. A new STRIKE v2 header captures the exact package/materialization digests,
   engine version, instance seed, and realized default geometry.
3. Initial condition/ribbon state is captured as header state or explicit
   zero-time events. Touch remains an explicit writer event as today.
4. The session opens and appends events exactly as it does now.

Changing the selected typewriter affects the next document, not the sheet
already in the platen. Moving a live sheet between machines can be designed
later as an explicit `CHANGE_TYPEWRITER` event; silently swapping renderers
mid-document is forbidden.

### Opening a document

1. Decode the header and verify the hash chain independently of packages.
2. Resolve the exact typewriter and engine.
3. Construct `machine.Model`, `render.Renderer`, and `sound.Bank` from the
   same immutable profile.
4. Fold events. The page layer passes the profile's fixed and ranged values
   into the engine; it does not use global model constants.
5. Cache page bitmaps exactly as today.

Loading an old document temporarily activates its bound renderer and sounds;
it does not change the user's default typewriter for new documents.

### GUI

`Machine -> Typewriters...` opens a small manager with Installed and My
Typewriters sections, preview, package/version/digest, fidelity, licences,
and Install File. The main Machine menu shows the current instance name and
only the controls the package declares. A fixed-Pica SM3 does not pretend it
can switch to Elite; its pitch menu is absent or disabled. Condition, ribbon,
and sound remain normal machine controls.

When a required package is missing, the document remains inspectable. The UI
shows its ID, version, digest, and an Install Package action. It does not go
to the network merely because a document named a package.

### Rendering and exports

- `render.New(scale)` becomes `render.New(scale, profile)`.
- Glyph cache keys include profile/materialization digest, rune, and physical
  size.
- `machine.New(seed)` becomes the selected engine constructed with the
  profile's mechanical parameters and glyph table.
- comfort chrome uses the active document's renderer, preserving the current
  “machine's own hand” behaviour.
- PNG uses the resolved renderer as today.
- PDF must stop naming core `/Courier`. The safest first exact implementation
  is to place a high-resolution page image produced by the canonical renderer
  into the PDF; a later engine can embed/subset licensed fonts and retain a
  hidden searchable text layer.
- DOCX uses the package's declared family and physical size. Embedding is only
  allowed when the font licence permits it; otherwise DOCX remains a semantic,
  potentially substituted export. PDF/PNG are the appearance-faithful formats.

### Sound

`sound.NewPlayer()` becomes `sound.NewPlayer(bank)` or the player accepts
atomic bank changes when opening another document. Live typing and replay
select hammer samples from the active profile. `CR`, bell, and other event
sounds are queued through the same mixer. Audio-device lifetime remains
process-wide.

## Author workflow

The existing `strike` tool should own the package workflow:

```text
strike typewriter init olympia-sm3-pica-1957
strike typewriter validate ./olympia-sm3-pica-1957
strike typewriter preview ./olympia-sm3-pica-1957 preview.png
strike typewriter pack ./olympia-sm3-pica-1957 olympia-sm3-pica-1957.aytw
strike typewriter inspect olympia-sm3-pica-1957.aytw
strike typewriter install olympia-sm3-pica-1957.aytw
```

`init` creates the tree and schema-valid starter files. `validate` reports
errors with JSON/CSV paths. `preview` types a fixed calibration sheet using a
fixed seed. `pack` resolves Google Fonts sources, verifies licences and hashes,
normalizes WAV files, writes the lock, and creates a deterministic archive.
`inspect` never installs. `install` performs the hostile-archive checks and
atomic registry update.

For authentic calibration:

1. Choose and identify a specimen, including year, keyboard, pitch, and
   preferably serial number (the public package may hash or omit the serial).
2. Fit a fresh ribbon and type a supplied calibration sheet at a controlled
   touch, including repeated glyphs and overstrikes.
3. Scan at a known DPI with no perspective correction hidden from the
   process.
4. Fit per-glyph baseline, x offset, rotation, ink gain, and fill; review the
   generated CSV rather than treating computer vision as truth.
5. Record isolated hammer strikes, carriage return, bell, space bar, and
   platen movement; clean and normalize without erasing the machine's body.
6. Add licences and detailed provenance.
7. Generate the fixed preview and golden render; validate, pack, inspect, and
   install into a clean profile.

## Proof-of-concept definition of done

The Olympia demo proves the architecture when all of these are true:

- installing one `.aytw` makes it appear without recompiling ayfor;
- a new document records its exact package and engine identity;
- the page is visibly rendered with Cutive Mono and package glyph offsets;
- five file-based hammer sounds and event sounds play through the existing
  mixer;
- closing, reopening, replaying, and PNG/PDF export resolve the same profile;
- uninstalling the package leaves `info`, `verify`, and text export working,
  while visual rendering gives an exact missing-package message;
- reinstalling the exact archive restores byte-identical golden rendering;
- installing another digest under the same ID/version is rejected;
- a legacy STRIKE v1 file still renders exactly as it does before the change;
- corrupt and adversarial archives fail without leaving partial installs.

## Implementation sequence

1. Extract the current Courier Prime constants, embedded font, and Olivetti
   bank behind an in-memory `typewriter.Profile`. Change no file format or
   output; golden tests must remain identical.
2. Implement schema, safe package reader, content digest, registry, and the
   `strike typewriter` commands. Build the Olympia demo.
3. Parameterize `machine`, `render`, and `sound`; retain `classic-impact/1`
   as the exact old algorithm plus profile data.
4. Add STRIKE v2 headers and the v1 synthetic-profile adapter. Split logical
   folding from visual appearance enough that text operations tolerate a
   missing package.
5. Add the GUI manager, per-typewriter instances, package selection for new
   documents, and missing-package handling.
6. Make PDF/DOCX typeface-aware, complete hostile-package tests, and publish
   the demo package alongside ayfor.

The first step is intentionally an output-preserving refactor. It creates the
dependency seam before package loading, document migration, and UI are allowed
to add moving parts.
