package typewriter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/lajosnagyuk/ayfor/assets"
)

var builtinReleaseDigests = map[string]string{
	"classic-1.0.0.aytw":                  "sha256:050ddbc51a829798e966e1fe90c5fd9910be5dd0a59650c31f5263e117e9c949",
	"olympia-sm3-pica-1957-0.1.0.aytw":    "sha256:1a880597a632bf4b7f9abf23f90a07fb137cbf7ad95a21975c51f665bcbca2b0",
	"olympia-splendid-66-1967-0.1.0.aytw": "sha256:b6bd0413e9772437e0fa1161b43fed7506387749f90fec350c668d3d21dbdd0f",
}

var builtinArchiveSHA256 = map[string]string{
	"classic-1.0.0.aytw":                  "bb2802feb60daa1db354199a1acc0a6c51ddcb24c78de46ed569791d589345cf",
	"olympia-sm3-pica-1957-0.1.0.aytw":    "3f0d0d34579869dc8292c0c816071232baf8944db593ffb0bdfa31c70445de81",
	"olympia-splendid-66-1967-0.1.0.aytw": "0a8a9873d6f39d642fa22c239735964427a0908478a4220e85c6576c23cfb76b",
}

var (
	legacyClassicOnce sync.Once
	legacyClassicRef  Ref
	legacyClassicErr  error
)

const (
	ClassicID         = "io.ayfor.typewriters.classic"
	OlympiaDemoID     = "io.ayfor.typewriters.olympia-sm3-pica-1957"
	OlympiaSplendidID = "io.ayfor.typewriters.olympia-splendid-66-1967"
)

// Builtins loads and validates every package embedded in the executable.
// Failure is a build/release defect, so callers normally treat it as fatal at
// startup rather than falling back to unverified assets.
func Builtins() ([]*Package, error) {
	entries, err := assets.TypewriterReleases.ReadDir("typewriter-releases")
	if err != nil {
		return nil, fmt.Errorf("read built-in releases: %w", err)
	}
	out := make([]*Package, 0, len(entries))
	seenFiles := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".aytw" {
			continue
		}
		expected, pinned := builtinReleaseDigests[entry.Name()]
		if !pinned {
			return nil, fmt.Errorf("built-in release %s has no pinned digest", entry.Name())
		}
		data, err := assets.TypewriterReleases.ReadFile("typewriter-releases/" + entry.Name())
		if err != nil {
			return nil, err
		}
		rawExpected, rawPinned := builtinArchiveSHA256[entry.Name()]
		rawSum := sha256.Sum256(data)
		if !rawPinned || hex.EncodeToString(rawSum[:]) != rawExpected {
			return nil, fmt.Errorf("built-in release %s canonical archive bytes changed", entry.Name())
		}
		p, err := LoadArchiveBytes(data)
		if err != nil {
			return nil, fmt.Errorf("built-in release %s: %w", entry.Name(), err)
		}
		if p.Digest != expected {
			return nil, fmt.Errorf("built-in release %s digest changed: got %s, pinned %s; publish a new version instead", entry.Name(), p.Digest, expected)
		}
		if _, err := p.Profile(); err != nil {
			return nil, fmt.Errorf("built-in release %s: %w", entry.Name(), err)
		}
		out = append(out, p)
		seenFiles[entry.Name()] = true
	}
	for name := range builtinReleaseDigests {
		if !seenFiles[name] {
			return nil, fmt.Errorf("pinned built-in release %s is not embedded", name)
		}
	}
	for name := range builtinArchiveSHA256 {
		if !seenFiles[name] {
			return nil, fmt.Errorf("raw-pinned built-in release %s is not embedded", name)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Manifest.ID != out[j].Manifest.ID {
			return out[i].Manifest.ID < out[j].Manifest.ID
		}
		return compareSemVer(out[i].Manifest.Version, out[j].Manifest.Version) < 0
	})
	return out, nil
}

func Builtin(id string) (*Package, error) {
	all, err := Builtins()
	if err != nil {
		return nil, err
	}
	var newest *Package
	for _, p := range all {
		if p.Manifest.ID == id && (newest == nil || compareSemVer(p.Manifest.Version, newest.Manifest.Version) > 0) {
			newest = p
		}
	}
	if newest != nil {
		return newest, nil
	}
	return nil, fmt.Errorf("%w: built-in %s", ErrNotFound, id)
}

// LegacyClassicRef is the one package reference whose sessions deliberately
// remain STRIKE v1. Comparing the complete immutable reference prevents a
// future package that happens to reuse the display identity from silently
// taking the legacy font, sound, or export paths.
func LegacyClassicRef() (Ref, error) {
	legacyClassicOnce.Do(func() {
		p, err := Builtin(ClassicID)
		if err != nil {
			legacyClassicErr = err
			return
		}
		legacyClassicRef = p.Ref()
	})
	return legacyClassicRef, legacyClassicErr
}

func IsLegacyClassic(profile *Profile) bool {
	if profile == nil {
		return true
	}
	ref, err := LegacyClassicRef()
	return err == nil && profile.Ref == ref
}
