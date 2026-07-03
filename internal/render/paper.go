package render

// paper grain: lightweight 50 gsm typewriter stock is flat but never
// featureless - fine fibre tooth plus a soft mottle where the pulp thins.
// The grain is a precomputed tileable noise field; each sheet samples it
// at its own deterministic offset (from machine.PaperSeed), so no two
// sheets are the same and yet every sheet re-renders identically forever.

import (
	"image"
	"image/draw"

	"github.com/lajosnagyuk/ayfor/internal/units"
)

const (
	// texTile is the wrapping noise field size in pixels at texBaseScale.
	// Power of two so sampling wraps with a mask.
	texTile     = 512
	texTileMask = texTile - 1

	// texBaseScale is the render scale (px per mm) the tile is authored
	// at. Other scales index the tile through a ratio so grain size stays
	// constant in millimetres, not pixels.
	texBaseScale = 8.0

	// grainSeed keys the noise field itself. A model-level constant: the
	// field is shared by all machines; per-seed variety comes entirely
	// from each sheet's sampling offset.
	grainSeed = 0x50A9E7C0FFEE1234

	// Octave amplitudes in RGB levels (out of 255). Total worst case ~±6
	// levels, weighted toward the low-frequency mottle: the first cut
	// (±3, evenly split) measured at only ±2 levels in a real screenshot
	// of the fit-to-window GUI - below perception - because the window
	// downscale averages fine grain toward zero while low-frequency
	// mottle survives. Visible as gentle tooth on the glass, still flat
	// at reading distance - 50 gsm, not parchment.
	fineAmp   = 1.9
	midAmp    = 1.3
	mottleAmp = 2.8

	// Per-sheet overall brightness wobble, RGB levels.
	sheetTintAmp = 1.5
)

// splitmix advances a splitmix64 state; used for all texture hashing.
func splitmix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// latticeUnit hashes a wrapped lattice point of one octave to [0,1).
func latticeUnit(octave, ix, iy int) float64 {
	h := splitmix(grainSeed + uint64(octave)*0x9E3779B97F4A7C15 + uint64(ix)*0xC2B2AE3D27D4EB4F + uint64(iy)*0x165667B19E3779F9)
	return float64(h>>11) / float64(1<<53)
}

// smooth is the smoothstep fade used for value-noise interpolation.
func smooth(t float64) float64 { return t * t * (3 - 2*t) }

// valueNoise samples one octave of tileable value noise at (x, y) with a
// lattice cell of (cw, ch) pixels. cw and ch must divide texTile so the
// lattice wraps cleanly.
func valueNoise(octave, x, y, cw, ch int) float64 {
	nx, ny := texTile/cw, texTile/ch
	ix, iy := x/cw, y/ch
	fx := smooth(float64(x%cw) / float64(cw))
	fy := smooth(float64(y%ch) / float64(ch))
	ix1, iy1 := (ix+1)%nx, (iy+1)%ny
	a := latticeUnit(octave, ix, iy)
	b := latticeUnit(octave, ix1, iy)
	c := latticeUnit(octave, ix, iy1)
	d := latticeUnit(octave, ix1, iy1)
	return (a*(1-fx)+b*fx)*(1-fy) + (c*(1-fx)+d*fx)*fy // 0..1
}

// buildGrainTile bakes the octaves into one wrapping tile of RGB-level
// deltas. Fibre cells are wider than tall: machine-made paper has a grain
// direction, and typewriter stock feeds with it running across the page.
func buildGrainTile() []float32 {
	t := make([]float32, texTile*texTile)
	for y := range texTile {
		for x := range texTile {
			v := (valueNoise(1, x, y, 4, 2) - 0.5) * 2 * fineAmp
			v += (valueNoise(2, x, y, 16, 8) - 0.5) * 2 * midAmp
			v += (valueNoise(3, x, y, 128, 64) - 0.5) * 2 * mottleAmp
			t[y*texTile+x] = float32(v)
		}
	}
	return t
}

// grain returns the renderer's noise tile, built eagerly in New: the old
// build-on-first-use was a data race for a Renderer shared between the
// GUI thread and a background export, and the tile is always needed by
// the first page anyway.
func (r *Renderer) grain() []float32 {
	return r.grainTile
}

// texIndex precomputes tile indices for each pixel coordinate along one
// axis, folding in the scale ratio and a per-sheet offset, so the page
// fill loop is pure array lookups.
func (r *Renderer) texIndex(n int, offset uint64) []int {
	k := texBaseScale / r.Scale
	idx := make([]int, n)
	for i := range idx {
		idx[i] = (int(float64(i)*k) + int(offset)) & texTileMask
	}
	return idx
}

// NewPage allocates a paper-coloured A4 bitmap at the renderer's scale,
// grained deterministically from paperSeed (see machine.PaperSeed).
func (r *Renderer) NewPage(paperSeed uint64) *image.RGBA {
	w := int(units.PaperWidthMM*r.Scale + 0.5)
	h := int(units.PaperHeightMM*r.Scale + 0.5)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{paper}, image.Point{}, draw.Src)

	tile := r.grain()
	xIdx := r.texIndex(w, splitmix(paperSeed+1))
	yIdx := r.texIndex(h, splitmix(paperSeed+2))
	tint := (float64(splitmix(paperSeed+3)>>11)/float64(1<<53)*2 - 1) * sheetTintAmp

	for y := range h {
		row := tile[yIdx[y]*texTile:]
		pix := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := range w {
			d := float64(row[xIdx[x]]) + tint
			p := pix[x*4 : x*4+4 : x*4+4]
			p[0] = clampByte(float64(paper.R) + d)
			p[1] = clampByte(float64(paper.G) + d)
			p[2] = clampByte(float64(paper.B) + d)
		}
	}
	return img
}

func clampByte(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}
