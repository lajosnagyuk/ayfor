// Package session owns a live writing session: the append-only file on
// disk, the folded document state, and the delta clock. The GUI calls
// intent methods (Strike, Return, Toss...); every intent is appended to
// the session buffer before it is applied, and the buffer is flushed to
// disk every flushInterval, on structural moments (close, rename,
// explicit Flush), and once right after a session starts (so a fresh
// file is never left headerless). A crash therefore loses at most the
// last flushInterval of typing - an owner-accepted trade (2026-07-03)
// against issuing one write syscall per keystroke; the format's
// truncation tolerance and crash repair handle a kill mid-flush exactly
// as they handled a kill mid-append.
package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/atomicfile"
	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/importer"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

// ErrLocked is returned when the .strike file is already held by another
// live writer (a second window, or the strike CLI). The append-only log has
// no way to merge two interleaved writers - their delta clocks and hash
// chains fork - so a second writer is refused rather than allowed to corrupt.
//
// lockFile (per-platform, lock_*.go) takes the non-blocking exclusive lock
// on the open handle. The lock is released when the fd is closed (Close, or
// process exit). A same-volume rename keeps the fd, so the lock follows the
// file; the copy-move path re-locks the new handle explicitly.
var ErrLocked = errors.New("strike: file is open in another window")

// ErrClosed is returned by any operation on a session that has lost its
// writer (after Close, or after a move that could not be recovered). One
// sentinel, so a caller can match it instead of three near-identical
// strings.
var ErrClosed = errors.New("strike: session is closed")

// ErrFinalClose means every buffered byte was checkpointed and flushed, but
// the operating system reported an error while releasing the file handle. The
// session cannot safely be reused because Close may have released the handle
// despite returning an error. This is deliberately distinct from a flush
// error, for which the live session remains recoverable with Rename.
var ErrFinalClose = errors.New("strike: final file close failed after a successful flush")

// ErrMoveCleanup means Rename committed the complete manuscript at its new
// Path, but could not durably retire the old pathname. Callers must treat the
// save as successful and may warn that a harmless orphan/duplicate remains.
var ErrMoveCleanup = errors.New("strike: manuscript moved but old-name cleanup was incomplete")

// defaultFlushInterval is how long typed events may sit in memory before
// they are written to the file. Per-session (a Session field, not
// package-mutable state) so a test shrinking its own session's interval
// cannot change every other session's behaviour.
const defaultFlushInterval = 5 * time.Second

// bufWriter accumulates encoded events in memory between flushes. It is
// the io.Writer the format.Writer writes into; only Flush touches the
// file. A flush error is sticky and resurfaces on the next Write, so a
// background flush failure (disk full, volume gone) reaches the writer
// on their next keystroke instead of vanishing into a goroutine.
type bufWriter struct {
	mu  sync.Mutex
	buf []byte
	f   io.Writer
	err error
}

func (b *bufWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return 0, b.err
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// Flush writes everything buffered so far to the file.
func (b *bufWriter) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	if len(b.buf) == 0 {
		return nil
	}
	n, err := b.f.Write(b.buf)
	if n < 0 || n > len(b.buf) {
		return fmt.Errorf("strike: invalid write count %d for %d buffered bytes", n, len(b.buf))
	}
	if n > 0 {
		copy(b.buf, b.buf[n:])
		b.buf = b.buf[:len(b.buf)-n]
	}
	if err != nil {
		b.err = err
		return err
	}
	if n == 0 && len(b.buf) > 0 {
		b.err = io.ErrShortWrite
		return b.err
	}
	if len(b.buf) > 0 {
		b.err = io.ErrShortWrite
		return b.err
	}
	if s, ok := b.f.(interface{ Sync() error }); ok {
		if err := s.Sync(); err != nil {
			b.err = err
			return err
		}
	}
	return nil
}

// hasError reports whether a flush error is currently sticky.
func (b *bufWriter) hasError() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err != nil
}

func (b *bufWriter) fail(err error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = err
	}
	return b.err
}

// buffered returns a copy of the bytes not yet flushed to disk. The copy-move
// recovery uses it to assemble the full image (on-disk prefix + tail) even
// when the buffer is in a sticky error state.
func (b *bufWriter) buffered() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	return out
}

