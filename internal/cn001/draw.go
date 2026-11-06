package cn001

// Drawing a key.
//
// Marks are a few bold strokes and dots, measured in pixels from the key's
// centre with y pointing down, and rasterised here rather than through a
// graphics library: a few shapes at 96 pixels do not need a font, and this
// way the drawing is testable anywhere (ADR 0016). Only the badge is
// anti-aliased; the border and background are square and need nothing.

import (
	"bytes"
	"embed"
	"image"
	"image/jpeg"
	"math"
)

const (
	badgeRadius  = 27.0
	borderWidth  = 5
	keylineWidth = 2 // dark, inside the border, so it shows on a pale project too
	supersample  = 3
	LogoPixels   = 56
	jpegQuality  = 90
)

type partKind int

const (
	stroke partKind = iota
	dot
	arc
)

// part is one shape of a mark. A stroke runs from (ax, ay) to (bx, by) with
// half-width half; a dot is centred on (ax, ay) with radius r; an arc is
// centred on (ax, ay) with radius r and half-width half, from one angle to
// another in degrees. strength is how much of the mark's colour it has, over
// the badge.
type part struct {
	kind                 partKind
	ax, ay, bx, by, half float64
	r, from, to          float64
	strength             float64
}

func strokeOf(ax, ay, bx, by, half, strength float64) part {
	return part{kind: stroke, ax: ax, ay: ay, bx: bx, by: by, half: half, strength: strength}
}
func dotOf(x, y, r, strength float64) part {
	return part{kind: dot, ax: x, ay: y, r: r, strength: strength}
}
func arcOf(x, y, r, half, from, to, strength float64) part {
	return part{kind: arc, ax: x, ay: y, r: r, half: half, from: from, to: to, strength: strength}
}

const spokes = 8

var marks = map[byte][]part{
	// working: a spinner, frozen -- its spokes fade round from the top, and
	// it never turns.
	'w': func() []part {
		var out []part
		for spoke := 0; spoke < spokes; spoke++ {
			angle := (-90 - 360*float64(spoke)/spokes) * math.Pi / 180
			out = append(out, strokeOf(9*math.Cos(angle), 9*math.Sin(angle),
				19*math.Cos(angle), 19*math.Sin(angle), 3, 1-0.8*float64(spoke)/(spokes-1)))
		}
		return out
	}(),
	// blocked: an exclamation mark
	'b': {strokeOf(0, -15, 0, 3, 4, 1), dotOf(0, 13, 4.5, 1)},
	// done: a tick
	'd': {strokeOf(-13, 0, -4, 9, 4, 1), strokeOf(-4, 9, 13, -10, 4, 1)},
	// unknown: a question mark
	'u': {arcOf(0, -7, 8, 3.5, -180, 90, 1), strokeOf(0, 1, 0, 5, 3.5, 1), dotOf(0, 13, 4, 1)},
	// the microphone key: a microphone -- capsule, cradle, stand
	'm': {strokeOf(0, -13, 0, -2, 6, 1), arcOf(0, -4, 11, 2, 0, 180, 1),
		strokeOf(0, 7, 0, 13, 2, 1), strokeOf(-7, 14, 7, 14, 2, 1)},
	// the return key: a return arrow
	'e': {strokeOf(12, -12, 12, 4, 3, 1), strokeOf(12, 4, -10, 4, 3, 1),
		strokeOf(-10, 4, -3, -3, 3, 1), strokeOf(-10, 4, -3, 11, 3, 1)},
}

func toSegment(x, y, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := max(0, min(1, ((x-ax)*dx+(y-ay)*dy)/(dx*dx+dy*dy)))
	return math.Hypot(x-ax-t*dx, y-ay-t*dy)
}

// strength is how much of the mark's colour shows at (x, y): 0 for none.
func strength(parts []part, x, y float64) float64 {
	best := 0.0
	for _, p := range parts {
		inside := false
		switch p.kind {
		case stroke:
			inside = toSegment(x, y, p.ax, p.ay, p.bx, p.by) <= p.half
		case dot:
			inside = math.Hypot(x-p.ax, y-p.ay) <= p.r
		case arc:
			angle := math.Atan2(y-p.ay, x-p.ax) * 180 / math.Pi
			inside = math.Abs(math.Hypot(x-p.ax, y-p.ay)-p.r) <= p.half && p.from <= angle && angle <= p.to
		}
		if inside {
			best = max(best, p.strength)
		}
	}
	return best
}

