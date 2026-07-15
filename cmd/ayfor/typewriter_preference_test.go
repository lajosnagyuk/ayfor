package main

import (
	"strings"
	"testing"

	fynetest "fyne.io/fyne/v2/test"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

func TestSelectedTypewriterFailsClosedWhenPreferredPackageIsMissing(t *testing.T) {
	app := fynetest.NewTempApp(t)
	prefs := app.Preferences()
	prefs.SetString("typewriterID", "io.example.typewriters.missing")
	prefs.SetString("typewriterVersion", "1.2.3")
	prefs.SetString("typewriterDigest", "sha256:"+strings.Repeat("a", 64))

	registry, err := typewriter.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := selectedTypewriter(registry, prefs)
	if err == nil || profile != nil {
		t.Fatalf("missing preferred package returned profile %#v, error %v", profile, err)
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error does not explain the failed preference: %v", err)
	}
}

func TestSelectedTypewriterUsesClassicForLegacyEmptyPreference(t *testing.T) {
	app := fynetest.NewTempApp(t)
	registry, err := typewriter.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := selectedTypewriter(registry, app.Preferences())
	if err != nil {
		t.Fatal(err)
	}
	if profile.Ref.ID != typewriter.ClassicID {
		t.Fatalf("empty legacy preference selected %s", profile.Ref.ID)
	}
}

func TestSelectedTypewriterRejectsPartialPreference(t *testing.T) {
	app := fynetest.NewTempApp(t)
	app.Preferences().SetString("typewriterID", typewriter.OlympiaDemoID)
	registry, err := typewriter.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if profile, err := selectedTypewriter(registry, app.Preferences()); err == nil || profile != nil {
		t.Fatalf("partial preference returned profile %#v, error %v", profile, err)
	}
}

func TestPreferredTypewriterIsStoredAsOneReference(t *testing.T) {
	app := fynetest.NewTempApp(t)
	prefs := app.Preferences()
	pkg, err := typewriter.Builtin(typewriter.OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	setPreferredTypewriter(prefs, pkg.Ref())
	if prefs.String("typewriterRef") == "" {
		t.Fatal("single reference preference was not written")
	}
	for _, old := range []string{"typewriterID", "typewriterVersion", "typewriterDigest"} {
		if prefs.String(old) != "" {
			t.Fatalf("legacy partial-write key %s remains", old)
		}
	}
	registry, err := typewriter.NewRegistry(t.TempDir(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := selectedTypewriter(registry, prefs)
	if err != nil || profile.Ref != pkg.Ref() {
		t.Fatalf("selected profile %#v, error %v", profile, err)
	}
}
