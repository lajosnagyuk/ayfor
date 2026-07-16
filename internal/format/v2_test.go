package format

import (
	"bytes"
	"strings"
	"testing"
)

func sampleV2Header() Header {
	return DefaultHeaderV2(0x0102030405060708, 1767225600000, TypewriterRef{
		ID: "io.ayfor.typewriters.test", Version: "1.2.3",
		Digest:   "sha256:" + strings.Repeat("a", 64),
		EngineID: "classic-impact", EngineVersion: 1,
	})
}

func TestV2HeaderAndEventsRoundTrip(t *testing.T) {
	h := sampleV2Header()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, h)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range allEventKinds() {
		if err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Check(); err != nil {
		t.Fatal(err)
	}
	f, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if f.Header.FormatVersion != Version2 || f.Header.Typewriter == nil || *f.Header.Typewriter != *h.Typewriter {
		t.Fatalf("v2 header lost identity: %+v", f.Header)
	}
	if f.Header.Seed != h.Seed || f.Header.CreatedUnixMS != h.CreatedUnixMS || len(f.Events) != len(allEventKinds())+1 {
		t.Fatalf("v2 round trip mismatch: %+v", f)
	}
	verified, err := Verify(f)
	if err != nil {
		t.Fatal(err)
	}
	if verified.FirstBad != -1 || verified.Checks == 0 {
		t.Fatalf("v2 verification = %+v", verified)
	}
}

func TestV2HeaderTruncationIsFatalNotRepairableTail(t *testing.T) {
	b, err := EncodeFileHeader(sampleV2Header())
	if err != nil {
		t.Fatal(err)
	}
	for cut := 0; cut < len(b); cut++ {
		f, err := Decode(b[:cut])
		if err == nil || f != nil {
			t.Fatalf("cut %d treated partial immutable header as event-tail truncation", cut)
		}
	}
}

func TestV2RejectsUnknownHeaderFieldsAndBadDigest(t *testing.T) {
	b, _ := EncodeFileHeader(sampleV2Header())
	payload := b[V2PreambleSize:]
	payload = bytes.Replace(payload, []byte(`"schema":1`), []byte(`"schema":1,"surprise":true`), 1)
	bad := append([]byte(nil), b[:V2PreambleSize]...)
	bad = append(bad, payload...)
	bad[6], bad[7] = byte(len(payload)), byte(len(payload)>>8)
	if _, err := Decode(bad); err == nil {
		t.Fatal("accepted unknown v2 header field")
	}

	h := sampleV2Header()
	h.Typewriter.Digest = "sha256:nope"
	if _, err := EncodeFileHeader(h); err == nil {
		t.Fatal("encoded invalid package digest")
	}
}

func TestV1EncodingRemainsFortyBytes(t *testing.T) {
	h := sampleHeader()
	b, err := EncodeFileHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != HeaderSize || !bytes.Equal(b, EncodeHeader(h)) {
		t.Fatal("v1 dispatcher changed legacy header bytes")
	}
}
