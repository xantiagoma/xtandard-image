package imageinfo

import "math"

// RGB is an 8-bit opaque colour.
type RGB struct {
	R, G, B uint8
}

// DominantColors are imgproxy's six dominant colour classes (info
// `dominant_colors`). A nil entry was not found in the image (and not
// built).
type DominantColors struct {
	Vibrant, LightVibrant, DarkVibrant *RGB
	Muted, LightMuted, DarkMuted       *RGB
}

// Swatch targets and weights of the Vibrant.js / Android Palette
// algorithm (node-vibrant DefaultGenerator options), which imgproxy's
// six classes follow.
const (
	targetDarkLuma          = 0.26
	maxDarkLuma             = 0.45
	minLightLuma            = 0.55
	targetLightLuma         = 0.74
	minNormalLuma           = 0.3
	targetNormalLuma        = 0.5
	maxNormalLuma           = 0.7
	targetMutedSaturation   = 0.3
	maxMutedSaturation      = 0.4
	targetVibrantSaturation = 1.0
	minVibrantSaturation    = 0.35
	weightSaturation        = 3.0
	weightLuma              = 6.5
	weightPopulation        = 0.5

	// dominantSwatches is the median-cut size (node-vibrant colorCount).
	dominantSwatches = 64
	// dominantMinAlpha: more transparent pixels are ignored (node-vibrant).
	dominantMinAlpha = 125
	// dominantBits is the per-channel precision of the histogram (MMCQ
	// sigbits); swatch colours are the mean of the real pixel values.
	dominantBits = 5
)

// target is one of the six classes: the luma and saturation ranges a
// swatch must fall in and the values it is scored against.
type target struct {
	lumaTarget, lumaMin, lumaMax float64
	satTarget, satMin, satMax    float64
	vibrant                      bool
}

// The six classes, in node-vibrant's selection order: Vibrant,
// LightVibrant, DarkVibrant, Muted, LightMuted, DarkMuted.
var targets = [6]target{
	{targetNormalLuma, minNormalLuma, maxNormalLuma, targetVibrantSaturation, minVibrantSaturation, 1, true},
	{targetLightLuma, minLightLuma, 1, targetVibrantSaturation, minVibrantSaturation, 1, true},
	{targetDarkLuma, 0, maxDarkLuma, targetVibrantSaturation, minVibrantSaturation, 1, true},
	{targetNormalLuma, minNormalLuma, maxNormalLuma, targetMutedSaturation, 0, maxMutedSaturation, false},
	{targetLightLuma, minLightLuma, 1, targetMutedSaturation, 0, maxMutedSaturation, false},
	{targetDarkLuma, 0, maxDarkLuma, targetMutedSaturation, 0, maxMutedSaturation, false},
}

// fallbacks lists, per class, the classes a missing one is built from, in
// order of preference: the same family (vibrant or muted) first, then the
// same tone of the other family.
var fallbacks = [6][5]int{
	{2, 1, 3, 5, 4}, // Vibrant ← DarkVibrant, LightVibrant, Muted, DarkMuted, LightMuted
	{0, 2, 4, 3, 5}, // LightVibrant ← Vibrant, DarkVibrant, LightMuted, Muted, DarkMuted
	{0, 1, 5, 3, 4}, // DarkVibrant ← Vibrant, LightVibrant, DarkMuted, Muted, LightMuted
	{5, 4, 0, 2, 1}, // Muted ← DarkMuted, LightMuted, Vibrant, DarkVibrant, LightVibrant
	{3, 5, 1, 0, 2}, // LightMuted ← Muted, DarkMuted, LightVibrant, Vibrant, DarkVibrant
	{3, 4, 2, 0, 1}, // DarkMuted ← Muted, LightMuted, DarkVibrant, Vibrant, LightVibrant
}

