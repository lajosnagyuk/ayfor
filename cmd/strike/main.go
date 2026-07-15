// Command strike is the toolbox for STRIKE files: inspect, verify the
// hash chain, import plain text, export to md/docx/pdf/png, and replay a
// typing session live in the terminal.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/export"
	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/importer"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/render"
	"github.com/lajosnagyuk/ayfor/internal/safefile"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

const maxTextImportBytes = 1 << 20

func usage() {
	fmt.Fprint(os.Stderr, `strike - STRIKE typewriter file toolbox

usage:
  strike info    <file.strike>
  strike verify  <file.strike>
  strike import  [-seed N] [-typewriter machine.aytw] <in.txt|.md|any text> <out.strike>
  strike export  [-page N] [-scale PXMM] <file.strike> <out.{txt|md|docx|pdf|png}>  (-page/-scale affect png only)
  strike replay  [-speed X] <file.strike>
  strike text    <file.strike>   (plain text to stdout)
  strike typewriter list
  strike typewriter inspect <package.aytw>
  strike typewriter pack <source-dir> <package.aytw>
  strike typewriter install <package.aytw>
  strike typewriter remove <id> <version> <sha256:digest>
  strike typewriter export-builtin <id> <package.aytw>
  strike validate-version <semver>
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "info":
		err = cmdInfo(args)
	case "verify":
		err = cmdVerify(args)
	case "import":
		err = cmdImport(args)
	case "export":
		err = cmdExport(args)
	case "replay":
		err = cmdReplay(args)
	case "text":
		err = cmdText(args)
	case "typewriter":
		err = cmdTypewriter(args)
	case "validate-version":
		if len(args) != 1 {
			usage()
		}
		err = typewriter.ValidateVersion(args[0])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "strike:", err)
		os.Exit(1)
	}
}

func load(path string) (*format.File, error) {
	b, err := safefile.ReadRegular(path, format.MaxFileBytes)
	if err != nil {
		return nil, err
	}
	return format.Decode(b)
}

func resolveProfileForFile(f *format.File, registry *typewriter.Registry) (*typewriter.Profile, error) {
	if f.Header.FormatVersion != format.Version2 {
		return nil, nil
	}
	if registry == nil || f.Header.Typewriter == nil {
		return nil, fmt.Errorf("v2 document requires a typewriter registry")
	}
	tr := f.Header.Typewriter
	profile, err := registry.Resolve(typewriter.Ref{ID: tr.ID, Version: tr.Version, Digest: tr.Digest})
	if err != nil {
		return nil, err
	}
	if profile.Manifest.Engine.ID != tr.EngineID || profile.Manifest.Engine.Version != tr.EngineVersion {
		return nil, fmt.Errorf("resolved package engine %s/%d does not match document %s/%d", profile.Manifest.Engine.ID, profile.Manifest.Engine.Version, tr.EngineID, tr.EngineVersion)
	}
	if err := page.VerifyProfile(f, profile); err != nil {
		return nil, err
	}
	return profile, nil
}

func cmdInfo(args []string) error {
	if len(args) != 1 {
		usage()
	}
	f, err := load(args[0])
	if err != nil {
		return err
	}
	d := page.Replay(f)
	var strikes, sessions, tossed, ribbons int
	var human, imported bool
	var totalMS uint64
	for _, e := range f.Events {
		totalMS += e.DeltaMS
		switch e.Op {
		case format.OpStrike:
			strikes++
		case format.OpNewRibbon:
			ribbons++
		case format.OpSession:
			sessions++
			if e.Origin == format.OriginImported {
				imported = true
			} else {
				human = true
			}
		}
	}
	for _, p := range d.Pages {
		if p.Tossed {
			tossed++
		}
	}
	origin := "human"
	if imported && !human {
		origin = "imported (machine-typed)"
	} else if imported && human {
		origin = "mixed"
	}
	fmt.Printf("file:         %s\n", args[0])
	fmt.Printf("created:      %s\n", time.UnixMilli(f.Header.CreatedUnixMS).UTC().Format(time.RFC3339))
	if f.Header.FormatVersion == format.Version2 {
		fmt.Printf("machine seed: %016X\n", f.Header.Seed)
	} else {
		fmt.Printf("machine seed: %016X (model v%d)\n", f.Header.Seed, f.Header.ModelVersion)
	}
	if f.Header.Typewriter != nil {
		tr := f.Header.Typewriter
		fmt.Printf("typewriter:   %s@%s (%s, engine %s/%d)\n", tr.ID, tr.Version, tr.Digest, tr.EngineID, tr.EngineVersion)
	}
	fmt.Printf("pitch:        %d cpi, line spacing x%.1f\n", f.Header.Pitch, float64(f.Header.LineSpacing)/10)
	fmt.Printf("events:       %d (%d strikes, %d sessions, origin %s)\n", len(f.Events), strikes, sessions, origin)
	fmt.Printf("pages:        %d (%d in the bin)\n", len(d.Pages), tossed)
	fmt.Printf("ribbons:      %d replaced\n", ribbons)
	// "elapsed", not "typing time": DeltaMS sums every in-session pause
	// too, so a two-hour tea break mid-session counts. Actual keystroke
	// rhythm is what replay is for.
	fmt.Printf("elapsed (in session): %s\n", (time.Duration(totalMS) * time.Millisecond).Round(time.Second))
	if f.Truncated {
		fmt.Printf("NOTE: file ends mid-event (crash during append); content is intact\n")
	}
	return nil
}

func cmdVerify(args []string) error {
	if len(args) != 1 {
		usage()
	}
	f, err := load(args[0])
	if err != nil {
		return err
	}
	res, err := format.Verify(f)
	if err != nil {
		return err
	}
	if res.FirstBad != -1 {
		return fmt.Errorf("hash chain BROKEN at event %d of %d (%d checkpoints)", res.FirstBad, len(f.Events), res.Checks)
	}
	if f.Truncated {
		return fmt.Errorf("file ends mid-event; valid prefix has %d events (repair by opening it in ayfor)", len(f.Events))
	}
	if res.Checks == 0 {
		fmt.Println("no checkpoints present (empty or very short file)")
		return nil
	}
	fmt.Printf("hash chain OK: %d checkpoints over %d events\n", res.Checks, len(f.Events))
	return nil
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	seed := fs.Uint64("seed", 0, "machine seed (0 = derive from time)")
	typewriterArchive := fs.String("typewriter", "", "bind import to this .aytw package (default: Ayfor Classic)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
	}
	text, err := safefile.ReadRegular(fs.Arg(0), maxTextImportBytes)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	s := *seed
	if s == 0 {
		s = format.DeriveSeed(now)
	}
	h := format.DefaultHeader(s, now)
	if *typewriterArchive != "" {
		pkg, err := typewriter.LoadArchive(*typewriterArchive)
		if err != nil {
			return err
		}
		profile, err := pkg.Profile()
		if err != nil {
			return err
		}
		if !typewriter.IsLegacyClassic(profile) {
			pm := profile.Manifest
			h = format.DefaultHeaderV2(s, now, format.TypewriterRef{
				ID: profile.Ref.ID, Version: profile.Ref.Version, Digest: profile.Ref.Digest,
				EngineID: pm.Engine.ID, EngineVersion: pm.Engine.Version,
			})
			h.Pitch = units.Pitch(pm.Geometry.PitchCPI)
			h.LineSpacing = units.LineSpacing(pm.Geometry.LineSpacing)
			h.Margins = units.Margins{
				Left: float64(pm.Geometry.DefaultMarginsTenthMM[0]) / 10, Right: float64(pm.Geometry.DefaultMarginsTenthMM[1]) / 10,
				Top: float64(pm.Geometry.DefaultMarginsTenthMM[2]) / 10, Bottom: float64(pm.Geometry.DefaultMarginsTenthMM[3]) / 10,
			}
		}
	}
	// Reserve space for Writer's periodic and final CHECK records under the
	// decoder's total event ceiling.
	const maxImportedEvents = format.MaxEvents - format.MaxEvents/format.CheckInterval - 16
	events, err := importer.ImportLimited(string(text), h, now, maxImportedEvents)
	if err != nil {
		return err
	}

	// Encode to memory and write atomically: a failed import must not
	// leave a truncated .strike under the user's chosen name (the export
	// paths keep the same promise). Imports are text-sized, so buffering
	// the whole encoding is cheap.
	var buf bytes.Buffer
	w, err := format.NewWriter(&buf, h)
	if err != nil {
		return err
	}
	for _, e := range events {
		if err := w.Append(e); err != nil {
			return err
		}
	}
	if err := w.Check(); err != nil {
		return err
	}
	if err := export.AtomicCreateFile(fs.Arg(1), buf.Bytes()); err != nil {
		return err
	}
	fmt.Printf("typed %d events into %s (machine seed %016X)\n", len(events), fs.Arg(1), s)
	return nil
}

func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	pageN := fs.Int("page", 1, "page number for png export (1-based, live pages; png only)")
	scale := fs.Float64("scale", 8, "png pixels per millimetre (png only)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
	}
	f, err := load(fs.Arg(0))
	if err != nil {
		return err
	}
	d := page.Replay(f)
	out := fs.Arg(1)
	ext := strings.ToLower(filepath.Ext(out))
	// Logical exports depend only on the event stream and embedded geometry,
	// so they remain available when an appearance package is missing.
	if ext == ".txt" {
		return export.AtomicCreateFile(out, []byte(export.Text(d)))
	}
	if ext == ".md" {
		return export.AtomicCreateFile(out, []byte(export.Markdown(d)))
	}
	if ext != ".docx" && ext != ".pdf" && ext != ".png" {
		return fmt.Errorf("unknown export format %q (txt, md, docx, pdf, png)", filepath.Ext(out))
	}
	if err := page.VerifyModel(f.Header); err != nil {
		return err
	}
	var profile *typewriter.Profile
	if f.Header.FormatVersion == format.Version2 {
		registry, err := typewriter.DefaultRegistry()
		if err != nil {
			return err
		}
		profile, err = resolveProfileForFile(f, registry)
		if err != nil {
			return err
		}
		d = page.ReplayWithProfile(f, profile)
	}
	switch ext {
	case ".docx":
		var b []byte
		if profile != nil {
			b, err = export.DOCXWithProfile(d, profile)
		} else {
			b, err = export.DOCX(d)
		}
		if err != nil {
			return err
		}
		return export.AtomicCreateFile(out, b)
	case ".pdf":
		var b []byte
		if profile != nil {
			r, rerr := render.NewWithProfile(8, profile)
			if rerr != nil {
				return rerr
			}
			return export.AtomicCreate(out, func(w io.Writer) error {
				return export.PDFRasterTo(w, d, r)
			})
		} else {
			b, err = export.PDF(d)
		}
		if err != nil {
			return err
		}
		return export.AtomicCreateFile(out, b)
	case ".png":
		if *scale <= 0 {
			return fmt.Errorf("scale must be positive, got %g", *scale)
		}
		live := d.LivePages()
		if *pageN < 1 || *pageN > len(live) {
			return fmt.Errorf("page %d of %d live pages", *pageN, len(live))
		}
		var r *render.Renderer
		if profile != nil {
			r, err = render.NewWithProfile(*scale, profile)
		} else {
			r, err = render.New(*scale)
		}
		if err != nil {
			return err
		}
		// Paper grain is seeded by the page's absolute position in the
		// stack, not its live index, so binning a sheet does not regrain
		// the ones after it.
		target := live[*pageN-1]
		absIdx := -1
		for i, p := range d.Pages {
			if p == target {
				absIdx = i
				break
			}
		}
		if absIdx < 0 {
			// Cannot happen (live pages come from d.Pages) - but a silent
			// fallback would export with the wrong grain, the worst way to
			// fail a determinism promise.
			return fmt.Errorf("internal error: live page %d not found in the stack", *pageN)
		}
		img, err := r.RenderPage(target, d.Pitch, d.PaperSeed(absIdx))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return err
		}
		return export.AtomicCreateFile(out, buf.Bytes())
	}
	return nil
}

func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	speed := fs.Float64("speed", 1, "playback speed multiplier (0 = no pauses, print instantly)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	f, err := load(fs.Arg(0))
	if err != nil {
		return err
	}
	// Buffered output, flushed after every event: replay must appear
	// keystroke by keystroke, but unbuffered fmt.Printf paid one write
	// syscall per rune, which at high -speed was thousands of 1-byte
	// writes.
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	fmt.Fprintf(out, "replaying %s at %gx - every pause is as it was typed\n\n", fs.Arg(0), *speed)
	col := 0
	for _, e := range f.Events {
		if e.DeltaMS > 0 && *speed > 0 {
			out.Flush() // show everything typed so far before sleeping
			d := min(time.Duration(float64(e.DeltaMS)/(*speed))*time.Millisecond,
				// cap long thinking pauses
				3*time.Second)
			time.Sleep(d)
		}
		switch e.Op {
		case format.OpStrike:
			fmt.Fprintf(out, "%c", e.Rune)
			col++
		case format.OpSpace:
			out.WriteByte(' ')
			col++
		case format.OpBack:
			if col > 0 {
				out.WriteByte('\b')
				col--
			}
		case format.OpCR:
			out.WriteByte('\n')
			col = 0
		case format.OpNewSheet, format.OpToss:
			out.WriteString("\n----- new sheet -----\n")
			col = 0
		case format.OpNewRibbon:
			out.WriteString("[new ribbon]")
		case format.OpSession:
			origin := "human"
			if e.Origin == format.OriginImported {
				origin = "machine import"
			}
			fmt.Fprintf(out, "[session %s, %s]\n", time.UnixMilli(e.WallUnixMS).UTC().Format(time.RFC3339), origin)
		}
	}
	out.WriteByte('\n')
	return nil
}

func cmdText(args []string) error {
	if len(args) != 1 {
		usage()
	}
	f, err := load(args[0])
	if err != nil {
		return err
	}
	fmt.Print(export.Text(page.Replay(f)))
	return nil
}
