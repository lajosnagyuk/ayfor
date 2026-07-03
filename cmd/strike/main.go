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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/export"
	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/importer"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/render"
)

func usage() {
	fmt.Fprint(os.Stderr, `strike - STRIKE typewriter file toolbox

usage:
  strike info    <file.strike>
  strike verify  <file.strike>
  strike import  [-seed N] <in.txt|.md|any text> <out.strike>
  strike export  [-page N] [-scale PXMM] <file.strike> <out.{txt|md|docx|pdf|png}>  (-page/-scale affect png only)
  strike replay  [-speed X] <file.strike>
  strike text    <file.strike>   (plain text to stdout)
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
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "strike:", err)
		os.Exit(1)
	}
}

func load(path string) (*format.File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return format.Decode(b)
}

// loadForRender is load plus the model-version check: export, replay and
// text derive appearance/content from the personality model, and doing
// that with the wrong model version would misrepresent the manuscript.
// info and verify stay on plain load - metadata and hash chains are
// version-independent.
func loadForRender(path string) (*format.File, error) {
	f, err := load(path)
	if err != nil {
		return nil, err
	}
	if err := page.VerifyModel(f.Header); err != nil {
		return nil, err
	}
	return f, nil
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
	fmt.Printf("machine seed: %016X (model v%d)\n", f.Header.Seed, f.Header.ModelVersion)
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
	if res.Checks == 0 {
		fmt.Println("no checkpoints present (empty or very short file)")
		return nil
	}
	if res.FirstBad != -1 {
		return fmt.Errorf("hash chain BROKEN at event %d of %d (%d checkpoints)", res.FirstBad, len(f.Events), res.Checks)
	}
	fmt.Printf("hash chain OK: %d checkpoints over %d events\n", res.Checks, len(f.Events))
	return nil
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	seed := fs.Uint64("seed", 0, "machine seed (0 = derive from time)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
	}
	text, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	s := *seed
	if s == 0 {
		s = format.DeriveSeed(now)
	}
	h := format.DefaultHeader(s, now)
	events := importer.Import(string(text), h, now)

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
	if err := export.AtomicWriteFile(fs.Arg(1), buf.Bytes()); err != nil {
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
	f, err := loadForRender(fs.Arg(0))
	if err != nil {
		return err
	}
	d := page.Replay(f)
	out := fs.Arg(1)
	switch strings.ToLower(filepath.Ext(out)) {
	case ".txt":
		return export.AtomicWriteFile(out, []byte(export.Text(d)))
	case ".md":
		return export.AtomicWriteFile(out, []byte(export.Markdown(d)))
	case ".docx":
		b, err := export.DOCX(d)
		if err != nil {
			return err
		}
		return export.AtomicWriteFile(out, b)
	case ".pdf":
		b, err := export.PDF(d)
		if err != nil {
			return err
		}
		return export.AtomicWriteFile(out, b)
	case ".png":
		if *scale <= 0 {
			return fmt.Errorf("scale must be positive, got %g", *scale)
		}
		live := d.LivePages()
		if *pageN < 1 || *pageN > len(live) {
			return fmt.Errorf("page %d of %d live pages", *pageN, len(live))
		}
		r, err := render.New(*scale)
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
		return export.AtomicWriteFile(out, buf.Bytes())
	default:
		return fmt.Errorf("unknown export format %q (txt, md, docx, pdf, png)", filepath.Ext(out))
	}
}

func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ExitOnError)
	speed := fs.Float64("speed", 1, "playback speed multiplier (0 = no pauses, print instantly)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	f, err := loadForRender(fs.Arg(0))
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
	f, err := loadForRender(args[0])
	if err != nil {
		return err
	}
	fmt.Print(export.Text(page.Replay(f)))
	return nil
}
