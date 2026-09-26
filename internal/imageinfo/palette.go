package imageinfo

import (
	"cmp"
	"errors"
	"slices"
)

// MaxPaletteColors is the largest palette imgproxy accepts.
const MaxPaletteColors = 256

// ErrPaletteSize is returned for a palette size outside 2..MaxPaletteColors.
var ErrPaletteSize = errors.New("imageinfo: palette size must be 2 to 256")

// Palette builds an RGBA palette of at most n colours for m (imgproxy info
// `palette`) by weighted median cut over the image's exact colours: the
// box with the largest population × channel range is split at the
// population median of its widest channel until there are n boxes, and
// each box contributes the rounded mean of its pixels. Fully transparent
// pixels count as one transparent black colour. An image with at most n
// distinct colours gets exactly those colours. Colours are ordered by
// population, most common first (ties by value).
func Palette(m *Image, n int) ([]Color, error) {
	if n < 2 || n > MaxPaletteColors {
		return nil, ErrPaletteSize
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	hist := make(map[uint32]int, 1024)
	for p := range m.Width * m.Height {
		px := m.Pix[p*4 : p*4+4 : p*4+4]
		var k uint32
		if px[3] != 0 {
			k = uint32(px[0])<<24 | uint32(px[1])<<16 | uint32(px[2])<<8 | uint32(px[3])
		}
		hist[k]++
	}
	buckets := make([]bucket, 0, len(hist))
	for k, c := range hist {
		key := [4]uint8{uint8(k >> 24), uint8(k >> 16), uint8(k >> 8), uint8(k)} //nolint:gosec // G115: byte extraction
		b := bucket{key: key, n: c}
		for i, v := range key {
			b.sum[i] = uint64(v) * uint64(c) //nolint:gosec // G115: c is a pixel count
		}
		buckets = append(buckets, b)
	}
	boxes := medianCut(buckets, n, 4)
	out := make([]Color, len(boxes))
	for i := range boxes {
		out[i] = boxes[i].mean()
	}
	return out, nil
}

// bucket is one histogram entry: the pixels that share key.
type bucket struct {
	key [4]uint8  // coordinates the cut sorts and splits on
	n   int       // pixels
	sum [4]uint64 // per-channel sums of the pixels' real values
}

// box is a set of buckets, one entry of the median-cut result.
type box struct {
	b []bucket
	n int
}

func (bx *box) mean() Color {
	var s [4]uint64
	for i := range bx.b {
		for c := range s {
			s[c] += bx.b[i].sum[c]
		}
	}
	n := uint64(bx.n) //nolint:gosec // G115: pixel counts are positive
	return Color{R: meanByte(s[0], n), G: meanByte(s[1], n), B: meanByte(s[2], n), A: meanByte(s[3], n)}
}

// widest is the channel (of the first channels) with the largest key
// range in bx, and that range.
func (bx *box) widest(channels int) (ch, span int) {
	for c := range channels {
		lo, hi := 255, 0
		for i := range bx.b {
			v := int(bx.b[i].key[c])
			lo, hi = min(lo, v), max(hi, v)
		}
		if hi-lo > span {
			ch, span = c, hi-lo
		}
	}
	return ch, span
}

// medianCut partitions buckets (distinct keys) into at most n boxes over
// the first channels of the key, sorted by population, largest first.
// The result is deterministic: buckets are sorted by key before cutting.
func medianCut(buckets []bucket, n, channels int) []box {
	if len(buckets) == 0 {
		return nil
	}
	slices.SortFunc(buckets, func(a, b bucket) int { return cmpKey(&a.key, &b.key) })
	total := 0
	for i := range buckets {
		total += buckets[i].n
	}
	boxes := make([]box, 1, n)
	boxes[0] = box{b: buckets, n: total}
	for len(boxes) < n {
		best, bestScore, bestCh := -1, 0, 0
		for i := range boxes {
			if len(boxes[i].b) < 2 {
				continue
			}
			ch, span := boxes[i].widest(channels)
			if score := boxes[i].n * span; score > bestScore {
				best, bestScore, bestCh = i, score, ch
			}
		}
		if best < 0 {
			break // every box is a single colour
		}
		lo, hi := split(boxes[best], bestCh)
		boxes[best] = lo
		boxes = append(boxes, hi)
	}
	slices.SortStableFunc(boxes, func(a, b box) int {
		if c := cmp.Compare(b.n, a.n); c != 0 {
			return c
		}
		ca, cb := a.mean(), b.mean()
		return cmpKey(&[4]uint8{ca.R, ca.G, ca.B, ca.A}, &[4]uint8{cb.R, cb.G, cb.B, cb.A})
	})
	return boxes
}

// split cuts bx on channel ch at its population median; both halves are
// non-empty.
func split(bx box, ch int) (lo, hi box) {
	b := bx.b
	slices.SortStableFunc(b, func(x, y bucket) int {
		if c := cmp.Compare(x.key[ch], y.key[ch]); c != 0 {
			return c
		}
		return cmpKey(&x.key, &y.key)
	})
	half := bx.n / 2
	acc, cut := 0, 0
	for i := range len(b) - 1 {
		acc += b[i].n
		cut = i + 1
		if acc >= half {
			break
		}
	}
	// Cut between two channel values, never inside a run of one value:
	// forwards past the run, or back before it when the run ends the box
	// (the channel has at least two values, so one side works).
	if v := b[cut-1].key[ch]; b[cut].key[ch] == v {
		j := cut
		for j < len(b) && b[j].key[ch] == v {
			j++
		}
		if j < len(b) {
			cut = j
		} else {
			for b[cut-1].key[ch] == v {
				cut--
			}
		}
	}
	lo, hi = box{b: b[:cut:cut]}, box{b: b[cut:]}
	for i := range lo.b {
		lo.n += lo.b[i].n
	}
	hi.n = bx.n - lo.n
	return lo, hi
}

func cmpKey(a, b *[4]uint8) int {
	for i := range a {
		if c := cmp.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}
