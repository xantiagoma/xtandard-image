package imageinfo

import "math"

// Downscale returns m fitted inside maxSide×maxSide (aspect ratio kept,
// each side at least 1 px) by area averaging, or m itself when it already
// fits. Colour is averaged weighted by alpha, so transparent pixels do not
// darken their neighbours.
func Downscale(m *Image, maxSide int) (*Image, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if maxSide < 1 {
		return nil, ErrInvalidImage
	}
	if m.Width <= maxSide && m.Height <= maxSide {
		return m, nil
	}
	s := float64(maxSide) / float64(max(m.Width, m.Height))
	w := max(1, int(math.Round(float64(m.Width)*s)))
	h := max(1, int(math.Round(float64(m.Height)*s)))
	return resample(m, min(w, maxSide), min(h, maxSide)), nil
}

// span is the part of source pixel i that falls in a destination pixel.
type span struct {
	i int
	w float64
}

// spans maps each of dst destination pixels to the src source pixels it
// covers, with their coverage (a box filter).
func spans(src, dst int) [][]span {
	out := make([][]span, dst)
	scale := float64(src) / float64(dst)
	for d := range dst {
		lo, hi := float64(d)*scale, float64(d+1)*scale
		for i := int(lo); i < src && float64(i) < hi; i++ {
			w := min(hi, float64(i+1)) - max(lo, float64(i))
			if w > 0 {
				out[d] = append(out[d], span{i, w})
			}
		}
	}
	return out
}

// resample is an area-average resize of m to w×h.
func resample(m *Image, w, h int) *Image {
	xs, ys := spans(m.Width, w), spans(m.Height, h)
	out := &Image{Width: w, Height: h, Pix: make([]byte, w*h*4)}
	for y := range h {
		for x := range w {
			var r, g, b, a, area float64
			for _, sy := range ys[y] {
				row := m.Pix[sy.i*m.Width*4:]
				for _, sx := range xs[x] {
					k := sx.w * sy.w
					px := row[sx.i*4 : sx.i*4+4 : sx.i*4+4]
					al := float64(px[3]) * k
					r += float64(px[0]) * al
					g += float64(px[1]) * al
					b += float64(px[2]) * al
					a += al
					area += k
				}
			}
			o := out.Pix[(y*w+x)*4 : (y*w+x)*4+4 : (y*w+x)*4+4]
			if a > 0 {
				o[0], o[1], o[2] = clamp8(r/a), clamp8(g/a), clamp8(b/a)
			}
			o[3] = clamp8(a / area)
		}
	}
	return out
}

func clamp8(v float64) uint8 {
	return uint8(math.Round(min(max(v, 0), 255)))
}