func mix(under, over RGB, amount float64) RGB {
	var out RGB
	for i := range out {
		out[i] = uint8(math.Round(float64(under[i]) + (float64(over[i])-float64(under[i]))*amount))
	}
	return out
}

// badgePixel is one pixel near the badge, averaged over a few samples so its
// edges are smooth.
func badgePixel(look Look, colour RGB, x, y int) RGB {
	parts := marks[look.Mark]
	var total [3]float64
	for sy := 0; sy < supersample; sy++ {
		for sx := 0; sx < supersample; sx++ {
			px := float64(x) + (float64(sx)+0.5)/supersample - KeyPixels/2
			py := float64(y) + (float64(sy)+0.5)/supersample - KeyPixels/2
			sample := look.Background
			if math.Hypot(px, py) <= badgeRadius {
				sample = mix(badge, colour, strength(parts, px, py))
			}
			for c := range total {
				total[c] += float64(sample[c])
			}
		}
	}
	var out RGB
	for c := range out {
		out[c] = uint8(math.Round(total[c] / (supersample * supersample)))
	}
	return out
}

// Pixels is an image as it should appear, row by row.
type Pixels [KeyPixels][KeyPixels]RGB

// Shown is the key as it should appear.
func Shown(look Look) *Pixels {
	var p Pixels
	n := KeyPixels
	for y := range p {
		for x := range p[y] {
			p[y][x] = look.Background
		}
	}
	if look.Focused {
		for y := 0; y < n; y++ {
			for x := 0; x < n; x++ {
				edge := min(x, y, n-1-x, n-1-y)
				if edge < borderWidth {
					p[y][x] = border
				} else if edge < borderWidth+keylineWidth {
					p[y][x] = badge
				}
			}
		}
	}
	if look.Mark != 0 {
		colour, ok := markColour[look.Mark]
		if !ok {
			colour = symbol
		}
		if look.Muted {
			colour = muted
		}
		reach := int(math.Ceil(badgeRadius)) + 1
		for y := n/2 - reach; y < n/2+reach; y++ {
			for x := n/2 - reach; x < n/2+reach; x++ {
				p[y][x] = badgePixel(look, colour, x, y)
			}
		}
	}
	if look.Logo != "" {
		offset := (n - LogoPixels) / 2
		mask := logoMask(look.Logo)
		for y := 0; y < LogoPixels; y++ {
			for x := 0; x < LogoPixels; x++ {
				if coverage := mask[y*LogoPixels+x]; coverage != 0 {
					p[offset+y][offset+x] = mix(p[offset+y][offset+x], logoColour, float64(coverage)/255)
				}
			}
		}
	}
	return &p
}

// Turned is the image as the panel wants it.
//
// The panel turns every image 90 degrees clockwise -- measured on the board,
// a corner drawn top-left appears top-right -- so the image is drawn turned a
// quarter anticlockwise: what should show at (x, y) is drawn at (y, 95 - x).
func Turned(p *Pixels) *image.RGBA {
	n := KeyPixels
	out := image.NewRGBA(image.Rect(0, 0, n, n))
	for x := 0; x < n; x++ {
		for y := 0; y < n; y++ {
			i := out.PixOffset(y, n-1-x)
			c := p[y][x]
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = c[0], c[1], c[2], 255
		}
	}
	return out
}

// Encode is an image as the JPEG the panel is sent.
func Encode(img image.Image) ([]byte, error) {
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// RenderKey is a key's JPEG.
func RenderKey(look Look) ([]byte, error) { return Encode(Turned(Shown(look))) }

//go:embed logos/*.mask
var logoFiles embed.FS

// logoMask is a forge's logo as a coverage mask, LogoPixels square, one byte
// a pixel: how much of that pixel is logo, from 0 to 255. Baked from
// assets/logos by scripts/bake_logos.py (ADR 0018).
func logoMask(name string) []byte {
	data, err := logoFiles.ReadFile("logos/" + name + ".mask")
	if err != nil || len(data) != LogoPixels*LogoPixels {
		return make([]byte, LogoPixels*LogoPixels)
	}
	return data
}

func hasLogo(name string) bool {
	_, err := logoFiles.ReadFile("logos/" + name + ".mask")
	return err == nil
}
