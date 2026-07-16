package typewriter

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
)

const (
	manifestName       = "typewriter.json"
	lockName           = "TYPEWRITER.LOCK"
	signatureName      = "TYPEWRITER.SIG"
	maxArchiveBytes    = 32 << 20
	maxExpandedBytes   = 32 << 20
	maxFileBytes       = 8 << 20
	maxArchiveEntries  = 128
	maxCompressionRate = 200
)

type Lock struct {
	Schema int               `json:"schema"`
	Digest string            `json:"digest"`
	Files  map[string]string `json:"files"`
}

// Package is a validated package before its assets are materialized.
// Files are keyed relative to the package root and never include the lock or
// optional signature.
type Package struct {
	Manifest Manifest
	Digest   string
	Files    map[string][]byte
}

func (p *Package) Ref() Ref {
	return Ref{ID: p.Manifest.ID, Version: p.Manifest.Version, Digest: p.Digest}
}

func (p *Package) Profile() (*Profile, error) { return materialize(p) }

// LoadArchive validates an .aytw archive from one open descriptor, avoiding
// stat/open replacement races. No untrusted content is extracted.
func LoadArchive(filename string) (*Package, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArchiveBytes {
		return nil, fmt.Errorf("%w: archive size must be 1..%d bytes", ErrInvalidPackage, maxArchiveBytes)
	}
	if err := preflightZIP(f, info.Size()); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	return loadZipFiles(zr.File)
}

// LoadArchiveBytes is the in-memory equivalent used by tests and registries.
func LoadArchiveBytes(data []byte) (*Package, error) {
	if len(data) == 0 || len(data) > maxArchiveBytes {
		return nil, fmt.Errorf("%w: archive size must be 1..%d bytes", ErrInvalidPackage, maxArchiveBytes)
	}
	r := bytes.NewReader(data)
	if err := preflightZIP(r, int64(len(data))); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(r, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	return loadZipFiles(zr.File)
}

// preflightZIP validates the classic EOCD before archive/zip allocates one
// zip.File per central-directory entry. Schema 1 has tiny archives and does
// not need ZIP64, so rejecting it gives us a trustworthy early entry bound.
func preflightZIP(r io.ReaderAt, size int64) error {
	const eocdLen = 22
	if size < eocdLen {
		return fmt.Errorf("%w: ZIP end record is missing", ErrInvalidPackage)
	}
	n := min(size, int64(eocdLen+65535))
	tail := make([]byte, n)
	if _, err := r.ReadAt(tail, size-n); err != nil {
		return fmt.Errorf("%w: read ZIP end record: %v", ErrInvalidPackage, err)
	}
	for i := len(tail) - eocdLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:i+4]) != 0x06054b50 {
			continue
		}
		comment := int(binary.LittleEndian.Uint16(tail[i+20 : i+22]))
		if i+eocdLen+comment != len(tail) {
			continue
		}
		entriesDisk := binary.LittleEndian.Uint16(tail[i+8 : i+10])
		entries := binary.LittleEndian.Uint16(tail[i+10 : i+12])
		centralSize := binary.LittleEndian.Uint32(tail[i+12 : i+16])
		centralOff := binary.LittleEndian.Uint32(tail[i+16 : i+20])
		if entries == 0xffff || centralSize == 0xffffffff || centralOff == 0xffffffff {
			return fmt.Errorf("%w: ZIP64 archives are not supported", ErrInvalidPackage)
		}
		if entries == 0 || entries > maxArchiveEntries || entriesDisk != entries {
			return fmt.Errorf("%w: archive must contain 1..%d entries", ErrInvalidPackage, maxArchiveEntries)
		}
		eocdOffset := uint64(size - n + int64(i))
		if uint64(centralOff)+uint64(centralSize) > eocdOffset {
			return fmt.Errorf("%w: central directory is outside archive", ErrInvalidPackage)
		}
		// Do not trust the wrapping 16-bit EOCD count. archive/zip walks
		// headers until they stop and only compares modulo 65,536, so count
		// the bounded central-directory records ourselves before it allocates.
		central := make([]byte, int(centralSize))
		if _, err := r.ReadAt(central, int64(centralOff)); err != nil {
			return fmt.Errorf("%w: read central directory: %v", ErrInvalidPackage, err)
		}
		count := 0
		for pos := 0; pos < len(central); count++ {
			if count >= maxArchiveEntries || len(central)-pos < 46 || binary.LittleEndian.Uint32(central[pos:pos+4]) != 0x02014b50 {
				return fmt.Errorf("%w: central directory exceeds %d entries or is malformed", ErrInvalidPackage, maxArchiveEntries)
			}
			nameLen := int(binary.LittleEndian.Uint16(central[pos+28 : pos+30]))
			extraLen := int(binary.LittleEndian.Uint16(central[pos+30 : pos+32]))
			commentLen := int(binary.LittleEndian.Uint16(central[pos+32 : pos+34]))
			recordLen := 46 + nameLen + extraLen + commentLen
			if recordLen > len(central)-pos {
				return fmt.Errorf("%w: truncated central-directory record", ErrInvalidPackage)
			}
			pos += recordLen
		}
		if count != int(entries) {
			return fmt.Errorf("%w: central-directory count %d does not match EOCD %d", ErrInvalidPackage, count, entries)
		}
		return nil
	}
	return fmt.Errorf("%w: ZIP end record is missing", ErrInvalidPackage)
}

