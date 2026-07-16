package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

func TestPackageBoundSessionRoundTrip(t *testing.T) {
	builtins, err := typewriter.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := typewriter.NewRegistry(filepath.Join(t.TempDir(), "registry"), builtins...)
	if err != nil {
		t.Fatal(err)
	}
	pkg, _ := typewriter.Builtin(typewriter.OlympiaDemoID)
	profile, _ := pkg.Profile()
	path := filepath.Join(t.TempDir(), "olympia.strike")
	s, err := NewWithProfile(path, 42, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Strike('A'); err != nil {
		t.Fatal(err)
	}
	want := s.Doc.Pages[0].Strikes[0].App
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if f.Header.FormatVersion != format.Version2 || f.Header.Typewriter.ID != profile.Ref.ID || f.Header.Typewriter.Digest != profile.Ref.Digest {
		t.Fatalf("document did not bind exact package: %+v", f.Header)
	}
	reopened, err := OpenWithRegistry(path, nil, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := reopened.Doc.Pages[0].Strikes[0].App
	if got != want {
		t.Fatalf("reopened appearance changed:\n got %+v\nwant %+v", got, want)
	}
}

func TestV2OpenRequiresExactPackage(t *testing.T) {
	pkg, _ := typewriter.Builtin(typewriter.OlympiaDemoID)
	profile, _ := pkg.Profile()
	path := filepath.Join(t.TempDir(), "missing.strike")
	s, err := NewWithProfile(path, 42, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	classic, _ := typewriter.Builtin(typewriter.ClassicID)
	r, _ := typewriter.NewRegistry(t.TempDir(), classic)
	if _, err := OpenWithRegistry(path, nil, r); !errors.Is(err, typewriter.ErrNotFound) {
		t.Fatalf("missing exact package error = %v", err)
	}
	if _, err := Open(path, nil); !errors.Is(err, typewriter.ErrNotFound) {
		t.Fatalf("resolver-free v2 open error = %v", err)
	}
}

func TestImportTextBindsSelectedPackage(t *testing.T) {
	pkg, err := typewriter.Builtin(typewriter.OlympiaSplendidID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := pkg.Profile()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "import.strike")
	s, err := ImportTextWithProfile(path, "imported", 66, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if f.Header.FormatVersion != format.Version2 || f.Header.Typewriter == nil || f.Header.Typewriter.Digest != profile.Ref.Digest {
		t.Fatalf("import did not bind selected package: %+v", f.Header)
	}
}

func TestImportTextStartsHumanSessionAfterMachineEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "import-provenance.strike")
	s, err := ImportText(path, "hello", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Abort()
	before, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	beforeFile, err := format.Decode(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range beforeFile.Events {
		if e.Op == format.OpSession && e.Origin == format.OriginHuman {
			t.Fatal("unused machine import falsely claimed a human session")
		}
	}
	if _, err := s.Strike('!'); err != nil {
		t.Fatal(err)
	}
	b, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	f, err := format.Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	var imported, human bool
	for _, e := range f.Events {
		if e.Op == format.OpSession && e.Origin == format.OriginImported {
			imported = true
		}
		if e.Op == format.OpSession && e.Origin == format.OriginHuman {
			human = true
		}
	}
	if !imported || !human {
		t.Fatalf("session provenance imported=%v human=%v", imported, human)
	}
}
