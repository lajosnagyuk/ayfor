# Open-sourcing punch list

Goal: publish ayfor under a Do What The Fuck You Want To licence with a
codebase clean enough for the very-akshually crowd. Written 2026-07-03
after an audit of the repo as it stands; each item records WHY so the
list survives being picked up cold. Work top to bottom within a section;
the sections are ordered by how loudly a reviewer would tweet about them.

Items marked DECISION need the owner's call before work starts.

## 1. Licensing and provenance (blocks publishing)

- [x] **LICENSE file at the root.** WTFPL text. DECISION: plain WTFPL vs
  the common "WTFPL with no-warranty clause" variant - WTFPL's missing
  warranty disclaimer is the most repeated akshually about the licence
  itself.
- [x] **Per-asset licensing section in README.** WTFPL covers our code
  only. The repo also ships Courier Prime (SIL OFL - `assets/fonts/
  OFL.txt` already present and must stay, per the OFL) and the five
  Olivetti hammer samples (Pixabay Content License, provenance in
  `internal/sound/samples/SOURCE.md`). State all three plainly: code
  WTFPL, font OFL, samples Pixabay.
- [x] **Pixabay redistribution position in SOURCE.md.** Pixabay's
  licence restricts redistributing content "as standalone files"; our
  clips are heavily processed 140 ms excerpts embedded in a program
  (normal use), but the repo does contain them as `.pcm` files. Write
  one paragraph stating the position so the question is answered before
  it is asked.

## 2. First-hour reviewer finds (real bugs and hygiene)

- [x] **Enforce ModelVersion.** `machine.go` and DESIGN.md both promise
  "renderers refuse mismatches rather than misrender", but nothing
  anywhere compares `Header.ModelVersion` against `machine.ModelVersion`
  (`DecodeHeader` checks FormatVersion only). The docs make a promise
  the code does not keep. Refuse (or at minimum warn) on mismatch in
  `session.Open` and the `strike` CLI, with a pinning test.
- [x] **`go mod tidy`.** `go.mod` lists `github.com/ebitengine/oto/v3`
  as `// indirect` while `internal/sound` imports it directly - tidy was
  never re-run after the sound work landed.
- [x] **`git init` + first commit.** The repo still has no version
  control at all. Before the first commit: add `.claude/` to
  `.gitignore` (contains session-local settings that must not ship);
  binaries (`/ayfor`, `/strike`, `*.app`) are already covered.
- [x] **Delete the empty `assets/sounds/` directory** - leftover from
  the synthesized-sound era.
- [x] **Ignored error returns.** `u.renderer.Stamp(...)` in `after()`
  and `replayApply()` silently drops its error, plus a few similar
  spots. Handle, or make each discard explicit and justified -
  errcheck-style reviewers grep for exactly this.
- [x] **`go test -race ./...` pass.** The replay threading model is
  documented as "all doc access on the Fyne thread via fyne.DoAndWait"
  and should hold; a race-detector run over the testable packages is
  cheap proof.
- [x] **Fuzz `format.Decode`.** It parses untrusted input - people will
  download `.strike` files from strangers. Native Go fuzz test:
  malformed varints, absurd rune values, truncation mid-payload. Exactly
  the credibility signal this audience respects, and genuinely useful.
- [x] **Linter sweep with committed config.** gopls flags a handful of
  modernize hints (`for i := 0; i < n; i++` -> `range n`, manual min/max
  -> builtins). Pick a linter (staticcheck or golangci-lint), fix or
  explicitly ignore each class, commit the config so "runs clean" is
  reproducible.

## 3. Repo identity and docs

- [x] **Module path.** DONE 2026-07-03 evening: the forge repo exists
  (github.com/lajosnagyuk/ayfor), so the one-shot rename happened as
  decided - `go mod edit -module github.com/lajosnagyuk/ayfor` plus the
  mechanical import rewrite, before the first release tag.
- [x] **README refresh.** It predates the 2026-07-02 evening round: no
  mention of replay (Cmd+R), ribbon replacement, the touch dial, real
  hammer samples, or paper grain. Also needs: platform statement (the
  GUI is macOS-only - Fyne builds elsewhere but the bell shells out to
  afplay; the `strike` CLI is cross-platform), build prerequisites (Go
  1.26, Xcode CLT), and the licensing section from section 1.
- [x] **DECISION: HANDOVER.md's fate.** It is an honest engineering log
  - arguably a feature for this audience - but written as private notes:
  home paths, ~/Downloads references, owner quotes. Either sweep and
  publish proudly (recommended - it is the most interesting file in the
  repo) or move it out before the first commit.
- [x] **Regenerate `examples/`.** `page1.png` and `sample.pdf` were
  rendered under the older model; the current renderer produces
  different (textured, lighter) output. Shipping examples that do not
  match `strike export` output on the included `sample.strike` is a
  determinism-promise own-goal.
- [x] **Final personal-data sweep.** Code is clean (verified); do one
  last grep across docs/ and examples/ for personal paths and names
  immediately before the first commit.

## 4. Infrastructure (looks maintained, not just released)

- [x] **CI.** GitHub Actions: gofmt check, `go vet`, `go test ./...` on
  a macOS runner (the full build needs cgo/CoreAudio), plus the linter
  from section 2. Badge in README.
- [x] **Tag v0.1.0 + short CHANGELOG.** DONE 2026-07-03 evening:
  CHANGELOG.md written, tag pushed with the first public (squashed)
  commit; the release workflow builds macOS arm64/Intel apps, a Linux
  AppImage + tarball, and a Windows zip. Also makes the "model v1
  freezes at the first real manuscript" promise auditable in history.
- [x] **.app packaging recipe.** `fyne package -os darwin` documented
  (or a tiny Makefile) so "how do I run this" has a one-line answer for
  non-Go people.
- [x] **CONTRIBUTING.md (optional).** Two paragraphs: the invariants
  (intent-only format, determinism, append-before-apply, nothing is
  ever deleted) and "every bug fix gets a pinning test". Already the
  house style; writing it down deflects drive-by PRs that break the
  philosophy.

## Open decisions, gathered

1. LICENSE variant - DECIDED 2026-07-03: WTFPL derivative with a
   no-warranty clause and a no-impersonation clause ("Do Whatever The
   Fuck You Want But Don't Impersonate Me"), renamed as WTFPL's own
   terms require. Written to LICENSE.
2. Module path - DECIDED 2026-07-03: keep `module ayfor` until the
   forge repo exists, then one-shot rename (see the item above).
3. HANDOVER.md - DECIDED 2026-07-03: kept private. Purged from git
   history (filter-branch before any push, refs/reflog/gc cleaned), the
   file stays on disk gitignored, and README/CONTRIBUTING no longer
   reference it. It remains the working manual for anyone (or anything)
   maintaining this repo from this machine.
