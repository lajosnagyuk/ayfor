// Package render stamps strikes onto a page bitmap. The page is a bitmap
// because paper is a bitmap: nothing reflows, ink only accumulates.
//
// Type size follows the machine, not the font: a typewriter slot IS the
// character width, so the em is chosen to make the font's monospace
// advance equal the slot width (Courier's advance is 0.6 em; Pica lands
// on the classic 12 pt).
package render

import (
	"container/list"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/lajosnagyuk/ayfor/assets"
	"github.com/lajosnagyuk/ayfor/internal/page"
	"github.com/lajosnagyuk/ayfor/internal/typewriter"
	"github.com/lajosnagyuk/ayfor/internal/units"
)

// paper is the sheet colour: warm, aged-white, deliberately not #FFFFFF.
// Unexported with an accessor: an exported mutable package var would let
// any caller repaint every future render and silently break the
// "same file renders identically forever" promise.
var paper = color.RGBA{R: 0xF7, G: 0xF3, B: 0xE9, A: 0xFF}

// ink is the ribbon colour: near-black with a hint of blue-grey, never
// pure black — full alpha is modulated per strike.
var ink = color.RGBA{R: 0x1C, G: 0x1B, B: 0x22, A: 0xFF}

// PaperColor returns the sheet colour (a copy; renders cannot be
// recoloured from outside).
func PaperColor() color.RGBA { return paper }

type glyphMask struct {
	mask *image.Alpha
	// offset of mask origin relative to the dot (baseline pen position)
	offX, offY int
	advance    float64 // px
}

// Renderer rasterizes strikes at a fixed scale (pixels per millimetre).
type Renderer struct {
	Scale float64 // px per mm

	sfnt            *opentype.Font
	faces           map[units.Pitch]font.Face
	cache           map[glyphCacheKey]*glyphMask
	cacheLRU        *list.List
	cacheEntries    map[glyphCacheKey]*list.Element
	cachePixels     int64
	grainTile       []float32 // paper grain noise field, built in New
	emMM            float64
	scaleX          float64
	baselineShiftMM float64
}

type glyphCacheKey struct {
	glyph sfnt.GlyphIndex
	pitch units.Pitch
}

type glyphCacheEntry struct {
	key    glyphCacheKey
	pixels int64
}

const (
	maxRenderScale       = 32.0
	maxGlyphDimension    = 1024
	maxGlyphRasterPixels = 1 << 20
	maxGlyphCachePixels  = 8 << 20
	maxGlyphCacheEntries = 512
)

func validScale(scale float64) bool {
	return scale > 0 && scale <= maxRenderScale && !math.IsNaN(scale) && !math.IsInf(scale, 0)
}

// New creates a renderer. scale is pixels per millimetre (5.67 ≈ 144 dpi;
// use ×2 for retina backing).
func New(scale float64) (*Renderer, error) {
	if !validScale(scale) {
		return nil, fmt.Errorf("render: scale must be finite and within 0..%g px/mm", maxRenderScale)
	}
	f, err := opentype.Parse(assets.CourierPrimeRegular)
	if err != nil {
		return nil, err
	}
	return &Renderer{
		Scale:        scale,
		sfnt:         f,
		faces:        make(map[units.Pitch]font.Face),
		cache:        make(map[glyphCacheKey]*glyphMask),
		cacheLRU:     list.New(),
		cacheEntries: make(map[glyphCacheKey]*list.Element),
		grainTile:    buildGrainTile(), // eager: lazy init raced shared renderers
		scaleX:       1,
	}, nil
}

// NewWithProfile creates a renderer from one exact materialized typewriter.
func NewWithProfile(scale float64, profile *typewriter.Profile) (*Renderer, error) {
	if profile == nil {
		return nil, fmt.Errorf("render: nil typewriter profile")
	}
	if !validScale(scale) {
		return nil, fmt.Errorf("render: scale must be finite and within 0..%g px/mm", maxRenderScale)
	}
	f, err := opentype.Parse(profile.Font)
	if err != nil {
		return nil, err
	}
	return &Renderer{
		Scale: scale, sfnt: f, faces: make(map[units.Pitch]font.Face),
		cache: make(map[glyphCacheKey]*glyphMask), cacheLRU: list.New(),
		cacheEntries: make(map[glyphCacheKey]*list.Element), grainTile: buildGrainTile(),
		emMM:            float64(profile.Manifest.Typeface.EMMicrometres) / 1000,
		scaleX:          float64(profile.Manifest.Typeface.ScaleXPermille) / 1000,
		baselineShiftMM: float64(profile.Manifest.Typeface.BaselineShiftUM) / 1000,
	}, nil
}

