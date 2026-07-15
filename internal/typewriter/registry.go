package typewriter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lajosnagyuk/ayfor/internal/atomicfile"
)

// Installed describes a package without exposing mutable package internals.
type Installed struct {
	Ref     Ref
	Name    string
	Builtin bool
	Path    string
}

// Registry resolves immutable built-in and user-installed packages.
type Registry struct {
	Dir      string
	builtins map[string]*Package
}

func NewRegistry(dir string, builtins ...*Package) (*Registry, error) {
	r := &Registry{Dir: dir, builtins: make(map[string]*Package)}
	versions := make(map[string]string)
	for _, p := range builtins {
		if p == nil {
			return nil, errors.New("typewriter: nil builtin")
		}
		key := refKey(p.Ref())
		if _, exists := r.builtins[key]; exists {
			return nil, fmt.Errorf("%w: duplicate builtin %s", ErrConflict, p.Ref())
		}
		if digest, exists := versions[versionKey(p.Ref())]; exists && digest != p.Digest {
			return nil, fmt.Errorf("%w: built-in %s@%s has digests %s and %s", ErrConflict, p.Manifest.ID, p.Manifest.Version, digest, p.Digest)
		}
		versions[versionKey(p.Ref())] = p.Digest
		r.builtins[key] = p
	}
	return r, nil
}

