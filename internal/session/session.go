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
	"os"
	"sync"
	"time"

	"github.com/lajosnagyuk/ayfor/internal/atomicfile"
	"github.com/lajosnagyuk/ayfor/internal/format"
	"github.com/lajosnagyuk/ayfor/internal/importer"
	"github.com/lajosnagyuk/ayfor/internal/page"
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
	f   *os.File
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
	if _, err := b.f.Write(b.buf); err != nil {
		b.err = err
		return err
	}
	b.buf = b.buf[:0]
	return nil
}

// hasError reports whether a flush error is currently sticky.
func (b *bufWriter) hasError() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err != nil
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
func (b *bufWriter) rebindFlushed(f *os.File) {
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
	Doc  *page.Doc
	Path string

	file       *os.File
	bw         *bufWriter
	w          *format.Writer
	clock      Clock
	last       time.Time
	stopFlush  chan struct{}
	flushEvery time.Duration // defaultFlushInterval unless a test shrinks it
}

// startFlusher begins the periodic background flush; stopped by Close.
// The goroutine works on captured locals, never Session fields - Close
// and Rename mutate those concurrently.
func (s *Session) startFlusher() {
	if s.flushEvery <= 0 {
		s.flushEvery = defaultFlushInterval
	}
	s.stopFlush = make(chan struct{})
	stop, bw, interval := s.stopFlush, s.bw, s.flushEvery
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				// A failure here is sticky in bw and resurfaces on the
				// next keystroke, where the GUI can show it.
				bw.Flush()
			}
		}
	}()
}

// stopFlusher stops the background flush goroutine if running. Idempotent.
func (s *Session) stopFlusher() {
	if s.stopFlush != nil {
		close(s.stopFlush)
		s.stopFlush = nil
	}
}

// Flush forces buffered events to disk now. Anything that reads the file
// back while the session is open (replay, external tooling) must call
// this first or it sees a file up to flushInterval stale.
func (s *Session) Flush() error {
	return s.bw.Flush()
}

// New creates a fresh .strike file at path and opens a human session.
func New(path string, seed uint64, clock Clock) (*Session, error) {
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	h := format.DefaultHeader(seed, now.UnixMilli())
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o644)
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
		Doc:   page.New(h),
		Path:  path,
		file:  f,
		bw:    bw,
		w:     w,
		clock: clock,
		last:  now,
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

// backupCrashed writes the original bytes to a .crashed sidecar next to
// path, choosing a non-colliding name so a second repair cannot overwrite an
// earlier backup with already-damaged bytes. Written through atomicfile so
// a crash during the backup cannot leave a half-written backup that a later
// repair then trusts.
func backupCrashed(path string, b []byte) error {
	name := path + ".crashed"
	for i := 1; ; i++ {
		if _, err := os.Stat(name); os.IsNotExist(err) {
			break
		}
		name = fmt.Sprintf("%s.crashed.%d", path, i)
	}
	return atomicfile.WriteFile(name, b)
}

// Open resumes an existing .strike file: replays it, then appends a new
// human session marker so the pause between sessions is not counted as
// one long keystroke delay.
func Open(path string, clock Clock) (*Session, error) {
	if clock == nil {
		clock = time.Now
	}
	b, err := os.ReadFile(path)
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
	if f.Truncated || derr != nil {
		// Repair by rewriting only the intact prefix, keeping the original
		// bytes in a .crashed backup. Truncation is a benign crash tail; a
		// hard decode error (derr != nil) is corruption whose bytes after the
		// bad point are discarded from the working file but preserved in the
		// backup, so a damaged file opens to its readable prefix instead of
		// being wholly lost.
		if err := backupCrashed(path, b); err != nil {
			return nil, err
		}
		clean := format.EncodeHeader(f.Header)
		for _, e := range f.Events {
			var err error
			if clean, err = format.EncodeEvent(clean, e); err != nil {
				return nil, err
			}
		}
		if err := atomicfile.WriteFile(path, clean); err != nil {
			return nil, err
		}
		b = clean
	}
	fh, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(fh); err != nil {
		fh.Close()
		return nil, err
	}
	bw := &bufWriter{f: fh}
	// b was decoded (and repaired if needed) above; skip the second full
	// decode a plain ResumeWriter would pay.
	w := format.ResumeWriterValidated(bw, b)
	now := clock()
	s := &Session{
		Doc:   page.Replay(f),
		Path:  path,
		file:  fh,
		bw:    bw,
		w:     w,
		clock: clock,
		last:  now,
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
		fh.Close()
		return nil, err
	}
	s.startFlusher()
	return s, nil
}

// ImportText creates a new .strike file at path from plain text, typed by
// the machine at a uniform robotic cadence.
func ImportText(path, text string, seed uint64, clock Clock) (*Session, error) {
	if clock == nil {
		clock = time.Now
	}
	now := clock()
	h := format.DefaultHeader(seed, now.UnixMilli())
	events := importer.Import(text, h, now.UnixMilli())

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	bw := &bufWriter{f: f}
	w, err := format.NewWriter(bw, h)
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	d := page.New(h)
	for _, e := range events {
		if err := w.Append(e); err != nil {
			f.Close()
			os.Remove(path)
			return nil, err
		}
		d.Apply(e)
	}
	if err := w.Check(); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	s := &Session{Doc: d, Path: path, file: f, bw: bw, w: w, clock: clock, last: now}
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
// only naming. Same-volume renames keep the open file handle valid (the
// fd follows the inode); across volumes we copy to the new path, swap
// handles, and remove the original. Refuses to overwrite an existing
// file (best-effort: the Stat-then-rename pair is not atomic, so a file
// created in the gap by another process can still be replaced - both
// callers pick the target through a save dialog on the GUI thread, so
// the race needs an outside writer aiming at the same fresh path).
func (s *Session) Rename(newPath string) error {
	if s.w == nil {
		return ErrClosed
	}
	if newPath == s.Path {
		return nil
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("%s already exists", newPath)
	}
	// A same-volume os.Rename keeps the current file handle - but if that
	// handle's buffer is in a sticky error state, keeping it would carry the
	// error (and strand the buffered tail) into the renamed file. Force the
	// copy path, which rebuilds a fresh writer over the full image and clears
	// the error, so Save-As is a real recovery.
	if !s.bw.hasError() {
		if err := os.Rename(s.Path, newPath); err == nil {
			s.Path = newPath
			return nil
		}
	}
	// Cross-volume (or other rename failure, or a buffer in error): copy then
	// reopen.
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

	prefix, err := os.ReadFile(s.Path)
	if err != nil {
		return err
	}
	// prefix (flushed) + buffered tail (nonempty only when the flush above
	// failed) is the complete document.
	image := append(prefix, s.bw.buffered()...)
	if err := os.WriteFile(newPath, image, 0o644); err != nil {
		os.Remove(newPath)
		return err
	}
	fh, err := os.OpenFile(newPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		os.Remove(newPath)
		return err
	}
	if err := lockFile(fh); err != nil {
		fh.Close()
		os.Remove(newPath)
		return err
	}
	w, err := format.ResumeWriter(s.bw, image)
	if err != nil {
		fh.Close()
		os.Remove(newPath)
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
	old.Close()
	os.Remove(oldPath)
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
		return ErrClosed
	}
	if err := s.w.Check(); err != nil {
		s.startFlusher()
		return err
	}
	if err := s.bw.Flush(); err != nil {
		s.startFlusher()
		return err
	}
	cerr := s.file.Close()
	s.w = nil
	return cerr
}
