package session

import (
	"bytes"
	"errors"
	"testing"
)

type partialErrorWriter struct {
	written bytes.Buffer
	n       int
	err     error
}

type syncingWriter struct {
	bytes.Buffer
	syncs int
	err   error
}

func (w *syncingWriter) Sync() error {
	w.syncs++
	return w.err
}

func (w *partialErrorWriter) Write(p []byte) (int, error) {
	n := min(w.n, len(p))
	_, _ = w.written.Write(p[:n])
	return n, w.err
}

func TestBufWriterRetainsOnlyUnwrittenSuffixAfterPartialError(t *testing.T) {
	boom := errors.New("disk full")
	dst := &partialErrorWriter{n: 3, err: boom}
	b := &bufWriter{f: dst, buf: []byte("abcdef")}
	if err := b.Flush(); !errors.Is(err, boom) {
		t.Fatalf("Flush error = %v, want %v", err, boom)
	}
	if got := dst.written.String(); got != "abc" {
		t.Fatalf("written prefix = %q, want abc", got)
	}
	if got := string(b.buffered()); got != "def" {
		t.Fatalf("recoverable suffix = %q, want def", got)
	}
}

func TestBufWriterRejectsZeroProgress(t *testing.T) {
	dst := &partialErrorWriter{}
	b := &bufWriter{f: dst, buf: []byte("abc")}
	if err := b.Flush(); err == nil {
		t.Fatal("Flush accepted a zero-progress write")
	}
	if got := string(b.buffered()); got != "abc" {
		t.Fatalf("buffer = %q, want abc", got)
	}
}

func TestBufWriterFlushSyncsDurablyAndSticksSyncFailure(t *testing.T) {
	dst := &syncingWriter{}
	b := &bufWriter{f: dst, buf: []byte("durable")}
	if err := b.Flush(); err != nil {
		t.Fatal(err)
	}
	if dst.syncs != 1 {
		t.Fatalf("Sync calls = %d, want 1", dst.syncs)
	}

	boom := errors.New("sync failed")
	dst = &syncingWriter{err: boom}
	b = &bufWriter{f: dst, buf: []byte("written")}
	if err := b.Flush(); !errors.Is(err, boom) {
		t.Fatalf("Flush error = %v, want %v", err, boom)
	}
	if !b.hasError() {
		t.Fatal("sync failure was not sticky")
	}
}
