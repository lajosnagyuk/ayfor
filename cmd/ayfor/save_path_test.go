package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveSaveTargetNeverOpensOrTruncatesDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "manuscript.strike")
	want := []byte("irreplaceable existing contents")
	if err := os.WriteFile(target, want, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := targetWithExtension(".strike")(dir, "manuscript")
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("target = %q, want %q", got, target)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(want) {
		t.Fatalf("path selection changed destination: got %q, want %q", after, want)
	}
}

func TestResolveSaveTargetRejectsPathsAndSymlinkFolders(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", ".", "..", "sub/file", `sub\\file`} {
		if _, err := resolveSaveTarget(dir, name); err == nil {
			t.Errorf("name %q was accepted", name)
		}
	}

	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := resolveSaveTarget(link, "manuscript.strike"); err == nil {
		t.Fatal("symlink folder was accepted")
	}
}

func TestExportTargetRequiresSupportedFinalExtension(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"draft.txt", "draft.MD", "draft.docx", "draft.pdf"} {
		if _, err := exportTarget(dir, name); err != nil {
			t.Errorf("%q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"draft", "draft.png", "draft.pdf.exe"} {
		if _, err := exportTarget(dir, name); err == nil {
			t.Errorf("%q was accepted", name)
		}
	}
}