func (r *Renderer) emPixels(p units.Pitch) float64 {
	emMM := r.emMM
	if emMM == 0 {
		emMM = p.SlotMM() / units.CourierAdvanceEM
	}
	return emMM * r.Scale
}

// face returns (creating on demand) the face sized for a pitch.
func (r *Renderer) face(p units.Pitch) (font.Face, error) {
	if f, ok := r.faces[p]; ok {
		return f, nil
	}
	emPX := r.emPixels(p)
	f, err := opentype.NewFace(r.sfnt, &opentype.FaceOptions{
		Size:    emPX, // with DPI 72, size in points == size in pixels
		DPI:     72,
		Hinting: font.HintingNone, // hinting would fight sub-pixel placement
	})
	if err != nil {
		return nil, err
	}
	r.faces[p] = f
	return f, nil
}

func (r *Renderer) validateGlyphRaster(ru rune, idx sfnt.GlyphIndex, p units.Pitch) error {
	var buf sfnt.Buffer
	ppem := fixed.Int26_6(r.emPixels(p)*64 + 0.5)
	bounds, _, err := r.sfnt.GlyphBounds(&buf, idx, ppem, font.HintingNone)
	if err != nil {
		return fmt.Errorf("render: glyph %U bounds: %w", ru, err)
	}
	w := bounds.Max.X.Ceil() - bounds.Min.X.Floor()
	h := bounds.Max.Y.Ceil() - bounds.Min.Y.Floor()
	if r.scaleX > 1 {
		w = int(math.Ceil(float64(w) * r.scaleX))
	}
	if w < 0 || h < 0 || w > maxGlyphDimension || h > maxGlyphDimension || int64(w)*int64(h) > maxGlyphRasterPixels {
		return fmt.Errorf("render: refusing unsafe %dx%d glyph raster for %U", w, h, ru)
	}
	return nil
}

func (r *Renderer) cacheGet(key glyphCacheKey) (*glyphMask, bool) {
	g, ok := r.cache[key]
	if ok {
		r.cacheLRU.MoveToFront(r.cacheEntries[key])
	}
	return g, ok
}

func (r *Renderer) cachePut(key glyphCacheKey, g *glyphMask) {
	pixels := int64(g.mask.Bounds().Dx()) * int64(g.mask.Bounds().Dy())
	r.cache[key] = g
	e := r.cacheLRU.PushFront(glyphCacheEntry{key: key, pixels: pixels})
	r.cacheEntries[key] = e
	r.cachePixels += pixels
	for r.cachePixels > maxGlyphCachePixels || len(r.cache) > maxGlyphCacheEntries {
		oldest := r.cacheLRU.Back()
		if oldest == nil {
			break
		}
		entry := oldest.Value.(glyphCacheEntry)
		delete(r.cache, entry.key)
		delete(r.cacheEntries, entry.key)
		r.cachePixels -= entry.pixels
		r.cacheLRU.Remove(oldest)
	}
}

func (r *Renderer) resolvedGlyph(ru rune) (sfnt.GlyphIndex, rune, error) {
	var buf sfnt.Buffer
	idx, err := r.sfnt.GlyphIndex(&buf, ru)
	if err != nil {
		return 0, 0, fmt.Errorf("render: glyph %U index: %w", ru, err)
	}
	if idx != 0 {
		return idx, ru, nil
	}
	replacement, err := r.sfnt.GlyphIndex(&buf, '�')
	if err != nil {
		return 0, 0, fmt.Errorf("render: replacement glyph index: %w", err)
	}
	if replacement != 0 {
		return replacement, '�', nil
	}
	return 0, ru, nil
}

