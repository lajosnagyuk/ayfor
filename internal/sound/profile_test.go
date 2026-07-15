package sound

import (
	"bytes"
	"testing"

	"github.com/lajosnagyuk/ayfor/internal/typewriter"
)

func TestClassicPackageSoundMatchesLegacyBank(t *testing.T) {
	pkg, err := typewriter.Builtin(typewriter.ClassicID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pkg.Profile()
	if err != nil {
		t.Fatal(err)
	}
	legacy := NewBank()
	packaged := NewBankWithProfile(p)
	for i := range 40 {
		ink := float64(i%13) / 10
		a := legacy.Pick(i%3, i%9, i, i%2, ink)
		b := packaged.Pick(i%3, i%9, i, i%2, ink)
		if !bytes.Equal(a, b) {
			t.Fatalf("packaged classic sound differs at strike %d", i)
		}
	}
}

func TestSplendidPackageSoundTuningIsApplied(t *testing.T) {
	splendidPkg, err := typewriter.Builtin(typewriter.OlympiaSplendidID)
	if err != nil {
		t.Fatal(err)
	}
	splendid, err := splendidPkg.Profile()
	if err != nil {
		t.Fatal(err)
	}
	sm3Pkg, _ := typewriter.Builtin(typewriter.OlympiaDemoID)
	sm3, _ := sm3Pkg.Profile()

	a := NewBankWithProfile(splendid).Pick(0, 2, 8, 0, 0.7)
	b := NewBankWithProfile(sm3).Pick(0, 2, 8, 0, 0.7)
	if bytes.Equal(a, b) {
		t.Fatal("Splendid sound tuning produced the same strike as the SM3")
	}
}