// rebindFlushed points the buffer at a new handle whose file already holds
// everything buffered so far, clearing both the tail and any sticky error in
// one critical section so the tail cannot be written a second time.
func (b *bufWriter) rebindFlushed(f io.Writer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.f = f
	b.buf = b.buf[:0]
	b.err = nil
}

// Clock abstracts time for tests.
type Clock func() time.Time

// Session is a document being written.
type Session struct {
	Doc     *page.Doc
	Path    string
	Profile *typewriter.Profile // nil for implicit STRIKE v1 / Ayfor Classic

	file              *os.File
	bw                *bufWriter
	w                 *format.Writer
	clock             Clock
	last              time.Time
	stopFlush         chan struct{}
	flushDone         chan struct{}
	flushEvery        time.Duration // defaultFlushInterval unless a test shrinks it
	abortRemove       bool          // transaction cleanup for a newly created draft/import
	abortAt           int64         // transaction cleanup offset for an opened manuscript
	needsHumanSession bool          // imported stream starts human provenance lazily on first intent
}

// startFlusher begins the periodic background flush; stopped by Close.
// The goroutine works on captured locals, never Session fields - Close
// and Rename mutate those concurrently.
func (s *Session) startFlusher() {
	if s.flushEvery <= 0 {
		s.flushEvery = defaultFlushInterval
	}
	s.stopFlush = make(chan struct{})
	s.flushDone = make(chan struct{})
	stop, done, bw, interval := s.stopFlush, s.flushDone, s.bw, s.flushEvery
	path, file := s.Path, s.file
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				// A failure here is sticky in bw and resurfaces on the
				// next keystroke, where the GUI can show it.
				if err := ensurePathNamesFile(path, file); err != nil {
					bw.fail(fmt.Errorf("session: manuscript pathname changed: %w", err))
					continue
				}
				if err := bw.Flush(); err == nil {
					if err := ensurePathNamesFile(path, file); err != nil {
						bw.fail(fmt.Errorf("session: manuscript pathname changed during flush: %w", err))
					}
				}
			}
		}
	}()
}

// stopFlusher stops the background flush goroutine if running. Idempotent.
func (s *Session) stopFlusher() {
	if s.stopFlush != nil {
		close(s.stopFlush)
		<-s.flushDone
		s.stopFlush = nil
		s.flushDone = nil
	}
}

// Flush forces buffered events to disk now. Anything that reads the file
// back while the session is open (replay, external tooling) must call
// this first or it sees a file up to flushInterval stale.
func (s *Session) Flush() error {
	if s.w == nil || s.file == nil {
		return ErrClosed
	}
	if err := ensurePathNamesFile(s.Path, s.file); err != nil {
		return s.bw.fail(fmt.Errorf("session: manuscript pathname changed: %w", err))
	}
	if err := s.bw.Flush(); err != nil {
		return err
	}
	if err := ensurePathNamesFile(s.Path, s.file); err != nil {
		return s.bw.fail(fmt.Errorf("session: manuscript pathname changed during flush: %w", err))
	}
	return nil
}

// Snapshot returns the complete current STRIKE image from the locked file
// descriptor. It never reopens Path, so a pathname replacement cannot make
// replay read an attacker's file or lose access to recoverable orphaned data.
func (s *Session) Snapshot() ([]byte, error) {
	if err := s.Flush(); err != nil {
		return nil, err
	}
	info, err := s.file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 0 || info.Size() > format.MaxFileBytes {
		return nil, fmt.Errorf("session: manuscript exceeds %d-byte safety limit", format.MaxFileBytes)
	}
	b := make([]byte, int(info.Size()))
	if len(b) == 0 {
		return b, nil
	}
	n, err := s.file.ReadAt(b, 0)
	if err != nil && !(errors.Is(err, io.EOF) && n == len(b)) {
		return nil, err
	}
	if n != len(b) {
		return nil, io.ErrUnexpectedEOF
	}
	return b, nil
}