// glyph rasterizes (and caches) the un-transformed glyph mask.
func (r *Renderer) glyph(ru rune, p units.Pitch) (*glyphMask, error) {
	idx, renderRune, err := r.resolvedGlyph(ru)
	if err != nil {
		return nil, err
	}
	key := glyphCacheKey{idx, p}
	if g, ok := r.cacheGet(key); ok {
		return g, nil
	}
	f, err := r.face(p)
	if err != nil {
		return nil, err
	}
	if err := r.validateGlyphRaster(renderRune, idx, p); err != nil {
		return nil, err
	}
	dot := fixed.Point26_6{}
	dr, mask, maskp, adv, ok := f.Glyph(dot, renderRune)
	if !ok {
		// Unknown glyph: render the replacement box the font provides
		// for U+FFFD, or an empty mask as last resort.
		dr, mask, maskp, adv, ok = f.Glyph(dot, '�')
		if !ok {
			g := &glyphMask{mask: image.NewAlpha(image.Rect(0, 0, 1, 1))}
			r.cachePut(key, g)
			return g, nil
		}
	}
	// Copy the mask region into an owned image.Alpha.
	m := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
	draw.Draw(m, m.Bounds(), mask, maskp, draw.Src)
	offX := dr.Min.X
	advance := float64(adv) / 64.0
	if r.scaleX != 1 && m.Bounds().Dx() > 0 {
		w := max(1, int(float64(m.Bounds().Dx())*r.scaleX+0.5))
		scaled := image.NewAlpha(image.Rect(0, 0, w, m.Bounds().Dy()))
		xdraw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), m, m.Bounds(), draw.Src, nil)
		m = scaled
		offX = int(float64(offX) * r.scaleX)
		advance *= r.scaleX
	}
	g := &glyphMask{
		mask:    m,
		offX:    offX,
		offY:    dr.Min.Y,
		advance: advance,
	}
	r.cachePut(key, g)
	return g, nil
}

// Impression texture constants (render half of the personality model -
// the machine decides ink/Tex/Fill, these decide what a die face pressing
// inked cloth into soft paper does with them). Documented in
// docs/DESIGN.md section 3.
const (
	// Deboss relief: the hammer presses the glyph INTO the paper, so under
	// the fixed page light (upper left, the way a desk lamp sits) stroke
	// flanks facing the light fall into shadow and the far flanks catch
	// it. Sampled as a directional derivative of the glyph mask.
	// Tuned upward twice on owner feedback: 0.11/0.16 was invisible in
	// the fit-to-window GUI (a ~1 px flank disappears under the window
	// downscale), 0.17/0.26 read as "still a tiny bit subtle". Current
	// values are the third pass; go gently from here - deboss should be
	// felt, not seen first.
	reliefStepMM   = 0.20 // sample distance for the derivative
	reliefStrength = 0.34 // highlight alpha at full ink; shadows slightly less

	// Ribbon cloth never transfers ink evenly: per-strike speckle, worse
	// on light strikes (partial face contact), plus the paper's own fibre
	// tooth soaking ink unevenly.
	speckleBase  = 0.16
	speckleLight = 0.45
	fiberAmp     = 0.10
	grainNorm    = fineAmp + midAmp + mottleAmp // grainAt() -> roughly [-1,1]

	// Die fouling: machine.Strike.Fill (0..fillMax) enlarges the effective
	// die face, fattening strokes and closing counters on gunked hammers.
	fillGain = 0.22

	// stampPadPX pads the stamp box beyond the rotated glyph radius (in
	// pixels, on top of the relief step) so anti-aliased edges and
	// sub-pixel placement never clip at the box border.
	stampPadPX = 2

	// inkTransfer maps model ink weight to glyph alpha. Deliberately under
	// 1: cloth ribbon never transfers at full saturation, and without this
	// headroom every strike with ink >= 1.0 clamps to identical maximum
	// black - which was ~60% of a real typing session, and exactly why the
	// page read as uniform (measured on the owner's own draft: mean ink
	// 1.02, 95% of strikes inside 0.90-1.15, all crushed against the
	// ceiling). At 0.85, a nominal strike prints dark, a heavy one darker
	// still, and the model's force variation survives to the page.
	// Overstrikes still compound toward true black via source-over.
	inkTransfer = 0.85
)

// Relief blend targets: warm paper lifted into light, warm shadow - not a
// grayscale emboss.
var (
	reliefLight = color.RGBA{R: 0xFF, G: 0xFD, B: 0xF4, A: 0xFF}
	reliefShade = color.RGBA{R: 0x5A, G: 0x56, B: 0x4C, A: 0xFF}
)

// texUnit hashes a per-strike seed and a page pixel to [0,1): the ink
// speckle field, unique to every strike, identical on every re-render.
func texUnit(seed uint64, x, y int) float64 {
	h := splitmix(seed ^ (uint64(x)*0xC2B2AE3D27D4EB4F + uint64(y)*0x165667B19E3779F9))
	return float64(h>>11) / float64(1<<53)
}