func caseFoldPath(s string) string { return cases.Fold().String(s) }

func loadZipFiles(entries []*zip.File) (*Package, error) {
	if len(entries) == 0 || len(entries) > maxArchiveEntries {
		return nil, fmt.Errorf("%w: archive must contain 1..%d entries", ErrInvalidPackage, maxArchiveEntries)
	}
	var root string
	files := make(map[string][]byte)
	caseNames := make(map[string]string)
	var lockBytes []byte
	var total uint64
	for _, zf := range entries {
		name := zf.Name
		if !utf8.ValidString(name) || strings.ContainsRune(name, '\\') || strings.ContainsRune(name, 0) || path.IsAbs(name) || path.Clean(name) != strings.TrimSuffix(name, "/") {
			return nil, fmt.Errorf("%w: unsafe archive path %q", ErrInvalidPackage, name)
		}
		parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
		if len(parts) < 1 || parts[0] == "" || parts[0] == "." || parts[0] == ".." {
			return nil, fmt.Errorf("%w: unsafe archive root %q", ErrInvalidPackage, name)
		}
		if root == "" {
			root = parts[0]
		} else if root != parts[0] {
			return nil, fmt.Errorf("%w: archive has multiple roots", ErrInvalidPackage)
		}
		var rel string
		if len(parts) >= 2 {
			rel = strings.Join(parts[1:], "/")
			if err := safeRelativeFile(rel); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
			}
		}
		modeType := zf.Mode() & fs.ModeType
		if zf.FileInfo().IsDir() {
			if modeType != fs.ModeDir {
				return nil, fmt.Errorf("%w: non-directory entry %q has a trailing slash", ErrInvalidPackage, name)
			}
			continue
		}
		if modeType != 0 {
			return nil, fmt.Errorf("%w: non-regular entry %q", ErrInvalidPackage, name)
		}
		if len(parts) < 2 {
			return nil, fmt.Errorf("%w: files must live below one root directory", ErrInvalidPackage)
		}
		fold := caseFoldPath(rel)
		if prior, ok := caseNames[fold]; ok {
			return nil, fmt.Errorf("%w: duplicate/case-colliding paths %q and %q", ErrInvalidPackage, prior, rel)
		}
		caseNames[fold] = rel
		if zf.UncompressedSize64 > maxFileBytes {
			return nil, fmt.Errorf("%w: %q exceeds %d bytes", ErrInvalidPackage, rel, maxFileBytes)
		}
		if zf.CompressedSize64 == 0 && zf.UncompressedSize64 > 0 || zf.CompressedSize64 > 0 && zf.CompressedSize64 < zf.UncompressedSize64 && zf.UncompressedSize64 > zf.CompressedSize64*maxCompressionRate {
			return nil, fmt.Errorf("%w: %q has suspicious compression ratio", ErrInvalidPackage, rel)
		}
		total += zf.UncompressedSize64
		if total > maxExpandedBytes {
			return nil, fmt.Errorf("%w: expanded archive exceeds %d bytes", ErrInvalidPackage, maxExpandedBytes)
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: open %q: %v", ErrInvalidPackage, rel, err)
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxFileBytes+1))
		closeErr := rc.Close()
		if err != nil || closeErr != nil || len(b) > maxFileBytes || uint64(len(b)) != zf.UncompressedSize64 {
			return nil, fmt.Errorf("%w: read %q failed or size changed", ErrInvalidPackage, rel)
		}
		switch rel {
		case lockName:
			lockBytes = b
		case signatureName:
			// Reserved. Signatures are not trusted until a trust-store design
			// exists, but including one does not change package content.
		default:
			files[rel] = b
		}
	}
	if lockBytes == nil {
		return nil, fmt.Errorf("%w: missing %s", ErrInvalidPackage, lockName)
	}
	return validateFiles(root, files, lockBytes)
}