// Dominant finds m's dominant colours (imgproxy info `dominant_colors`)
// with the Vibrant.js / Android Palette method: pixels with alpha of at
// least 125 are quantised by median cut into up to 64 swatches; each class
// takes the not-yet-chosen swatch within its luma and saturation ranges
// that scores best on closeness to the class's target saturation (weight
// 3) and luma (6.5) and on population (0.5).
//
// With buildMissed, every class no swatch fits is derived from a found
// one (see fallbacks): its hue is kept, its lightness set to the class's
// target luma and, when it comes from the other family, its saturation
// to the class's target saturation (an achromatic source stays grey).
// Nothing is built when no swatch was found at all.
func Dominant(m *Image, buildMissed bool) (DominantColors, error) {
	if err := m.Validate(); err != nil {
		return DominantColors{}, err
	}
	const shift = 8 - dominantBits
	hist := make(map[uint32]*bucket, 1024)
	for p := range m.Width * m.Height {
		px := m.Pix[p*4 : p*4+4 : p*4+4]
		if px[3] < dominantMinAlpha {
			continue
		}
		k := uint32(px[0]>>shift)<<16 | uint32(px[1]>>shift)<<8 | uint32(px[2]>>shift)
		b := hist[k]
		if b == nil {
			b = &bucket{key: [4]uint8{px[0] >> shift, px[1] >> shift, px[2] >> shift, 0}}
			hist[k] = b
		}
		b.n++
		b.sum[0] += uint64(px[0])
		b.sum[1] += uint64(px[1])
		b.sum[2] += uint64(px[2])
		b.sum[3] += 255
	}
	buckets := make([]bucket, 0, len(hist))
	for _, b := range hist {
		buckets = append(buckets, *b)
	}
	boxes := medianCut(buckets, dominantSwatches, 3)

	type swatch struct {
		rgb     RGB
		h, s, l float64
		n       int
	}
	sw := make([]swatch, len(boxes))
	maxPop := 0
	for i := range boxes {
		c := boxes[i].mean()
		h, s, l := rgbToHSL(c.R, c.G, c.B)
		sw[i] = swatch{rgb: RGB{c.R, c.G, c.B}, h: h, s: s, l: l, n: boxes[i].n}
		maxPop = max(maxPop, boxes[i].n)
	}

	var found [6]*swatch
	used := make([]bool, len(sw))
	for t, tg := range targets {
		best, bestScore := -1, math.Inf(-1)
		for i := range sw {
			w := &sw[i]
			if used[i] || w.s < tg.satMin || w.s > tg.satMax || w.l < tg.lumaMin || w.l > tg.lumaMax {
				continue
			}
			score := (weightSaturation*(1-math.Abs(w.s-tg.satTarget)) +
				weightLuma*(1-math.Abs(w.l-tg.lumaTarget)) +
				weightPopulation*float64(w.n)/float64(maxPop)) /
				(weightSaturation + weightLuma + weightPopulation)
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		if best >= 0 {
			used[best] = true
			found[t] = &sw[best]
		}
	}

	var out [6]*RGB
	for t := range found {
		if found[t] != nil {
			c := found[t].rgb
			out[t] = &c
		}
	}
	if buildMissed {
		for t := range out {
			if out[t] != nil {
				continue
			}
			for _, from := range fallbacks[t] {
				src := found[from]
				if src == nil {
					continue
				}
				s := src.s
				if targets[from].vibrant != targets[t].vibrant && s > 0 {
					s = targets[t].satTarget
				}
				c := hslToRGB(src.h, s, targets[t].lumaTarget)
				out[t] = &c
				break
			}
		}
	}
	return DominantColors{
		Vibrant: out[0], LightVibrant: out[1], DarkVibrant: out[2],
		Muted: out[3], LightMuted: out[4], DarkMuted: out[5],
	}, nil
}

// rgbToHSL converts to hue [0,1), saturation and lightness [0,1].
func rgbToHSL(r8, g8, b8 uint8) (h, s, l float64) {
	r, g, b := float64(r8)/255, float64(g8)/255, float64(b8)/255
	hi, lo := max(r, g, b), min(r, g, b)
	l = (hi + lo) / 2
	if hi == lo {
		return 0, 0, l
	}
	d := hi - lo
	if l > 0.5 {
		s = d / (2 - hi - lo)
	} else {
		s = d / (hi + lo)
	}
	switch hi {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, l
}

// hslToRGB is the inverse of rgbToHSL, rounded to 8 bits.
func hslToRGB(h, s, l float64) RGB {
	if s == 0 {
		v := to8(l)
		return RGB{v, v, v}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	return RGB{to8(hueToRGB(p, q, h+1.0/3)), to8(hueToRGB(p, q, h)), to8(hueToRGB(p, q, h-1.0/3))}
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t++
	}
	if t > 1 {
		t--
	}
	switch {
	case t < 1.0/6:
		return p + (q-p)*6*t
	case t < 0.5:
		return q
	case t < 2.0/3:
		return p + (q-p)*(2.0/3-t)*6
	default:
		return p
	}
}

// to8 maps [0,1] to a rounded byte.
func to8(v float64) uint8 {
	return uint8(math.Round(min(max(v, 0), 1) * 255))
}
