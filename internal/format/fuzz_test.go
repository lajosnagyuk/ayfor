package format

import (
	"bytes"
	"testing"
)

// FuzzDecode hammers the parser with hostile input: .strike files arrive
// from strangers (that is the point of an open format), so Decode must
// never panic or hang, and everything it accepts must survive
// re-encoding and hash verification.
func FuzzDecode(f *testing.F) {
	// Seed with a small valid file and interesting corruptions of it.
	valid := EncodeHeader(sampleHeader())
	for _, e := range allEventKinds() {
		valid, _ = EncodeEvent(valid, e)
	}
	f.Add(valid)
	f.Add(valid[:len(valid)-3]) // truncated mid-event
	f.Add(valid[:HeaderSize])   // header only
	f.Add([]byte("STRK"))       // magic only
	f.Add([]byte{})
	// Overlong varint / absurd rune material.
	f.Add(append(bytes.Clone(valid), 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F, 0x01))

	f.Fuzz(func(t *testing.T, b []byte) {
		decoded, err := Decode(b)
		if err != nil {
			return // rejected is fine; panicking is not
		}
		// Whatever was accepted must round-trip: re-encode every event
		// and re-verify the hash chain without a crash.
		if _, err := Verify(decoded); err != nil {
			t.Fatalf("accepted file failed to re-encode for verification: %v", err)
		}
		enc := EncodeHeader(decoded.Header)
		for i, e := range decoded.Events {
			enc, err = EncodeEvent(enc, e)
			if err != nil {
				t.Fatalf("event %d decoded but will not re-encode: %v", i, err)
			}
		}
		// Byte-identity with the input is NOT guaranteed (the input may
		// use overlong varints or nonzero reserved header bytes, neither
		// of which a conforming writer produces - Verify correctly flags
		// such files as failing the hash chain). What IS guaranteed is
		// idempotence: our own encoding must decode back to the same
		// header and events, with no truncation.
		again, err := Decode(enc)
		if err != nil {
			t.Fatalf("re-encoded output does not decode: %v", err)
		}
		if again.Truncated {
			t.Fatal("re-encoded output decodes as truncated")
		}
		if again.Header != decoded.Header {
			t.Fatalf("header changed across round-trip:\n first %+v\nsecond %+v", decoded.Header, again.Header)
		}
		if len(again.Events) != len(decoded.Events) {
			t.Fatalf("event count changed across round-trip: %d -> %d", len(decoded.Events), len(again.Events))
		}
		for i := range again.Events {
			if again.Events[i] != decoded.Events[i] {
				t.Fatalf("event %d changed across round-trip:\n first %+v\nsecond %+v", i, decoded.Events[i], again.Events[i])
			}
		}
	})
}
