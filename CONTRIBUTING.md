# Contributing

Sure, if you like. Before you write anything, read `docs/DESIGN.md`.
The project has a small number of load-bearing convictions, and PRs
that fight them will be declined no matter how good the code is:

1. **The file stores intent, never appearance.** Where a glyph lands,
   how heavy it strikes - all derived deterministically from the seed
   and event history. If your feature needs to store appearance, it
   needs a different design.
2. **Determinism is absolute.** No RNG, no wall-clock reads at render
   time. Same file, same pixels, forever. Changing any model constant
   is a personality-model version bump with the old code path kept.
3. **Append before apply.** Events reach the session buffer before they
   reach the document. Nothing is ever deleted: backspace moves, the
   bin keeps, toss refuses new ink.
4. **Honest provenance.** Imported text is machine-flagged at a robotic
   cadence. Nothing ever launders machine text as human typing.
5. **Character, not chores.** The machine has quirks (wonky hammers,
   ribbon wear, bell); it never has obligations (no jams, and a worn
   ribbon always stays readable whether or not you replace it).

House rules: stdlib-first (the DOCX and PDF writers are hand-rolled on
purpose), zero emojis in code, comments state constraints rather than
narrate, and every bug fix lands with a test that pins it. Run
`gofmt`, `go vet`, `go test -race ./...` before pushing; CI runs the
same plus staticcheck and a fuzz smoke.
