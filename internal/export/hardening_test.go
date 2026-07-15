package export

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docxDocumentXML extracts word/document.xml from a DOCX archive.
func docxDocumentXML(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			return string(data)
		}
	}
	t.Fatal("word/document.xml not found")
	return ""
}

// TestEscapeLeadingBlockMarker pins that line-leading Markdown block
// constructs are neutralised, while ordinary text and non-markers are not.
func TestEscapeLeadingBlockMarker(t *testing.T) {
	cases := map[string]string{
		"- item":     `\- item`,
		"+ item":     `\+ item`,
		"> quote":    `\> quote`,
		"1. first":   `1\. first`,
		"12) second": `12\) second`,
		"|a|b":       `\|a|b`,
		"  - indent": `  \- indent`,
		"hello":      "hello",
		"-dash":      "-dash", // no space: not a list marker
		"1.no":       "1.no",  // no space: not an ordered list
	}
	for in, want := range cases {
		if got := escapeLeadingBlockMarker(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// TestMarkdownLeadingMarkerEscaped pins the behaviour end to end: a typed
// line that looks like a list is escaped so a renderer shows it verbatim.
func TestMarkdownLeadingMarkerEscaped(t *testing.T) {
	d := docFromText(t, "- not a list")
	if md := Markdown(d); !strings.Contains(md, `\- not a list`) {
		t.Fatalf("leading dash not escaped:\n%s", md)
	}
}

// TestDOCXHasA4Section pins that the document declares an A4 page with the
// document's margins, so Word does not rewrap fixed-width lines to Letter.
func TestDOCXHasA4Section(t *testing.T) {
	d := docFromText(t, "hello")
	b, err := DOCX(d)
	if err != nil {
		t.Fatal(err)
	}
	doc := docxDocumentXML(t, b)
	for _, want := range []string{"<w:sectPr>", `w:w="11906"`, `w:h="16838"`, "<w:pgMar"} {
		if !strings.Contains(doc, want) {
			t.Errorf("DOCX missing %q", want)
		}
	}
}

// TestTextIsPlainNoMarkup pins that Text emits the characters as typed with
// no Markdown escaping or bold/strike markup, and separates sheets with a
// form feed that import reads back.
func TestTextIsPlainNoMarkup(t *testing.T) {
	d := docFromText(t, "a*b_c#d")
	got := Text(d)
	if !strings.Contains(got, "a*b_c#d") {
		t.Fatalf("plain text should not escape metacharacters: %q", got)
	}
	if strings.Contains(got, `\*`) || strings.Contains(got, "**") {
		t.Fatalf("plain text leaked markup: %q", got)
	}

	// Two sheets are separated by a form feed.
	two := Text(docFromText(t, "one\ftwo"))
	if !strings.Contains(two, "\f") {
		t.Fatalf("sheets not separated by a form feed: %q", two)
	}
}

// TestAtomicCreateFileLeavesNoTemp pins that a successful write publishes into
// place and leaves no temporary file behind.
func TestAtomicCreateFileLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.md")
	if err := AtomicCreateFile(path, []byte("content")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "content" {
		t.Fatalf("content = %q, err = %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly the output file, got %d entries", len(entries))
	}
}