// New creates a fresh .strike file at path and opens a human session.
func New(path string, seed uint64, clock Clock) (*Session, error) {
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	h := format.DefaultHeader(seed, now.UnixMilli())
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		os.Remove(path) // we just created it; do not orphan it
		return nil, err
	}
	bw := &bufWriter{f: f}
	w, err := format.NewWriter(bw, h)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	s := &Session{
		Doc:         page.New(h),
		Path:        path,
		file:        f,
		bw:          bw,
		w:           w,
		clock:       clock,
		last:        now,
		abortRemove: true,
	}
	if _, err := s.append(format.Event{Op: format.OpSession, WallUnixMS: now.UnixMilli(), Origin: format.OriginHuman}); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if _, err := s.append(format.Event{Op: format.OpNewSheet}); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	// Flush the header and opening events immediately: a crash in the
	// first flushInterval must not leave a zero-byte, unopenable file.
	if err := s.bw.Flush(); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	s.startFlusher()
	return s, nil
}

// NewWithProfile creates a STRIKE v2 session bound to one exact package.
// Ayfor Classic continues to use New and v1 for backwards readability.
func NewWithProfile(path string, seed uint64, profile *typewriter.Profile, clock Clock) (*Session, error) {
	if profile == nil {
		return nil, errors.New("session: nil typewriter profile")
	}
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	pm := profile.Manifest
	h := format.DefaultHeaderV2(seed, now.UnixMilli(), format.TypewriterRef{
		ID: profile.Ref.ID, Version: profile.Ref.Version, Digest: profile.Ref.Digest,
		EngineID: pm.Engine.ID, EngineVersion: pm.Engine.Version,
	})
	h.Pitch = units.Pitch(pm.Geometry.PitchCPI)
	h.LineSpacing = units.LineSpacing(pm.Geometry.LineSpacing)
	h.Margins = units.Margins{
		Left:   float64(pm.Geometry.DefaultMarginsTenthMM[0]) / 10,
		Right:  float64(pm.Geometry.DefaultMarginsTenthMM[1]) / 10,
		Top:    float64(pm.Geometry.DefaultMarginsTenthMM[2]) / 10,
		Bottom: float64(pm.Geometry.DefaultMarginsTenthMM[3]) / 10,
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	cleanup := func() { f.Close(); os.Remove(path) }
	if err := lockFile(f); err != nil {
		cleanup()
		return nil, err
	}
	bw := &bufWriter{f: f}
	w, err := format.NewWriter(bw, h)
	if err != nil {
		cleanup()
		return nil, err
	}
	s := &Session{Doc: page.NewWithProfile(h, profile), Path: path, Profile: profile, file: f, bw: bw, w: w, clock: clock, last: now, abortRemove: true}
	if _, err := s.append(format.Event{Op: format.OpSession, WallUnixMS: now.UnixMilli(), Origin: format.OriginHuman}); err != nil {
		cleanup()
		return nil, err
	}
	if _, err := s.append(format.Event{Op: format.OpNewSheet}); err != nil {
		cleanup()
		return nil, err
	}
	if err := s.bw.Flush(); err != nil {
		cleanup()
		return nil, err
	}
	s.startFlusher()
	return s, nil
}

// backupCrashed writes the original bytes to a .crashed sidecar next to
// path, choosing a non-colliding name so a second repair cannot overwrite an
// earlier backup with already-damaged bytes. Written through atomicfile so
// a crash during the backup cannot leave a half-written backup that a later
// repair then trusts.
func backupCrashed(path string, b []byte) error {
	for i := 0; i < 10_000; i++ {
		name := path + ".crashed"
		if i > 0 {
			name = fmt.Sprintf("%s.crashed.%d", path, i)
		}
		err := atomicfile.CreateFile(name, b)
		if err == nil {
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return errors.New("session: could not allocate crash-backup name after 10000 attempts")
}

func ensurePathNamesFile(path string, f *os.File) error {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return errors.New("session: manuscript path must be a regular file, not a symlink")
	}
	fileInfo, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(pathInfo, fileInfo) {
		return errors.New("session: manuscript path changed while it was open; refusing replacement")
	}
	return nil
}

// replaceLocked atomically publishes clean while keeping an exclusive lock on
// both sides of the rename. Locking the temporary inode before publication is
// essential: a rename swaps in a new inode, so merely retaining the old file's
// lock would allow a second writer to open the repaired path immediately.
func replaceLocked(path string, old *os.File, clean []byte) (*os.File, error) {
	dir := filepath.Dir(path)
	tmp, err := createRepairTemp(dir)
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if info, err := old.Stat(); err == nil {
		_ = tmp.Chmod(info.Mode().Perm())
	}
	if _, err := tmp.Write(clean); err != nil {
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		return nil, err
	}
	if err := lockFile(tmp); err != nil {
		return nil, err
	}
	if err := publishRepair(path, tmpName, old, tmp); err != nil {
		return nil, err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	// tmp's offset is already at EOF and it stays open/locked as the live
	// writer. publishRepair handles the platform-specific ordering required to
	// retire the obsolete inode without exposing an unlocked published writer.
	ok = true
	return tmp, nil
}

// Open resumes an existing .strike file: replays it, then appends a new
// human session marker so the pause between sessions is not counted as
// one long keystroke delay.
func Open(path string, clock Clock) (*Session, error) {
	return open(path, clock, nil)
}

// OpenWithRegistry resolves STRIKE v2 package identity before any visual
// model is folded. V1 remains independent of the registry.
func OpenWithRegistry(path string, clock Clock, registry *typewriter.Registry) (*Session, error) {
	if registry == nil {
		return nil, errors.New("session: nil typewriter registry")
	}
	return open(path, clock, registry)
}

func open(path string, clock Clock, registry *typewriter.Registry) (*Session, error) {
	if clock == nil {
		clock = time.Now
	}
	// Lock the exact inode before reading it. Reading by pathname and locking
	// later creates a TOCTOU window where two processes can both validate and
	// repair/append the same manuscript.
	fh, err := openExistingSession(path)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = fh.Close()
		}
	}()
	if err := lockFile(fh); err != nil {
		return nil, err
	}
	if err := ensurePathNamesFile(path, fh); err != nil {
		return nil, err
	}
	info, err := fh.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 0 || info.Size() > format.MaxFileBytes {
		return nil, fmt.Errorf("session: manuscript exceeds %d-byte safety limit", format.MaxFileBytes)
	}
	if _, err := fh.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(fh, format.MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	f, derr := format.Decode(b)
	if f == nil {
		// The header itself is unreadable (bad magic/version/settings);
		// nothing can be recovered.
		return nil, derr
	}
	if err := page.VerifyModel(f.Header); err != nil {
		return nil, err
	}
	verified, err := format.Verify(f)
	if err != nil {
		return nil, err
	}
	if verified.FirstBad != -1 {
		return nil, fmt.Errorf("session: hash chain broken at event %d; refusing to repair or append", verified.FirstBad)
	}
	if derr != nil {
		// Only a short final write is safe to repair automatically. A hard
		// structural error may have valid content after the bad byte; silently
		// amputating it would turn corruption into user-visible data loss.
		return nil, fmt.Errorf("session: corrupt event stream; refusing automatic repair: %w", derr)
	}
	var profile *typewriter.Profile
	if f.Header.FormatVersion == format.Version2 {
		if registry == nil {
			return nil, fmt.Errorf("session: %w: v2 document requires package resolver", typewriter.ErrNotFound)
		}
		tr := f.Header.Typewriter
		profile, err = registry.Resolve(typewriter.Ref{ID: tr.ID, Version: tr.Version, Digest: tr.Digest})
		if err != nil {
			return nil, err
		}
		if profile.Manifest.Engine.ID != tr.EngineID || profile.Manifest.Engine.Version != tr.EngineVersion {
			return nil, fmt.Errorf("session: resolved package engine %s/%d does not match document %s/%d", profile.Manifest.Engine.ID, profile.Manifest.Engine.Version, tr.EngineID, tr.EngineVersion)
		}
		if err := page.VerifyProfile(f, profile); err != nil {
			return nil, err
		}
	}
	if f.Truncated {
		// Repair by rewriting only the intact prefix, keeping the original
		// bytes in a .crashed backup. Only a benign short final write reaches
		// this path; hard structural corruption was refused above.
		if err := ensurePathNamesFile(path, fh); err != nil {
			return nil, err
		}
		if err := backupCrashed(path, b); err != nil {
			return nil, err
		}
		clean := f.HeaderBytes()
		for _, e := range f.Events {
			var err error
			if clean, err = format.EncodeEvent(clean, e); err != nil {
				return nil, err
			}
		}
		newFH, err := replaceLocked(path, fh, clean)
		if err != nil {
			return nil, err
		}
		fh = newFH
		b = clean
	}
	bw := &bufWriter{f: fh}
	// b was decoded (and repaired if needed) above; skip the second full
	// decode a plain ResumeWriter would pay.
	dirty := len(f.Events) > 0 && f.Events[len(f.Events)-1].Op != format.OpCheck
	w := format.ResumeWriterValidated(bw, b, len(f.Events), dirty)
	now := clock()
	doc := page.Replay(f)
	if profile != nil {
		doc = page.ReplayWithProfile(f, profile)
	}
	s := &Session{
		Doc:     doc,
		Path:    path,
		Profile: profile,
		file:    fh,
		bw:      bw,
		w:       w,
		clock:   clock,
		last:    now,
		abortAt: int64(len(b)),
	}
	if _, err := s.append(format.Event{Op: format.OpSession, WallUnixMS: now.UnixMilli(), Origin: format.OriginHuman}); err != nil {
		fh.Close()
		return nil, err
	}
	if s.Doc.Current < 0 {
		if _, err := s.append(format.Event{Op: format.OpNewSheet}); err != nil {
			fh.Close()
			return nil, err
		}
	}
	if err := s.bw.Flush(); err != nil {
		return nil, err
	}
	s.startFlusher()
	keep = true
	return s, nil
}

// ImportText creates a new .strike file at path from plain text, typed by
// the machine at a uniform robotic cadence.
func ImportText(path, text string, seed uint64, clock Clock) (*Session, error) {
	return importText(path, text, seed, nil, clock)
}

// ImportTextWithProfile types text at robotic cadence into a document bound
// to the exact selected package. Classic deliberately remains STRIKE v1.
func ImportTextWithProfile(path, text string, seed uint64, profile *typewriter.Profile, clock Clock) (*Session, error) {
	if profile == nil || typewriter.IsLegacyClassic(profile) {
		return importText(path, text, seed, nil, clock)
	}
	return importText(path, text, seed, profile, clock)
}

func importText(path, text string, seed uint64, profile *typewriter.Profile, clock Clock) (*Session, error) {
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	h := format.DefaultHeader(seed, now.UnixMilli())
	if profile != nil {
		pm := profile.Manifest
		h = format.DefaultHeaderV2(seed, now.UnixMilli(), format.TypewriterRef{
			ID: profile.Ref.ID, Version: profile.Ref.Version, Digest: profile.Ref.Digest,
			EngineID: pm.Engine.ID, EngineVersion: pm.Engine.Version,
		})
		h.Pitch = units.Pitch(pm.Geometry.PitchCPI)
		h.LineSpacing = units.LineSpacing(pm.Geometry.LineSpacing)
		h.Margins = units.Margins{
			Left: float64(pm.Geometry.DefaultMarginsTenthMM[0]) / 10, Right: float64(pm.Geometry.DefaultMarginsTenthMM[1]) / 10,
			Top: float64(pm.Geometry.DefaultMarginsTenthMM[2]) / 10, Bottom: float64(pm.Geometry.DefaultMarginsTenthMM[3]) / 10,
		}
	}
	// Leave room for Writer's periodic and final CHECK records so the
	// resulting STRIKE file remains below the decoder's total event ceiling.
	const maxImportedEvents = format.MaxEvents - format.MaxEvents/format.CheckInterval - 16
	events, err := importer.ImportLimited(text, h, now.UnixMilli(), maxImportedEvents)
	if err != nil {
		return nil, err
	}

	// Build on a locked, destination-directory temporary. The final name is
	// claimed only after the complete imported stream is synced, so a crash can
	// leave at most a hidden temp—not an empty/truncated apparent manuscript.
	f, err := createRepairTemp(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	tmpName := f.Name()
	published := false
	cleanup := func() {
		_ = f.Close()
		if published {
			_ = os.Remove(path)
		} else {
			_ = os.Remove(tmpName)
		}
	}
	if err := f.Chmod(0o644); err != nil {
		cleanup()
		return nil, err
	}
	if err := lockFile(f); err != nil {
		cleanup()
		return nil, err
	}
	bw := &bufWriter{f: f}
	w, err := format.NewWriter(bw, h)
	if err != nil {
		cleanup()
		return nil, err
	}
	d := page.New(h)
	if profile != nil {
		d = page.NewWithProfile(h, profile)
	}
	for _, e := range events {
		if err := w.Append(e); err != nil {
			cleanup()
			return nil, err
		}
		d.Apply(e)
	}
	if err := w.Check(); err != nil {
		cleanup()
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		cleanup()
		return nil, err
	}
	if err := publishNoReplace(tmpName, path); err != nil {
		cleanup()
		return nil, err
	}
	published = true
	s := &Session{Doc: d, Path: path, Profile: profile, file: f, bw: bw, w: w, clock: clock, last: now, abortRemove: true, needsHumanSession: true}
	s.startFlusher()
	return s, nil
}

// append stamps the delta time, writes to disk, folds into the doc.
func (s *Session) append(e format.Event) (page.Result, error) {
	if s.w == nil {
		// The session has no writer (closed, or a move that could not be
		// recovered). Refuse rather than panic on s.w.Append.
		return page.Result{}, ErrClosed
	}
	now := s.clock()
	if s.needsHumanSession && e.Op != format.OpSession {
		human := format.Event{Op: format.OpSession, WallUnixMS: now.UnixMilli(), Origin: format.OriginHuman}
		if err := s.w.Append(human); err != nil {
			return page.Result{}, err
		}
		s.Doc.Apply(human)
		s.needsHumanSession = false
		s.last = now
	}
	delta := max(now.Sub(s.last), 0)
	s.last = now
	if e.Op != format.OpSession { // session markers carry wall time instead
		e.DeltaMS = uint64(delta.Milliseconds())
	}
	if err := s.w.Append(e); err != nil {
		return page.Result{}, err
	}
	return s.Doc.Apply(e), nil
}

// Intent methods, one per key on the machine.

func (s *Session) Strike(r rune) (page.Result, error) {
	return s.append(format.Event{Op: format.OpStrike, Rune: r})
}
func (s *Session) Space() (page.Result, error) {
	return s.append(format.Event{Op: format.OpSpace})
}
func (s *Session) Back() (page.Result, error) {
	return s.append(format.Event{Op: format.OpBack})
}
func (s *Session) Return() (page.Result, error) {
	return s.append(format.Event{Op: format.OpCR})
}
func (s *Session) LineFeed() (page.Result, error) {
	return s.append(format.Event{Op: format.OpLF})
}
func (s *Session) HalfUp() (page.Result, error) {
	return s.append(format.Event{Op: format.OpHalfUp})
}
func (s *Session) HalfDown() (page.Result, error) {
	return s.append(format.Event{Op: format.OpHalfDown})
}
func (s *Session) NewSheet() (page.Result, error) {
	return s.append(format.Event{Op: format.OpNewSheet})
}
func (s *Session) PagePrev() (page.Result, error) {
	return s.append(format.Event{Op: format.OpPagePrev})
}
func (s *Session) PageNext() (page.Result, error) {
	return s.append(format.Event{Op: format.OpPageNext})
}
func (s *Session) Toss() (page.Result, error) {
	return s.append(format.Event{Op: format.OpToss})
}
func (s *Session) NewRibbon() (page.Result, error) {
	return s.append(format.Event{Op: format.OpNewRibbon})
}
func (s *Session) SetTouch(v uint8) (page.Result, error) {
	return s.append(format.Event{Op: format.OpSetTouch, Value: v})
}
func (s *Session) SetDisposition(v uint8) (page.Result, error) {
	return s.append(format.Event{Op: format.OpSetDisposition, Value: v})
}
func (s *Session) SetSobriety(v uint8) (page.Result, error) {
	return s.append(format.Event{Op: format.OpSetSobriety, Value: v})
}
func (s *Session) SetCondition(v uint8) (page.Result, error) {
	return s.append(format.Event{Op: format.OpSetCondition, Value: v})
}
func (s *Session) SetPitch(v uint8) (page.Result, error) {
	if s.Profile != nil && int(v) != s.Profile.Manifest.Geometry.PitchCPI {
		return page.Result{}, fmt.Errorf("session: %s is a fixed %d cpi typewriter", s.Profile.Manifest.Name, s.Profile.Manifest.Geometry.PitchCPI)
	}
	return s.append(format.Event{Op: format.OpSetPitch, Value: v})
}
func (s *Session) SetLineSpacing(v uint8) (page.Result, error) {
	return s.append(format.Event{Op: format.OpSetLinespace, Value: v})
}
func (s *Session) SetMargins(m units.Margins) (page.Result, error) {
	return s.append(format.Event{Op: format.OpSetMargins, Margins: m})
}

// Rename gives the always-saved file its proper name. There is no
// "save" in ayfor - every keystroke is already on disk - so saving is
// only naming. It copies from the locked descriptor—not from the mutable old
// pathname—into an O_EXCL destination, swaps handles only after the complete
// destination is durable, then identity-checks old-name cleanup. This costs a
// copy but closes both overwrite and pathname-swap races on every platform.
func (s *Session) Rename(newPath string) error {
	if s.w == nil {
		return ErrClosed
	}
	if newPath == s.Path {
		return nil
	}
	return s.moveByCopy(newPath)
}

// moveByCopy moves the session to newPath by copying: build the complete
// new file FIRST, take its lock, and only then release the original. A
// failure at any step leaves the original session untouched and still
// live - there is no recovery path because nothing was torn down. This
// ordering also closes the window the old implementation had where the
// original's lock was dropped before the new handle was locked, letting
// a second window open (and fork) the file mid-move.
func (s *Session) moveByCopy(newPath string) error {
	// Pause the flusher for the whole move so no background Flush can race
	// the handle swap or write the tail to a stale fd. Rename runs on the
	// GUI goroutine, so appends cannot interleave either.
	s.stopFlusher()
	defer s.startFlusher()

	// Best-effort checkpoint + flush so the moved copy carries a verified
	// tail on the happy path. Neither is fatal: if the buffer is in a sticky
	// error state (a failed flush to the OLD volume) we assemble the full
	// image from the on-disk prefix plus the in-memory tail below, so
	// Save-As to a HEALTHY volume is a real recovery, not a dead end.
	_ = s.w.Check()
	_ = s.bw.Flush()

	info, err := s.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < 0 || info.Size() > format.MaxFileBytes {
		return fmt.Errorf("session: manuscript exceeds %d-byte move safety limit", format.MaxFileBytes)
	}
	prefix := make([]byte, int(info.Size()))
	if len(prefix) > 0 {
		n, readErr := s.file.ReadAt(prefix, 0)
		if readErr != nil && !(errors.Is(readErr, io.EOF) && n == len(prefix)) {
			return readErr
		}
		if n != len(prefix) {
			return io.ErrUnexpectedEOF
		}
	}
	// prefix (flushed) + buffered tail (nonempty only when the flush above
	// failed) is the complete document.
	image := append(prefix, s.bw.buffered()...)
	fh, err := createRepairTemp(filepath.Dir(newPath))
	if err != nil {
		return err
	}
	tmpName := fh.Name()
	published := false
	removeNew := func() {
		if published {
			_, _, _ = closeAndRemoveOwned(newPath, fh)
			return
		}
		_ = fh.Close()
		_ = os.Remove(tmpName)
	}
	if err := fh.Chmod(info.Mode().Perm()); err != nil {
		removeNew()
		return err
	}
	if err := lockFile(fh); err != nil {
		removeNew()
		return err
	}
	if n, err := fh.Write(image); err != nil || n != len(image) {
		removeNew()
		if err != nil {
			return err
		}
		return io.ErrShortWrite
	}
	if err := fh.Sync(); err != nil {
		removeNew()
		return err
	}
	if err := publishNoReplace(tmpName, newPath); err != nil {
		removeNew()
		return err
	}
	published = true
	w, err := format.ResumeWriter(s.bw, image)
	if err != nil {
		removeNew()
		return err
	}
	// The new file is complete, locked and resumable: commit the swap.
	// rebindFlushed clears the tail (it is persisted in newPath now) so
	// the flusher cannot write it a second time.
	s.bw.rebindFlushed(fh)
	old, oldPath := s.file, s.Path
	s.file = fh
	s.w = w
	s.Path = newPath
	// Cleanup is deliberately identity-checked. Once the complete, locked new
	// path is committed, an old-name cleanup failure may leave a harmless draft
	// behind, but must never delete a rival file swapped into that pathname.
	pathErr, removeErr, closeErr := closeAndRemoveOwned(oldPath, old)
	cleanupErr := errors.Join(pathErr, removeErr, closeErr)
	if removeErr == nil && pathErr == nil {
		cleanupErr = errors.Join(cleanupErr, syncRenameDir(filepath.Dir(oldPath)))
	}
	if cleanupErr != nil {
		return fmt.Errorf("%w: saved as %s; %v", ErrMoveCleanup, newPath, cleanupErr)
	}
	return nil
}

// Close writes a final checkpoint, flushes the buffer, and closes the file.
//
// If the final checkpoint or flush fails (a full disk), Close does NOT tear
// the session down: it restarts the flusher and returns the error with the
// session still live, so the caller can surface it and the user can recover
// by Save-As to a working volume (Rename rebuilds a fresh writer). Only a
// clean finalize closes the handle.
func (s *Session) Close() error {
	// Stop the flusher before the writer check: a session that lost its
	// writer (failed move, failed reopen) must still not leak the ticker
	// goroutine past Close. Idempotent, safe to call on any state.
	s.stopFlusher()
	if s.w == nil {
		// A partially torn-down session must not retain a Windows share lock or
		// leak its descriptor. Close is terminal in this state even though it
		// still reports ErrClosed to the caller.
		var closeErr error
		if s.file != nil {
			closeErr = s.file.Close()
			s.file = nil
		}
		return errors.Join(ErrClosed, closeErr)
	}
	if err := ensurePathNamesFile(s.Path, s.file); err != nil {
		s.startFlusher()
		return s.bw.fail(fmt.Errorf("session: manuscript pathname changed; use Save As to recover: %w", err))
	}
	if err := s.w.Check(); err != nil {
		s.startFlusher()
		return err
	}
	if err := s.bw.Flush(); err != nil {
		s.startFlusher()
		return err
	}
	if err := ensurePathNamesFile(s.Path, s.file); err != nil {
		s.startFlusher()
		return s.bw.fail(fmt.Errorf("session: manuscript pathname changed during close; use Save As to recover: %w", err))
	}
	if err := s.file.Close(); err != nil {
		s.w = nil
		s.file = nil
		return fmt.Errorf("%w: %v", ErrFinalClose, err)
	}
	s.w = nil
	s.file = nil
	return nil
}

// Abort rolls back a session prepared during a GUI document switch. New
// drafts/imports are removed; an existing manuscript is truncated to the
// exact verified length before Open appended its unused session marker. It
// deliberately emits no checkpoint and is not a general user-facing close.
func (s *Session) Abort() error {
	s.stopFlusher()
	if s.w == nil || s.file == nil {
		return ErrClosed
	}
	s.bw.mu.Lock()
	defer s.bw.mu.Unlock()
	if s.abortRemove {
		// Validate and unlink while the locked descriptor is still open, so
		// rollback cannot blindly delete a different file placed at the path.
		pathErr, removeErr, closeErr := closeAndRemoveOwned(s.Path, s.file)
		s.w = nil
		s.file = nil
		s.bw.buf = nil
		s.bw.err = nil
		return errors.Join(pathErr, removeErr, closeErr)
	}
	// Windows cannot truncate below its locked sentinel byte. Rebuild the
	// exact verified prefix under a replacement lock instead. This is also
	// stronger on Unix: the rollback is an atomic image replacement, never a
	// visible truncate followed by a rewrite or an unlocked writer gap.
	clean := make([]byte, int(s.abortAt))
	n, readErr := s.file.ReadAt(clean, 0)
	if errors.Is(readErr, io.EOF) && n == len(clean) {
		readErr = nil
	}
	if readErr == nil && n != len(clean) {
		readErr = io.ErrUnexpectedEOF
	}
	var replaceErr, closeErr error
	if readErr == nil {
		var replacement *os.File
		replacement, replaceErr = replaceLocked(s.Path, s.file, clean)
		if replaceErr == nil {
			s.file = replacement
			closeErr = replacement.Close()
		}
	}
	if readErr != nil || replaceErr != nil {
		// Abort is terminal even when rollback itself fails. Closing may report
		// an already-closed handle if Windows replacement failed after retiring
		// the old descriptor; preserve both errors for diagnosis.
		closeErr = errors.Join(closeErr, s.file.Close())
	}
	s.bw.buf = nil
	s.bw.err = nil
	s.w = nil
	s.file = nil
	return errors.Join(readErr, replaceErr, closeErr)
}
