package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"testing"
)

// fileHash returns the SHA-256 of the session file after flushing it.
func fileHash(t *testing.T, u *ui) [32]byte {
	t.Helper()
	if err := u.sess.Flush(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(u.sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// TestComfortAppendsNothing pins that building the chrome band never touches
// the strike file - it is display-only.
func TestComfortAppendsNothing(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()
	if _, err := u.sess.Strike('a'); err != nil {
		t.Fatal(err)
	}

	before := fileHash(t, u)
	u.comfortPageNo = true
	u.comfortWordCount = true
	for i := 0; i < 5; i++ {
		u.renderComfortBand(u.sess.Doc)
	}
	if after := fileHash(t, u); after != before {
		t.Fatal("comfort overlay changed the strike file")
	}
}

// TestComfortDoesNotTouchPageBitmaps pins that the overlay draws into its own
// image and never mutates the LRU-cached page bitmaps.
func TestComfortDoesNotTouchPageBitmaps(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()
	for _, r := range "hello world" {
		if _, err := u.sess.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	page0 := u.bitmap(u.sess.Doc.Current)
	snapshot := append([]uint8(nil), page0.Pix...)

	u.comfortPageNo = true
	u.comfortWordCount = true
	u.renderComfortBand(u.sess.Doc)

	if !bytes.Equal(u.bitmap(u.sess.Doc.Current).Pix, snapshot) {
		t.Fatal("comfort overlay mutated a cached page bitmap")
	}
	if u.comfortImg == nil {
		t.Fatal("comfort band was not rendered")
	}
	if bytes.Equal(u.comfortImg.Pix, make([]uint8, len(u.comfortImg.Pix))) {
		t.Fatal("comfort band is blank; nothing was stamped")
	}
}

// TestComfortOverlayDeterministic pins that recomputing the band from the same
// document state produces byte-identical pixels (no shimmer between recalcs).
func TestComfortOverlayDeterministic(t *testing.T) {
	u := buildTestUI(t)
	defer u.sess.Close()
	for _, r := range "the quick brown fox" {
		if _, err := u.sess.Strike(r); err != nil {
			t.Fatal(err)
		}
	}
	u.comfortPageNo = true
	u.comfortWordCount = true

	u.renderComfortBand(u.sess.Doc)
	first := append([]uint8(nil), u.comfortImg.Pix...)
	u.renderComfortBand(u.sess.Doc)
	if !bytes.Equal(u.comfortImg.Pix, first) {
		t.Fatal("comfort band is not deterministic across recalcs")
	}
}
