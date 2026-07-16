package typewriter

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lajosnagyuk/ayfor/assets"
)

func TestBuiltinsMaterialize(t *testing.T) {
	all, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d builtins, want 3", len(all))
	}
	for _, p := range all {
		profile, err := p.Profile()
		if err != nil {
			t.Fatalf("%s: %v", p.Manifest.ID, err)
		}
		if profile.Ref.Digest != p.Digest || len(profile.Font) == 0 || len(profile.HammerPCM16) != 5 {
			t.Fatalf("%s did not fully materialize", p.Manifest.ID)
		}
	}
	classic, _ := Builtin(ClassicID)
	p, _ := classic.Profile()
	if !bytes.Equal(p.Font, assets.CourierPrimeRegular) {
		t.Fatal("classic package font differs from legacy embedded font")
	}
}

func TestBuiltinSourcesMatchPinnedReleases(t *testing.T) {
	filenames := map[string]string{
		ClassicID:         "classic-1.0.0.aytw",
		OlympiaDemoID:     "olympia-sm3-pica-1957-0.1.0.aytw",
		OlympiaSplendidID: "olympia-splendid-66-1967-0.1.0.aytw",
	}
	for _, id := range []string{ClassicID, OlympiaDemoID, OlympiaSplendidID} {
		source, err := LoadSourceFS(os.DirFS("../../assets"), "typewriters/"+id)
		if err != nil {
			t.Fatal(err)
		}
		release, err := Builtin(id)
		if err != nil {
			t.Fatal(err)
		}
		if source.Ref() != release.Ref() {
			t.Fatalf("source %s is %s, pinned release is %s; publish a new version/archive instead of mutating a release", id, source.Ref(), release.Ref())
		}
		canonical, err := source.Archive()
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := assets.TypewriterReleases.ReadFile("typewriter-releases/" + filenames[id])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(canonical, embedded) {
			t.Fatalf("%s embedded release bytes are not the canonical archive produced from source", id)
		}
	}
}

func TestArchiveDeterministicAndRoundTrips(t *testing.T) {
	p, err := Builtin(OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.Archive()
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Archive()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same package did not produce byte-identical archives")
	}
	got, err := LoadArchiveBytes(a)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref() != p.Ref() {
		t.Fatalf("round trip ref = %v, want %v", got.Ref(), p.Ref())
	}
	profile, err := got.Profile()
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Glyphs) == 0 {
		t.Fatal("Olympia calibration was lost")
	}
}

