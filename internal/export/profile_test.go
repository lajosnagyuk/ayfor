package export

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/render"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

func profileDoc(t *testing.T) (*page.Doc, *typewriter.Profile) {
	t.Helper()
	pkg, err := typewriter.Builtin(typewriter.OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pkg.Profile()
	if err != nil {
		t.Fatal(err)
	}
	h := format.DefaultHeaderV2(42, 0, format.TypewriterRef{ID: p.Ref.ID, Version: p.Ref.Version, Digest: p.Ref.Digest, EngineID: p.Manifest.Engine.ID, EngineVersion: p.Manifest.Engine.Version})
	d := page.NewWithProfile(h, p)
	d.Apply(format.Event{Op: format.OpNewSheet})
	for _, ru := range "Olympia package" {
		if ru == ' ' {
			d.Apply(format.Event{Op: format.OpSpace})
			continue
		}
		d.Apply(format.Event{DeltaMS: 100, Op: format.OpStrike, Rune: ru})
	}
	return d, p
}

func TestProfileRasterPDFIsDeterministicAndEmbedsImage(t *testing.T) {
	d, p := profileDoc(t)
	r, err := render.NewWithProfile(2, p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := PDFRaster(d, r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PDFRaster(d, r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("profile PDF is not deterministic")
	}
	var streamed bytes.Buffer
	if err := PDFRasterTo(&streamed, d, r); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, streamed.Bytes()) {
		t.Fatal("streaming and byte-slice PDF paths differ")
	}
	if !bytes.HasPrefix(a, []byte("%PDF-1.4")) || !bytes.Contains(a, []byte("/Subtype /Image")) {
		t.Fatal("profile PDF does not contain a PDF image object")
	}
}

func TestProfileDOCXNamesPackageTypeface(t *testing.T) {
	d, p := profileDoc(t)
	b, err := DOCXWithProfile(d, p)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, _ := f.Open()
		xml, _ := io.ReadAll(rc)
		rc.Close()
		if !strings.Contains(string(xml), `w:ascii="Cutive Mono"`) {
			t.Fatal("DOCX did not name Cutive Mono")
		}
		return
	}
	t.Fatal("DOCX has no document.xml")
}