func DefaultRegistry() (*Registry, error) {
	builtins, err := Builtins()
	if err != nil {
		return nil, err
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return NewRegistry(filepath.Join(base, "ayfor", "typewriters"), builtins...)
}

func refKey(ref Ref) string { return ref.ID + "\x00" + ref.Version + "\x00" + ref.Digest }

func versionKey(ref Ref) string { return ref.ID + "\x00" + ref.Version }

const claimBytes int64 = 72 // "sha256:" + 64 lowercase hex digits + newline

func (r *Registry) archivePath(ref Ref) (string, error) {
	if !idPattern.MatchString(ref.ID) || hasWindowsReservedIDComponent(ref.ID) || ValidateVersion(ref.Version) != nil || !validDigest(ref.Digest) {
		return "", fmt.Errorf("%w: invalid package reference", ErrNotFound)
	}
	return filepath.Join(r.Dir, ref.ID, ref.Version, strings.TrimPrefix(ref.Digest, "sha256:")+".aytw"), nil
}

func (r *Registry) List() ([]Installed, error) {
	list := make([]Installed, 0, len(r.builtins))
	versions := make(map[string]string)
	for _, p := range r.builtins {
		if prior, ok := versions[versionKey(p.Ref())]; ok && prior != p.Digest {
			return nil, fmt.Errorf("%w: %s has digests %s and %s", ErrConflict, versionKey(p.Ref()), prior, p.Digest)
		}
		list = append(list, Installed{Ref: p.Ref(), Name: p.Manifest.Name, Builtin: true})
		versions[versionKey(p.Ref())] = p.Digest
	}
	if r.Dir != "" {
		err := filepath.WalkDir(r.Dir, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) && name == r.Dir {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&fs.ModeType != 0 || strings.ToLower(filepath.Ext(name)) != ".aytw" {
				return nil
			}
			p, err := LoadArchive(name)
			if err != nil {
				// A damaged or manually copied artifact must not make every
				// unrelated package unavailable. Exact resolution below remains
				// fail-closed for the requested reference.
				return nil
			}
			canonical, err := r.archivePath(p.Ref())
			if err != nil || filepath.Clean(name) != filepath.Clean(canonical) {
				return nil
			}
			if prior, ok := versions[versionKey(p.Ref())]; ok && prior != p.Digest {
				return fmt.Errorf("%w: %s has digests %s and %s", ErrConflict, versionKey(p.Ref()), prior, p.Digest)
			}
			versions[versionKey(p.Ref())] = p.Digest
			list = append(list, Installed{Ref: p.Ref(), Name: p.Manifest.Name, Path: name})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		if list[i].Ref.Version != list[j].Ref.Version {
			return compareSemVer(list[i].Ref.Version, list[j].Ref.Version) < 0
		}
		return list[i].Ref.Digest < list[j].Ref.Digest
	})
	return list, nil
}

func (r *Registry) Resolve(ref Ref) (*Profile, error) {
	if p := r.builtins[refKey(ref)]; p != nil {
		return p.Profile()
	}
	if r.Dir == "" {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	target, err := r.archivePath(ref)
	if err != nil {
		return nil, err
	}
	p, err := LoadArchive(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	if err != nil {
		return nil, fmt.Errorf("installed package %s: %w", target, err)
	}
	if p.Ref() != ref {
		return nil, fmt.Errorf("%w: resolved %s from path for %s", ErrConflict, p.Ref(), ref)
	}
	return p.Profile()
}

func (r *Registry) Install(filename string) (Installed, error) {
	var zero Installed
	p, err := LoadArchive(filename)
	if err != nil {
		return zero, err
	}
	// Installation is a transaction boundary: validate every font, sample,
	// calibration and required text before publishing anything to the registry.
	if _, err := p.Profile(); err != nil {
		return zero, err
	}
	for _, builtin := range r.builtins {
		if versionKey(builtin.Ref()) != versionKey(p.Ref()) {
			continue
		}
		if builtin.Digest != p.Digest {
			return zero, fmt.Errorf("%w: %s@%s is reserved by built-in digest %s", ErrConflict, p.Manifest.ID, p.Manifest.Version, builtin.Digest)
		}
		return Installed{Ref: builtin.Ref(), Name: builtin.Manifest.Name, Builtin: true}, nil
	}
	if r.Dir == "" {
		return zero, errors.New("typewriter: registry has no installation directory")
	}
	data, err := p.Archive()
	if err != nil {
		return zero, err
	}
	target, err := r.archivePath(p.Ref())
	if err != nil {
		return zero, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return zero, err
	}
	// Atomically pin ID+version to one digest across processes. A hard link
	// publishes a fully written, fsynced claim in a single filesystem step;
	// a crash can leave a retryable claim but never a torn or rebound one.
	claim := filepath.Join(filepath.Dir(target), ".ref")
	claimData := []byte(p.Digest + "\n")
	tmp, err := os.CreateTemp(filepath.Dir(target), ".ref-*.tmp")
	if err != nil {
		return zero, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(claimData); err != nil {
		tmp.Close()
		return zero, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return zero, err
	}
	if err := tmp.Close(); err != nil {
		return zero, err
	}
	if err := publishRegistryClaim(tmpName, claim); err != nil {
		return zero, err
	}
	claimInfo, err := os.Lstat(claim)
	if err != nil {
		return zero, err
	}
	if !claimInfo.Mode().IsRegular() || claimInfo.Size() != claimBytes {
		return zero, fmt.Errorf("%w: %s@%s has a malformed registry claim", ErrConflict, p.Manifest.ID, p.Manifest.Version)
	}
	claimFile, err := os.Open(claim)
	if err != nil {
		return zero, err
	}
	pinned := make([]byte, int(claimBytes))
	_, readErr := io.ReadFull(claimFile, pinned)
	closeErr := claimFile.Close()
	if readErr != nil || closeErr != nil || !validDigest(strings.TrimSuffix(string(pinned), "\n")) {
		return zero, fmt.Errorf("%w: %s@%s has a malformed registry claim", ErrConflict, p.Manifest.ID, p.Manifest.Version)
	}
	if !bytes.Equal(pinned, claimData) {
		return zero, fmt.Errorf("%w: %s@%s is pinned to a different digest", ErrConflict, p.Manifest.ID, p.Manifest.Version)
	}
	if existing, err := LoadArchive(target); err == nil {
		if existing.Ref() != p.Ref() {
			return zero, fmt.Errorf("%w: canonical path contains %s", ErrConflict, existing.Ref())
		}
		return Installed{Ref: p.Ref(), Name: p.Manifest.Name, Path: target}, nil
	}
	// Missing, truncated, or bit-rotted canonical content is repairable: the
	// immutable claim matches this fully validated input, so atomically
	// republish the canonical bytes. A write/permission failure still fails.
	if err := atomicfile.WriteFile(target, data); err != nil {
		return zero, err
	}
	return Installed{Ref: p.Ref(), Name: p.Manifest.Name, Path: target}, nil
}

func (r *Registry) Remove(ref Ref) error {
	if r.builtins[refKey(ref)] != nil {
		return errors.New("typewriter: built-in packages cannot be removed")
	}
	if r.Dir == "" {
		return fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	target, err := r.archivePath(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(target); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, ref)
	} else if err != nil {
		return err
	}
	// Keep .ref: immutable ID/version identity survives uninstall/reinstall.
	return nil
}