func archiveFromFiles(t *testing.T, root string, files map[string][]byte, mutateLock func(*Lock)) []byte {
	t.Helper()
	digest, hashes := contentDigest(files)
	lock := Lock{Schema: 1, Digest: digest, Files: hashes}
	if mutateLock != nil {
		mutateLock(&lock)
	}
	lb, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for name, data := range files {
		w, err := zw.Create(root + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	w, err := zw.Create(root + "/" + lockName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(lb); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func cloneBuiltinFiles(t *testing.T) (string, map[string][]byte) {
	t.Helper()
	p, err := Builtin(ClassicID)
	if err != nil {
		t.Fatal(err)
	}
	return p.Manifest.ID, cloneFiles(p.Files)
}

func TestRejectsTamperedLock(t *testing.T) {
	root, files := cloneBuiltinFiles(t)
	b := archiveFromFiles(t, root, files, func(l *Lock) { l.Digest = "sha256:" + strings.Repeat("0", 64) })
	if _, err := LoadArchiveBytes(b); err == nil {
		t.Fatal("accepted incorrect content digest")
	}
}

func TestRejectsUnknownManifestField(t *testing.T) {
	root, files := cloneBuiltinFiles(t)
	files[manifestName] = bytes.Replace(files[manifestName], []byte(`"schema": 1`), []byte(`"schema": 1, "surprise": true`), 1)
	b := archiveFromFiles(t, root, files, nil)
	if _, err := LoadArchiveBytes(b); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestRejectsDuplicateJSONKeys(t *testing.T) {
	root, files := cloneBuiltinFiles(t)
	files[manifestName] = bytes.Replace(files[manifestName], []byte(`"schema": 1`), []byte(`"schema": 1, "schema": 1`), 1)
	if _, err := LoadArchiveBytes(archiveFromFiles(t, root, files, nil)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate manifest key error = %v", err)
	}
	root, files = cloneBuiltinFiles(t)
	b := archiveFromFiles(t, root, files, func(l *Lock) {})
	// The generated lock contains one schema key; inject another into the
	// stored archive by rebuilding it explicitly.
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for name, data := range files {
		w, _ := zw.Create(root + "/" + name)
		w.Write(data)
	}
	digest, hashes := contentDigest(files)
	lb, _ := json.Marshal(Lock{Schema: 1, Digest: digest, Files: hashes})
	lb = bytes.Replace(lb, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1)
	w, _ := zw.Create(root + "/" + lockName)
	w.Write(lb)
	zw.Close()
	_ = b
	if _, err := LoadArchiveBytes(out.Bytes()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate lock key error = %v", err)
	}
}

func TestRejectsUnlistedAndMissingAssets(t *testing.T) {
	root, files := cloneBuiltinFiles(t)
	files["payload.bin"] = []byte("nope")
	if _, err := LoadArchiveBytes(archiveFromFiles(t, root, files, nil)); err == nil {
		t.Fatal("accepted unlisted file")
	}
	root, files = cloneBuiltinFiles(t)
	delete(files, "fonts/CourierPrime-Regular.ttf")
	if _, err := LoadArchiveBytes(archiveFromFiles(t, root, files, nil)); err == nil {
		t.Fatal("accepted missing font")
	}
}

func TestRejectsHostileArchivePaths(t *testing.T) {
	cases := []string{
		"root/../escape",
		"/absolute/file",
		"root/back\\slash",
		"root/a/../../escape",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			zw := zip.NewWriter(&out)
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			w.Write([]byte("x"))
			zw.Close()
			if _, err := LoadArchiveBytes(out.Bytes()); err == nil {
				t.Fatalf("accepted hostile path %q", name)
			}
		})
	}
}

func TestRejectsSymlinkAndCaseCollision(t *testing.T) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	h := &zip.FileHeader{Name: "root/link"}
	h.SetMode(fs.ModeSymlink | 0o777)
	w, _ := zw.CreateHeader(h)
	w.Write([]byte("target"))
	zw.Close()
	if _, err := LoadArchiveBytes(out.Bytes()); err == nil {
		t.Fatal("accepted symlink")
	}

	out.Reset()
	zw = zip.NewWriter(&out)
	h = &zip.FileHeader{Name: "root/link/"}
	h.SetMode(fs.ModeSymlink | 0o777)
	w, _ = zw.CreateHeader(h)
	w.Write([]byte("target"))
	zw.Close()
	if _, err := LoadArchiveBytes(out.Bytes()); err == nil {
		t.Fatal("accepted trailing-slash symlink")
	}

	out.Reset()
	zw = zip.NewWriter(&out)
	for _, name := range []string{"root/A", "root/a"} {
		w, _ := zw.Create(name)
		w.Write([]byte("x"))
	}
	zw.Close()
	if _, err := LoadArchiveBytes(out.Bytes()); err == nil {
		t.Fatal("accepted case collision")
	}
}

func TestRejectsNoncanonicalDirectoryPaths(t *testing.T) {
	root, files := cloneBuiltinFiles(t)
	valid := archiveFromFiles(t, root, files, nil)
	zr, err := zip.NewReader(bytes.NewReader(valid), int64(len(valid)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, source := range zr.File {
		r, err := source.Open()
		if err != nil {
			t.Fatal(err)
		}
		w, err := zw.CreateHeader(&source.FileHeader)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(w, r); err != nil {
			t.Fatal(err)
		}
		_ = r.Close()
	}
	h := &zip.FileHeader{Name: root + "/\u202eevil/"}
	h.SetMode(fs.ModeDir | 0o755)
	if _, err := zw.CreateHeader(h); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadArchiveBytes(out.Bytes()); err == nil {
		t.Fatal("accepted explicit directory containing a formatting control")
	}

	parent := t.TempDir()
	source := filepath.Join(parent, ClassicID)
	if err := os.CopyFS(source, os.DirFS(filepath.Join("..", "..", "assets", "typewriters", ClassicID))); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "\u202eevil"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSourceFS(os.DirFS(parent), ClassicID); err == nil {
		t.Fatal("accepted source directory containing a formatting control")
	}
}

func TestRejectsUnlistedLicenseOrProvenancePayload(t *testing.T) {
	for _, name := range []string{"licenses/install.sh", "provenance/payload.bin"} {
		t.Run(name, func(t *testing.T) {
			root, files := cloneBuiltinFiles(t)
			files[name] = []byte("unlisted")
			if _, err := LoadArchiveBytes(archiveFromFiles(t, root, files, nil)); err == nil {
				t.Fatalf("accepted unlisted %q", name)
			}
		})
	}
}

func TestCanonicalVersionAndReadableLegalRecords(t *testing.T) {
	for _, version := range []string{"1.0.0-RC", "1.0.0-01", "1.0.0-.bad"} {
		if err := ValidateVersion(version); err == nil {
			t.Errorf("accepted noncanonical version %q", version)
		}
	}
	if err := ValidateVersion("1.2.3-rc.1+build.7"); err != nil {
		t.Fatalf("rejected canonical version: %v", err)
	}

	p, err := Builtin(OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	files := cloneFiles(p.Files)
	files[p.Manifest.Licenses[0]] = []byte{0xff, 0xfe, 0x00}
	digest, _ := contentDigest(files)
	hostile := &Package{Manifest: p.Manifest, Digest: digest, Files: files}
	if _, err := hostile.Profile(); err == nil {
		t.Fatal("accepted binary license record")
	}
	files = cloneFiles(p.Files)
	files[p.Manifest.Provenance] = []byte(" \n\t")
	digest, _ = contentDigest(files)
	hostile = &Package{Manifest: p.Manifest, Digest: digest, Files: files}
	if _, err := hostile.Profile(); err == nil {
		t.Fatal("accepted empty provenance record")
	}
}

func TestRegistryRejectsConflictingBuiltinVersion(t *testing.T) {
	p, err := Builtin(OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	files := cloneFiles(p.Files)
	files["provenance/README.md"] = append(files["provenance/README.md"], []byte("\nconflict\n")...)
	digest, _ := contentDigest(files)
	conflict := &Package{Manifest: p.Manifest, Digest: digest, Files: files}
	if _, err := NewRegistry(t.TempDir(), p, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting builtins error = %v", err)
	}
}

func TestRegistryInstallResolveConflictAndRemove(t *testing.T) {
	classic, _ := Builtin(ClassicID)
	r, err := NewRegistry(t.TempDir(), classic)
	if err != nil {
		t.Fatal(err)
	}
	olympia, _ := Builtin(OlympiaDemoID)
	b, _ := olympia.Archive()
	file := filepath.Join(t.TempDir(), "olympia.aytw")
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := r.Install(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(item.Ref); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(item.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(item.Ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve after remove = %v", err)
	}
	if err := r.Remove(classic.Ref()); err == nil {
		t.Fatal("removed builtin")
	}

	// Same ID/version with different manifest bytes is a supply-chain conflict.
	files := cloneFiles(olympia.Files)
	files["provenance/README.md"] = append(files["provenance/README.md"], []byte("\nchanged\n")...)
	digest, _ := contentDigest(files)
	other := &Package{Manifest: olympia.Manifest, Digest: digest, Files: files}
	otherBytes, _ := other.Archive()
	otherFile := filepath.Join(t.TempDir(), "other.aytw")
	os.WriteFile(otherFile, otherBytes, 0o644)
	// Reinstall the original, then ensure the conflicting artifact is refused.
	if _, err := r.Install(file); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Install(otherFile); err == nil {
		t.Fatal("accepted ID/version digest conflict")
	}
}

func TestInstallFullyValidatesBeforePublishing(t *testing.T) {
	olympia, err := Builtin(OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	files := cloneFiles(olympia.Files)
	files[olympia.Manifest.Typeface.Path] = []byte("not a font")
	digest, _ := contentDigest(files)
	bad := &Package{Manifest: olympia.Manifest, Digest: digest, Files: files}
	b, err := bad.Archive()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "bad.aytw")
	if err := os.WriteFile(archive, b, 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := NewRegistry(t.TempDir())
	if _, err := r.Install(archive); err == nil {
		t.Fatal("installed a package whose font cannot be materialized")
	}
	items, err := r.List()
	if err != nil || len(items) != 0 {
		t.Fatalf("failed install mutated registry: items=%v err=%v", items, err)
	}
}

func TestConcurrentConflictingInstallsPinOneDigest(t *testing.T) {
	olympia, err := Builtin(OlympiaDemoID)
	if err != nil {
		t.Fatal(err)
	}
	makeArchive := func(suffix string) string {
		files := cloneFiles(olympia.Files)
		files["provenance/README.md"] = append(files["provenance/README.md"], []byte(suffix)...)
		digest, _ := contentDigest(files)
		p := &Package{Manifest: olympia.Manifest, Digest: digest, Files: files}
		b, err := p.Archive()
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(t.TempDir(), strings.TrimSpace(suffix)+".aytw")
		if err := os.WriteFile(name, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return name
	}
	a, b := makeArchive("a"), makeArchive("b")
	r, _ := NewRegistry(t.TempDir())
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Install(name)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected install error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want one each", successes, conflicts)
	}
	items, err := r.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("registry after race: items=%v err=%v", items, err)
	}
}

func TestLoadArchiveRejectsOversizedFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "huge.aytw")
	if err := os.WriteFile(file, make([]byte, maxArchiveBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadArchive(file); err == nil {
		t.Fatal("accepted oversized archive")
	}
}

func TestZIPPreflightCountsCentralRecordsInsteadOfTrustingEOCD(t *testing.T) {
	const records = maxArchiveEntries + 1
	b := make([]byte, records*46+22)
	for i := range records {
		binary.LittleEndian.PutUint32(b[i*46:i*46+4], 0x02014b50)
	}
	eocd := b[records*46:]
	binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50)
	binary.LittleEndian.PutUint16(eocd[8:10], 1)
	binary.LittleEndian.PutUint16(eocd[10:12], 1)
	binary.LittleEndian.PutUint32(eocd[12:16], records*46)
	if err := preflightZIP(bytes.NewReader(b), int64(len(b))); err == nil {
		t.Fatal("preflight trusted wrapped/false EOCD entry count")
	}
}

func TestReinstallRepairsCorruptCanonicalArchive(t *testing.T) {
	olympia, _ := Builtin(OlympiaDemoID)
	data, _ := olympia.Archive()
	source := filepath.Join(t.TempDir(), "olympia.aytw")
	_ = os.WriteFile(source, data, 0o644)
	r, _ := NewRegistry(t.TempDir())
	item, err := r.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(item.Path, []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Install(source); err != nil {
		t.Fatalf("reinstall did not heal canonical archive: %v", err)
	}
	if _, err := r.Resolve(item.Ref); err != nil {
		t.Fatalf("healed archive does not resolve: %v", err)
	}
}

func TestInstallRejectsUnsafeClaimWithoutReadingIt(t *testing.T) {
	olympia, _ := Builtin(OlympiaDemoID)
	data, _ := olympia.Archive()
	source := filepath.Join(t.TempDir(), "olympia.aytw")
	_ = os.WriteFile(source, data, 0o644)
	r, _ := NewRegistry(t.TempDir())
	target, _ := r.archivePath(olympia.Ref())
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(secret, []byte(strings.Repeat("DO-NOT-LEAK", 1000)), 0o644)
	if err := os.Symlink(secret, filepath.Join(filepath.Dir(target), ".ref")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := r.Install(source)
	if err == nil || !strings.Contains(err.Error(), "malformed registry claim") || strings.Contains(err.Error(), "DO-NOT-LEAK") {
		t.Fatalf("unsafe claim error = %v", err)
	}
}

func TestBuiltinsCarryStandaloneLicencesAndPinnedProvenance(t *testing.T) {
	all, err := Builtins()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range all {
		packageLicence := string(p.Files["licenses/package.txt"])
		if !strings.Contains(packageLicence, "BSD 3-Clause License") ||
			!strings.Contains(packageLicence, "THIS SOFTWARE IS PROVIDED") {
			t.Errorf("%s lacks a complete standalone package licence", p.Ref())
		}
		sounds := string(p.Files["licenses/sounds.txt"])
		for _, required := range []string{
			"keithpeter (Freesound)",
			"https://pixabay.com/sound-effects/typewriter-olivetti-lettra-22-20217/",
			"https://pixabay.com/service/terms/",
			"Downloaded 2026-07-02",
		} {
			if !strings.Contains(sounds, required) {
				t.Errorf("%s sound notice lacks %q", p.Ref(), required)
			}
		}
		provenance := string(p.Files[p.Manifest.Provenance])
		if !strings.Contains(provenance, "SHA-256") || !strings.Contains(provenance, "commit") {
			t.Errorf("%s lacks immutable font provenance", p.Ref())
		}
	}
}
