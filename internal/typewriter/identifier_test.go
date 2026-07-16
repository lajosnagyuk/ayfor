package typewriter

import "testing"

func TestPortablePackageIDGrammar(t *testing.T) {
	accepted := []string{
		"io.example.machine",
		"uk.co.example.typewriter-66",
		"a.b",
	}
	for _, id := range accepted {
		if !idPattern.MatchString(id) || hasWindowsReservedIDComponent(id) {
			t.Errorf("portable id rejected: %q", id)
		}
	}
	rejected := []string{
		"single",
		"foo-bar",
		"io.example_bad.machine",
		"io.-example.machine",
		"io.example-.machine",
		"con.vendor.machine",
		"io.aux.machine",
		"io.com1.machine",
		"io.LPT9.machine",
	}
	for _, id := range rejected {
		if idPattern.MatchString(id) && !hasWindowsReservedIDComponent(id) {
			t.Errorf("non-portable id accepted: %q", id)
		}
	}
}

func TestPathsRejectTerminalControls(t *testing.T) {
	for _, path := range []string{"fonts/\x1b]52;c;evil.ttf", "sounds/hammer\rforged.wav", "fonts/right\u202eto-left.ttf"} {
		if err := safeRelativeFile(path); err == nil {
			t.Errorf("unsafe path accepted: %q", path)
		}
	}
}

func TestReferenceIdentityIsCanonical(t *testing.T) {
	p, err := Builtin(OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistry(t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	badVersion := p.Ref()
	badVersion.Version = "0.1.0-RC"
	if _, err := r.archivePath(badVersion); err == nil {
		t.Fatal("accepted noncanonical uppercase SemVer reference")
	}
	badDigest := p.Ref()
	badDigest.Digest = "sha256:" + "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	if _, err := r.archivePath(badDigest); err == nil {
		t.Fatal("accepted uppercase digest reference")
	}
}
