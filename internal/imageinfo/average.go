package imageinfo

// Average is the mean colour of m (imgproxy info `average`): the rounded
// mean of each channel, alpha included. With ignoreTransparent, fully
// transparent pixels (alpha 0) are left out; an image with no other
// pixels averages to transparent black.
func Average(m *Image, ignoreTransparent bool) (Color, error) {
	if err := m.Validate(); err != nil {
		return Color{}, err
	}
	var sum [4]uint64
	var n uint64
	for p := range m.Width * m.Height {
		px := m.Pix[p*4 : p*4+4 : p*4+4]
		if ignoreTransparent && px[3] == 0 {
			continue
		}
		sum[0] += uint64(px[0])
		sum[1] += uint64(px[1])
		sum[2] += uint64(px[2])
		sum[3] += uint64(px[3])
		n++
	}
	if n == 0 {
		return Color{}, nil
	}
	return Color{R: meanByte(sum[0], n), G: meanByte(sum[1], n), B: meanByte(sum[2], n), A: meanByte(sum[3], n)}, nil
}

// meanByte is sum/n rounded half up; sum/n ≤ 255 by construction.
func meanByte(sum, n uint64) uint8 {
	return uint8((2*sum + n) / (2 * n)) //nolint:gosec // G115: a mean of bytes fits in a byte
}
