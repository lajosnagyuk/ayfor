package export

import (
	"archive/zip"
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/importer"
	"github.com/lajosnagyuk/ayfor/internal/page"
)

func docFromText(t *testing.T, text string) *page.Doc {
	t.Helper()
	h := format.DefaultHeader(7, 0)
	d := page.New(h)
	for _, e := range importer.Import(text, h, 1) {
		d.Apply(e)
	}
	return d
}

func docFromEvents(events []format.Event) *page.Doc {
	d := page.New(format.DefaultHeader(7, 0))
	d.Apply(format.Event{Op: format.OpSession, WallUnixMS: 1})
	d.Apply(format.Event{Op: format.OpNewSheet})
	for _, e := range events {
		d.Apply(e)
	}
	return d
}

func strike(r rune) format.Event { return format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: r} }
func back() format.Event         { return format.Event{DeltaMS: 100, Op: format.OpBack} }

func TestMarkdownPlain(t *testing.T) {
	d := docFromText(t, "hello world\nsecond line")
	md := Markdown(d)
	if !strings.Contains(md, "hello world") || !strings.Contains(md, "second line") {
		t.Fatalf("markdown lost text:\n%s", md)
	}
}

func TestMarkdownDoubleStrikeIsBold(t *testing.T) {
	// Type "hi", back twice, retype "hi": classic typewriter bold.
	d := docFromEvents([]format.Event{
		strike('h'), strike('i'), back(), back(), strike('h'), strike('i'),
	})
	md := Markdown(d)
	if !strings.Contains(md, "**hi**") {
		t.Fatalf("double-strike should export as bold, got %q", md)
	}
}

func TestMarkdownCrossOutIsStrikethrough(t *testing.T) {
	// Type "bad", back over it, x it out.
	d := docFromEvents([]format.Event{
		strike('b'), strike('a'), strike('d'),
		back(), back(), back(),
		strike('x'), strike('x'), strike('x'),
	})
	md := Markdown(d)
	if !strings.Contains(md, "~~bad~~") {
		t.Fatalf("x-ed out text should export struck through, got %q", md)
	}
}

func TestMarkdownOverstrikeLastWins(t *testing.T) {
	d := docFromEvents([]format.Event{
		strike('e'), back(), strike('o'),
	})
	md := Markdown(d)
	if !strings.Contains(md, "o") || strings.Contains(md, "e") {
		t.Fatalf("last glyph should win, got %q", md)
	}
}

func TestMarkdownPageSeparator(t *testing.T) {
	d := docFromText(t, "one\ftwo")
	md := Markdown(d)
	if !strings.Contains(md, "\n---\n") {
		t.Fatalf("pages should be separated by a rule, got %q", md)
	}
}

func TestMarkdownEscaping(t *testing.T) {
	d := docFromText(t, "a*b_c#d")
	md := Markdown(d)
	if !strings.Contains(md, `a\*b\_c\#d`) {
		t.Fatalf("markdown metacharacters must be escaped, got %q", md)
	}
}

// TestMarkdownEscapesHTML pins the < > escapes: most Markdown renderers
// pass raw HTML through, so a typed "<script>" must land as visible text
// in the export, never as markup.
func TestMarkdownEscapesHTML(t *testing.T) {
	d := docFromText(t, "see <b>bold</b> and <script>alert(1)</script>")
	md := Markdown(d)
	if strings.Contains(md, "<script>") || strings.Contains(md, "<b>") {
		t.Fatalf("raw HTML leaked into the markdown export:\n%s", md)
	}
	if !strings.Contains(md, `\<script\>`) {
		t.Fatalf("angle brackets must be backslash-escaped, got:\n%s", md)
	}
}

