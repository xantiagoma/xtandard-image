package negotiate

import (
	"errors"
	"math"
	"math/bits"
)

// AnalysisMaxDimension bounds the longer side of the sample Analyze expects:
// the caller reduces the processed image to it (nearest neighbour, so no
// colour is invented) before handing over its pixels.
const AnalysisMaxDimension = 256

// mixedBand is the half-width of the score band around the threshold that is
// classed ContentMixed: close to the threshold neither a lossy nor a
// lossless save is clearly smaller, so best tries both.
const mixedBand = 0.75

// edgeMagnitude is the Sobel magnitude (0..255 scale) from which a pixel
// counts as an edge for Analysis.EdgeDensity.
const edgeMagnitude = 32

// Analysis is the content analysis of a processed image (FORMATS §6.1
// point 3).
type Analysis struct {
	// Class is ContentGraphic when Complexity is below the threshold minus
	// the mixed band, ContentPhoto above the threshold plus it, and
	// ContentMixed in between.
	Class ContentClass
	// Complexity is the score: the Shannon entropy (bits) of the Sobel
	// gradient magnitudes of the luma plus a quarter of log2 of the
	// unique-colour count. Flat graphics score about 0–3, text and line art
	// 2–4.5, photos 6.5 and more (on the 0..~12 scale of
	// XI_FORMATS_BEST_COMPLEXITY_THRESHOLD, default 5.5).
	Complexity float64
	// Colors is the number of unique colours (RGBA) in the sample.
	Colors int
	// GradientEntropy is the entropy part of Complexity (bits, 0..8).
	GradientEntropy float64
	// EdgeDensity is the fraction of pixels whose gradient magnitude is at
	// least edgeMagnitude.
	EdgeDensity float64
	// AlphaUsed reports that some pixel of the sample is not opaque.
	AlphaUsed bool
}

// ErrAnalysisInput is returned by Analyze for a pixel buffer that does not
// match its dimensions.
var ErrAnalysisInput = errors.New("negotiate: analysis pixels do not match the dimensions")

// Analyze classifies an 8-bit sample of a processed image: w×h pixels of
// bands interleaved samples (1 grey, 2 grey+alpha, 3 RGB, 4 RGBA), ideally
// reduced to AnalysisMaxDimension first. threshold is
// XI_FORMATS_BEST_COMPLEXITY_THRESHOLD; 0 disables the classification (every
// image is a photo, as imgproxy does). It is pure Go; it allocates a luma
// plane, a colour plane and a colour hash table (about 12 bytes per pixel,
// 0.75 MiB for a 256×192 sample).
func Analyze(pix []byte, w, h, bands int, threshold float64) (Analysis, error) {
	if w <= 0 || h <= 0 || bands < 1 || bands > 4 || len(pix) != w*h*bands {
		return Analysis{}, ErrAnalysisInput
	}
	if threshold <= 0 {
		return Analysis{Class: ContentPhoto}, nil
	}
	alpha := bands == 2 || bands == 4
	n := w * h
	luma := make([]uint8, n)
	colors := make([]uint32, n)
	var a Analysis
	for i := range n {
		p := pix[i*bands : (i+1)*bands]
		var r, g, b, al uint32
		switch bands {
		case 1:
			r, g, b, al = uint32(p[0]), uint32(p[0]), uint32(p[0]), 255
		case 2:
			r, g, b, al = uint32(p[0]), uint32(p[0]), uint32(p[0]), uint32(p[1])
		case 3:
			r, g, b, al = uint32(p[0]), uint32(p[1]), uint32(p[2]), 255
		default:
			r, g, b, al = uint32(p[0]), uint32(p[1]), uint32(p[2]), uint32(p[3])
		}
		if al < 255 {
			a.AlphaUsed = true
		}
		colors[i] = r<<24 | g<<16 | b<<8 | al
		// Rec. 601 luma, premultiplied so alpha edges count as edges.
		y := (299*r + 587*g + 114*b + 500) / 1000
		if alpha {
			y = (y*al + 127) / 255
		}
		luma[i] = uint8(y) //nolint:gosec // G115: a weighted mean of bytes is at most 255
	}
	a.Colors = uniqueColors(colors)

	var hist [256]int
	edges, samples := 0, 0
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			m := sobel(luma, w, x, y)
			hist[m]++
			if m >= edgeMagnitude {
				edges++
			}
			samples++
		}
	}
	if samples > 0 {
		a.GradientEntropy = entropy(hist[:], samples)
		a.EdgeDensity = float64(edges) / float64(samples)
	}
	a.Complexity = a.GradientEntropy + 0.25*math.Log2(float64(max(a.Colors, 1)))
	switch {
	case a.Complexity < threshold-mixedBand:
		a.Class = ContentGraphic
	case a.Complexity <= threshold+mixedBand:
		a.Class = ContentMixed
	default:
		a.Class = ContentPhoto
	}
	return a, nil
}

// sobel returns the Sobel gradient magnitude at (x, y) of an interior pixel,
// scaled so a full black-to-white step scores 255.
func sobel(l []uint8, w, x, y int) uint8 {
	at := func(dx, dy int) int { return int(l[(y+dy)*w+x+dx]) }
	gx := at(1, -1) + 2*at(1, 0) + at(1, 1) - at(-1, -1) - 2*at(-1, 0) - at(-1, 1)
	gy := at(-1, 1) + 2*at(0, 1) + at(1, 1) - at(-1, -1) - 2*at(0, -1) - at(1, -1)
	m := math.Sqrt(float64(gx*gx+gy*gy)) / 4
	return uint8(min(m+0.5, 255))
}

// entropy is the Shannon entropy in bits of a histogram of total samples.
func entropy(hist []int, total int) float64 {
	var e float64
	t := float64(total)
	for _, c := range hist {
		if c > 0 {
			p := float64(c) / t
			e -= p * math.Log2(p)
		}
	}
	return e
}

// uniqueColors counts the distinct values of c with an open-addressing hash
// set (sorting costs several times more on a 256 px sample). Runs of equal
// neighbours, common in graphics, are skipped before hashing.
func uniqueColors(c []uint32) int {
	if len(c) == 0 {
		return 0
	}
	size := 1
	for size < 2*len(c) {
		size <<= 1
	}
	shift := 32 - bits.Len(uint(size-1))
	// Slot value v+1 stores colour v, so 0 marks an empty slot; colour
	// 0xffffffff (opaque white) would overflow and is counted apart.
	table := make([]uint32, size)
	n := 0
	white := false
	prev, first := uint32(0), true
	for _, v := range c {
		if !first && v == prev {
			continue
		}
		prev, first = v, false
		if v == math.MaxUint32 {
			if !white {
				white = true
				n++
			}
			continue
		}
		key := v + 1
		i := int((v * 0x9e3779b1) >> shift)
		for {
			switch table[i] {
			case 0:
				table[i] = key
				n++
			case key:
			default:
				i = (i + 1) & (size - 1)
				continue
			}
			break
		}
	}
	return n
}