// Stamp presses one strike onto the page bitmap, deboss relief and all.
func (r *Renderer) Stamp(dst *image.RGBA, rec page.StrikeRec, pitch units.Pitch) error {
	return r.stamp(dst, rec, pitch, true)
}

// StampFlat presses one strike WITHOUT the deboss relief: the ink glyph and
// its per-strike character, but no pressed-into-paper emboss. Used for
// display-only chrome drawn on a transparent overlay, where the relief has no
// paper to modulate and its offset shadow would read as a doubled stroke.
func (r *Renderer) StampFlat(dst *image.RGBA, rec page.StrikeRec, pitch units.Pitch) error {
	return r.stamp(dst, rec, pitch, false)
}

func (r *Renderer) stamp(dst *image.RGBA, rec page.StrikeRec, pitch units.Pitch, deboss bool) error {
	g, err := r.glyph(rec.Rune, pitch)
	if err != nil {
		return err
	}
	if g.mask.Bounds().Empty() {
		return nil
	}
	app := rec.App

	// Pen position: slot centre plus the machine's offsets, in pixels.
	cx := (rec.XMM + app.DX) * r.Scale
	cy := (rec.YMM + app.DY + r.baselineShiftMM) * r.Scale
	dotX := cx - g.advance/2 // centre the monospace advance on the slot
	dotY := cy

	// Glyph centre in mask coordinates, for rotation.
	mb := g.mask.Bounds()
	gcx := float64(mb.Dx()) / 2
	gcy := float64(mb.Dy()) / 2

	sin, cos := math.Sincos(app.TiltDeg * math.Pi / 180)

	// Gradient direction unit vector.
	gsin, gcos := math.Sincos(app.GradAxis)

	// Die fouling scales the whole impression up a few percent.
	fill := 1 + fillGain*app.Fill

	// Light step (page space, toward the upper-left lamp) rotated into
	// mask space and compensated for the fouling scale-up.
	step := reliefStepMM * r.Scale
	lux := (cos*-step + sin*-step) / fill
	luy := (-sin*-step + cos*-step) / fill
	reliefAmt := reliefStrength * math.Min(1, app.Ink)

	// Speckle for this strike: light strikes are patchier. spNorm keeps
	// the average ink weight where the model put it - the noise
	// redistributes ink, it must not fade the page.
	sp := speckleBase + speckleLight*math.Max(0, 1.05-app.Ink)
	spNorm := 1 / (1 - 0.5*sp)

	// Destination bounding box: mask box around its centre, grown to
	// cover the rotation, the fouling scale-up and the relief step.
	rad := math.Hypot(gcx, gcy)*fill + stampPadPX + step
	// Mask-centre position on the page:
	mcx := dotX + float64(g.offX) + gcx
	mcy := dotY + float64(g.offY) + gcy
	x0 := int(mcx - rad)
	y0 := int(mcy - rad)
	x1 := int(mcx+rad) + 1
	y1 := int(mcy+rad) + 1

	bounds := dst.Bounds()
	if x0 < bounds.Min.X {
		x0 = bounds.Min.X
	}
	if y0 < bounds.Min.Y {
		y0 = bounds.Min.Y
	}
	if x1 > bounds.Max.X {
		x1 = bounds.Max.X
	}
	if y1 > bounds.Max.Y {
		y1 = bounds.Max.Y
	}

	// Half the ink unevenness on each side of the face.
	grad := app.GradAmt / 2

	tile := r.grain()
	texK := texBaseScale / r.Scale

	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			// Inverse-rotate the page pixel into mask space; dividing by
			// fill samples a slightly shrunk mask grid = a fatter print.
			dx := float64(px) + 0.5 - mcx
			dy := float64(py) + 0.5 - mcy
			ux := (cos*dx + sin*dy) / fill
			uy := (-sin*dx + cos*dy) / fill
			a := bilinear(g.mask, ux+gcx, uy+gcy)
			if deboss {
				// Deboss lighting first, so the ink lies over the pressed
				// paper. relief > 0: flank facing away from the lamp, lit;
				// relief < 0: flank facing the lamp, shadowed.
				aL := bilinear(g.mask, ux+lux+gcx, uy+luy+gcy)
				if a <= 0 && aL <= 0 {
					continue
				}
				relief := aL - a
				if relief > 1e-3 {
					blendToward(dst, px, py, reliefLight, relief*reliefAmt)
				} else if relief < -1e-3 {
					blendToward(dst, px, py, reliefShade, -relief*reliefAmt*0.85)
				}
			}
			if a <= 0 {
				continue
			}
			// ink gradient across the hammer face: projection of the
			// pixel onto the gradient axis, normalised by glyph radius.
			proj := (ux*gcos + uy*gsin) / rad // -1..1
			ink := app.Ink * (1 - grad*proj)
			// ink texture: per-strike speckle plus the paper's fibre
			// tooth (sampled straight off the grain field).
			gx := int(float64(px)*texK) & texTileMask
			gy := int(float64(py)*texK) & texTileMask
			cov := (1 - sp*texUnit(app.Tex, px, py) - fiberAmp*float64(tile[gy*texTile+gx])/grainNorm) * spNorm
			if cov < 0 {
				cov = 0
			}
			alpha := a * clamp01(ink*cov*inkTransfer)
			if alpha <= 0 {
				continue
			}
			blendInk(dst, px, py, alpha)
		}
	}
	return nil
}