// TestDOCXDeterministicEntryOrder pins the zip layout: same document =
// byte-identical .docx, which requires a fixed entry order (a map-ordered
// zip is different bytes on every run), with [Content_Types].xml first as
// OPC consumers prefer.
func TestDOCXDeterministicEntryOrder(t *testing.T) {
	d := docFromText(t, "determinism is the brand")
	first, err := DOCX(d)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DOCX(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("two DOCX exports of the same document differ byte-for-byte")
	}
	zr, err := zip.NewReader(bytes.NewReader(first), int64(len(first)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"}
	if len(zr.File) != len(want) {
		t.Fatalf("zip has %d entries, want %d", len(zr.File), len(want))
	}
	for i, f := range zr.File {
		if f.Name != want[i] {
			t.Fatalf("zip entry %d = %q, want %q", i, f.Name, want[i])
		}
	}
}

func TestDOCXStructure(t *testing.T) {
	d := docFromText(t, "hello docx\nline two")
	b, err := DOCX(d)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	var docXML string
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			var sb strings.Builder
			buf := make([]byte, 64*1024)
			for {
				n, err := rc.Read(buf)
				sb.Write(buf[:n])
				if err != nil {
					break
				}
			}
			rc.Close()
			docXML = sb.String()
		}
	}
	for _, want := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		if !names[want] {
			t.Fatalf("docx missing %s", want)
		}
	}
	if !strings.Contains(docXML, "hello docx") {
		t.Fatal("docx lost text")
	}
	if !strings.Contains(docXML, "Courier Prime") {
		t.Fatal("docx must set the monospace face")
	}
}

func TestDOCXBoldAndStrike(t *testing.T) {
	d := docFromEvents([]format.Event{
		strike('h'), back(), strike('h'), // bold h
		strike('a'), back(), strike('x'), // struck a
	})
	b, err := DOCX(d)
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			var sb strings.Builder
			buf := make([]byte, 64*1024)
			for {
				n, err := rc.Read(buf)
				sb.Write(buf[:n])
				if err != nil {
					break
				}
			}
			rc.Close()
			s := sb.String()
			if !strings.Contains(s, "<w:b/>") {
				t.Fatal("missing bold run")
			}
			if !strings.Contains(s, "<w:strike/>") {
				t.Fatal("missing strikethrough run")
			}
		}
	}
}

func TestPDFStructure(t *testing.T) {
	d := docFromText(t, "hello pdf (with parens) and a second line\nindeed\fpage two")
	b, err := PDF(d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "%PDF-1.4") {
		t.Fatal("missing PDF header")
	}
	if !strings.Contains(s, "/Count 2") {
		t.Fatal("expected two pages")
	}
	// Each strike is its own one-character Tj, so an escaped paren
	// stands alone.
	if !strings.Contains(s, `(\() Tj`) || !strings.Contains(s, `(\)) Tj`) {
		t.Fatal("parentheses must be escaped in strings")
	}
	if !strings.HasSuffix(strings.TrimSpace(s), "%%EOF") {
		t.Fatal("missing EOF marker")
	}
	// Every strike gets its own Tm; "hello pdf..." has plenty.
	if strings.Count(s, " Tm\n") < 20 {
		t.Fatal("expected per-strike text matrices")
	}
	// xref offsets must actually point at objects.
	xref := strings.Index(s, "xref\n")
	if xref == -1 {
		t.Fatal("missing xref")
	}
	lines := strings.Split(s[xref:], "\n")
	for _, ln := range lines[3:6] { // first few entries after the free head
		if len(ln) < 10 {
			break
		}
		off, err := strconv.Atoi(ln[:10])
		if err != nil {
			continue
		}
		if off > 0 && off < len(s) {
			rest := s[off:]
			if !strings.Contains(rest[:20], " 0 obj") {
				t.Fatalf("xref offset %d does not point at an object: %q", off, rest[:20])
			}
		}
	}
}

func TestPDFInkVaries(t *testing.T) {
	// A pause then fast typing must produce different gray levels.
	d := docFromEvents([]format.Event{
		{DeltaMS: 3000, Op: format.OpStrike, Rune: 'H'},
		{DeltaMS: 40, Op: format.OpStrike, Rune: 'e'},
		{DeltaMS: 35, Op: format.OpStrike, Rune: 'y'},
	})
	b, err := PDF(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), " g\n") < 2 {
		t.Fatal("expected at least two distinct ink levels")
	}
}