func validateFiles(root string, files map[string][]byte, lockBytes []byte) (*Package, error) {
	manifestBytes, ok := files[manifestName]
	if !ok {
		return nil, fmt.Errorf("%w: missing %s", ErrInvalidPackage, manifestName)
	}
	m, err := decodeManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	if root != "" && root != m.ID {
		return nil, fmt.Errorf("%w: root %q does not match package id %q", ErrInvalidPackage, root, m.ID)
	}
	declared := make(map[string]bool)
	declared[manifestName] = true
	for _, p := range m.AssetPaths() {
		declared[p] = true
	}
	for p := range files {
		if !declared[p] {
			return nil, fmt.Errorf("%w: unlisted file %q", ErrInvalidPackage, p)
		}
	}
	for p := range declared {
		if _, ok := files[p]; !ok {
			return nil, fmt.Errorf("%w: missing manifest asset %q", ErrInvalidPackage, p)
		}
	}
	digest, hashes := contentDigest(files)
	lock, err := decodeLock(lockBytes)
	if err != nil {
		return nil, err
	}
	if lock.Digest != digest {
		return nil, fmt.Errorf("%w: lock digest is %q, computed %q", ErrInvalidPackage, lock.Digest, digest)
	}
	if len(lock.Files) != len(hashes) {
		return nil, fmt.Errorf("%w: lock file list does not match archive", ErrInvalidPackage)
	}
	for p, h := range hashes {
		if lock.Files[p] != h {
			return nil, fmt.Errorf("%w: hash mismatch for %q", ErrInvalidPackage, p)
		}
	}
	return &Package{Manifest: m, Digest: digest, Files: cloneFiles(files)}, nil
}

func decodeLock(data []byte) (Lock, error) {
	var l Lock
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return l, fmt.Errorf("%w: %s: %v", ErrInvalidPackage, lockName, err)
	}
	if _, err := requireExactObjectKeys(data, "schema", "digest", "files"); err != nil {
		return l, fmt.Errorf("%w: %s: %v", ErrInvalidPackage, lockName, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return l, fmt.Errorf("%w: %s: %v", ErrInvalidPackage, lockName, err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return l, fmt.Errorf("%w: %s: %v", ErrInvalidPackage, lockName, err)
	}
	if l.Schema != 1 || len(l.Files) == 0 || !validDigest(l.Digest) {
		return l, fmt.Errorf("%w: malformed %s", ErrInvalidPackage, lockName)
	}
	for p, h := range l.Files {
		if err := safeRelativeFile(p); err != nil || !validDigest(h) {
			return l, fmt.Errorf("%w: malformed lock entry %q", ErrInvalidPackage, p)
		}
	}
	return l, nil
}

func validDigest(s string) bool {
	if !strings.HasPrefix(s, "sha256:") || len(s) != len("sha256:")+sha256.Size*2 {
		return false
	}
	hexPart := strings.TrimPrefix(s, "sha256:")
	if hexPart != strings.ToLower(hexPart) {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

func cloneFiles(in map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = bytes.Clone(v)
	}
	return out
}

func contentDigest(files map[string][]byte) (string, map[string]string) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	hashes := make(map[string]string, len(files))
	var size [8]byte
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		fileHash := "sha256:" + hex.EncodeToString(sum[:])
		hashes[name] = fileHash
		h.Write([]byte(name))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(size[:], uint64(len(files[name])))
		h.Write(size[:])
		h.Write(sum[:])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), hashes
}

// LoadSourceFS loads a trusted package source directory. It performs the
// same manifest and asset validation as an archive and creates the same
// digest, but a source tree has no generated lock yet.
func LoadSourceFS(fsys fs.FS, root string) (*Package, error) {
	return loadSourceFS(fsys, root, path.Base(root))
}

// LoadSourceRootFS loads an already-open descriptor-rooted authoring tree.
// expectedID is its external directory basename; inside fsys the root is ".".
func LoadSourceRootFS(fsys fs.FS, expectedID string) (*Package, error) {
	if err := safeRelativeFile(expectedID); err != nil || strings.Contains(expectedID, "/") {
		return nil, fmt.Errorf("%w: invalid source id %q", ErrInvalidPackage, expectedID)
	}
	return loadSourceFS(fsys, ".", expectedID)
}