// RenderPage stamps every strike of a page onto a fresh bitmap grained
// from paperSeed (see machine.PaperSeed).
// Pitch note: strikes store their own physical position, so mid-document
// pitch changes are already baked into XMM; the pitch here only selects
// the die size. Documents that change pitch mid-page are rendered with
// the final pitch's die (v1 limitation).
func (r *Renderer) RenderPage(p *page.Page, pitch units.Pitch, paperSeed uint64) (*image.RGBA, error) {
	img := r.NewPage(paperSeed)
	for _, rec := range p.Strikes {
		if err := r.Stamp(img, rec, pitch); err != nil {
			return nil, err
		}
	}
	return img, nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// bilinear samples an alpha mask with bilinear filtering, returning 0..1.
func bilinear(m *image.Alpha, x, y float64) float64 {
	x -= 0.5
	y -= 0.5
	// Fast reject: the stamp loop covers a padded box (rotation, relief,
	// fouling), so most samples land well outside the mask - skip the four
	// bounds-checked lookups for those.
	if x < -1 || y < -1 || x >= float64(m.Rect.Dx()) || y >= float64(m.Rect.Dy()) {
		return 0
	}
	x0 := int(math.Floor(x))
	y0 := int(math.Floor(y))
	fx := x - float64(x0)
	fy := y - float64(y0)
	// Index Pix directly with hoisted dimensions: the four samples run for
	// every pixel of every stamp box (twice with deboss), and AlphaAt pays
	// a Bounds() plus a checked PixOffset per texel. Masks are zero-origin
	// (image.Rect(0,0,w,h), see glyph building), so pix[iy*stride+ix] is
	// AlphaAt exactly.
	w, hgt := m.Rect.Dx(), m.Rect.Dy()
	pix, stride := m.Pix, m.Stride
	get := func(ix, iy int) float64 {
		if ix < 0 || iy < 0 || ix >= w || iy >= hgt {
			return 0
		}
		return float64(pix[iy*stride+ix]) / 255
	}
	a00 := get(x0, y0)
	a10 := get(x0+1, y0)
	a01 := get(x0, y0+1)
	a11 := get(x0+1, y0+1)
	return a00*(1-fx)*(1-fy) + a10*fx*(1-fy) + a01*(1-fx)*fy + a11*fx*fy
}

// blendInk lays ink of the given opacity over a pixel. Plain source-over:
// repeated overstrikes darken naturally, like ink.
func blendInk(dst *image.RGBA, x, y int, alpha float64) {
	blendToward(dst, x, y, ink, alpha)
}

// blendToward source-over blends a pixel toward c with the given opacity.
// image.RGBA stores premultiplied alpha, so the source term is c*alpha and
// the destination decays by (1-alpha) across all four channels. Over the
// opaque page this reduces to the plain RGB lerp it always was; over the
// TRANSPARENT chrome band it composites honestly instead of forcing every
// partial-coverage edge pixel opaque (which read as a dark fringe, and in
// dank mode as a light halo).
func blendToward(dst *image.RGBA, x, y int, c color.RGBA, alpha float64) {
	i := dst.PixOffset(x, y)
	p := dst.Pix[i : i+4 : i+4]
	ia := 1 - alpha
	p[0] = uint8(float64(c.R)*alpha + float64(p[0])*ia + 0.5)
	p[1] = uint8(float64(c.G)*alpha + float64(p[1])*ia + 0.5)
	p[2] = uint8(float64(c.B)*alpha + float64(p[2])*ia + 0.5)
	p[3] = uint8(255*alpha + float64(p[3])*ia + 0.5)
}