func loadSourceFS(fsys fs.FS, root, expectedID string) (*Package, error) {
	if root != "." {
		if err := safeRelativeFile(root); err != nil {
			return nil, err
		}
	}
	files := make(map[string][]byte)
	caseNames := make(map[string]string)
	entries := 0
	total := 0
	err := fs.WalkDir(fsys, root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == root {
			return nil
		}
		rel := strings.TrimPrefix(name, root+"/")
		entries++
		if entries > maxArchiveEntries {
			return fmt.Errorf("source has more than %d entries", maxArchiveEntries)
		}
		if err := safeRelativeFile(rel); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		before, err := fs.Lstat(fsys, name)
		if err != nil {
			return err
		}
		if before.Mode()&fs.ModeType != 0 {
			return fmt.Errorf("non-regular source file %q", rel)
		}
		fold := caseFoldPath(rel)
		if prior, exists := caseNames[fold]; exists {
			return fmt.Errorf("case-colliding source paths %q and %q", prior, rel)
		}
		caseNames[fold] = rel
		if rel == lockName || rel == signatureName {
			return nil
		}
		f, err := fsys.Open(name)
		if err != nil {
			return err
		}
		opened, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		b, readErr := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
		closeErr := f.Close()
		after, afterErr := fs.Lstat(fsys, name)
		if readErr != nil || closeErr != nil || afterErr != nil {
			return fmt.Errorf("source file %q changed or could not be read", rel)
		}
		if after.Mode()&fs.ModeType != 0 || !sameSourceSnapshot(before, opened) || !sameSourceSnapshot(opened, after) {
			return fmt.Errorf("source file %q changed while packing", rel)
		}
		if len(b) > maxFileBytes {
			return fmt.Errorf("source file %q exceeds limit", rel)
		}
		total += len(b)
		if total > maxExpandedBytes {
			return fmt.Errorf("source exceeds %d bytes", maxExpandedBytes)
		}
		// Re-open and re-read once more. Same-inode writers do not change
		// os.SameFile, so metadata plus byte equality closes the common in-place
		// replacement race before a distributable package is published.
		second, err := fsys.Open(name)
		if err != nil {
			return fmt.Errorf("source file %q changed while packing", rel)
		}
		secondInfo, statErr := second.Stat()
		b2, secondReadErr := io.ReadAll(io.LimitReader(second, maxFileBytes+1))
		secondCloseErr := second.Close()
		finalInfo, finalErr := fs.Lstat(fsys, name)
		if statErr != nil || secondReadErr != nil || secondCloseErr != nil || finalErr != nil ||
			!sameSourceSnapshot(after, secondInfo) || !sameSourceSnapshot(secondInfo, finalInfo) || !bytes.Equal(b, b2) {
			return fmt.Errorf("source file %q changed while packing", rel)
		}
		files[rel] = b2
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPackage, err)
	}
	m, err := decodeManifest(files[manifestName])
	if err != nil {
		return nil, err
	}
	if expectedID != m.ID {
		return nil, fmt.Errorf("%w: source root %q does not match id %q", ErrInvalidPackage, expectedID, m.ID)
	}
	digest, hashes := contentDigest(files)
	lockBytes, err := json.Marshal(Lock{Schema: 1, Digest: digest, Files: hashes})
	if err != nil {
		return nil, err
	}
	return validateFiles(m.ID, files, lockBytes)
}

func sameSourceSnapshot(a, b fs.FileInfo) bool {
	return a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && os.SameFile(a, b)
}

// Archive returns a deterministic .aytw image. The source package remains
// immutable; TYPEWRITER.LOCK is generated into the archive only.
func (p *Package) Archive() ([]byte, error) {
	digest, hashes := contentDigest(p.Files)
	if digest != p.Digest {
		return nil, errors.New("typewriter: package files mutated after validation")
	}
	lockBytes, err := json.Marshal(Lock{Schema: 1, Digest: digest, Files: hashes})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(p.Files)+1)
	for name := range p.Files {
		names = append(names, name)
	}
	names = append(names, lockName)
	sort.Strings(names)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	fixed := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range names {
		data := p.Files[name]
		if name == lockName {
			data = lockBytes
		}
		h := &zip.FileHeader{Name: p.Manifest.ID + "/" + name, Method: zip.Deflate}
		h.Modified = fixed
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if out.Len() > maxArchiveBytes {
		return nil, fmt.Errorf("typewriter: packed archive exceeds %d bytes", maxArchiveBytes)
	}
	return out.Bytes(), nil
}
