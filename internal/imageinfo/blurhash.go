// BlurHash encoder: a Go port of the reference C encoder of
// https://github.com/woltapp/blurhash (C/encode.c, C/common.h),
// Copyright (c) 2018 Wolt Enterprises, MIT License. Computed in float64
// instead of float32; the reference vectors in testdata/blurhash match.

package imageinfo

import (
	"errors"
	"math"
)

// MaxBlurhashComponents is the largest component count per axis.
const MaxBlurhashComponents = 9

// ErrBlurhashComponents is returned for component counts outside 1..9.
var ErrBlurhashComponents = errors.New("imageinfo: blurhash components must be 1 to 9")

// base83 is the BlurHash alphabet.
const base83 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~"

// Blurhash encodes m with xc×yc components (imgproxy info `blurhash`).
// Alpha is ignored, like the reference encoder, which reads RGB only.
func Blurhash(m *Image, xc, yc int) (string, error) {
	if xc < 1 || xc > MaxBlurhashComponents || yc < 1 || yc > MaxBlurhashComponents {
		return "", ErrBlurhashComponents
	}
	if err := m.Validate(); err != nil {
		return "", err
	}
	w, h := m.Width, m.Height
	var lut [256]float64
	for i := range lut {
		lut[i] = srgbToLinear(i)
	}
	// Separable DCT: per horizontal component i, the cosine-weighted sum
	// of every row (rows[i][y]), then per vertical component j the
	// cosine-weighted sum of those. O(xc·w·h + xc·yc·h) instead of
	// O(xc·yc·w·h); the reference vectors still match exactly.
	rows := make([][3]float64, xc*h)
	cosX := make([]float64, w)
	for i := range xc {
		for x := range w {
			cosX[x] = math.Cos(math.Pi * float64(i) * float64(x) / float64(w))
		}
		for y := range h {
			px := m.Pix[y*w*4 : (y+1)*w*4]
			var r, g, b float64
			for x := range w {
				r += float64(cosX[x] * lut[px[x*4]])
				g += float64(cosX[x] * lut[px[x*4+1]])
				b += float64(cosX[x] * lut[px[x*4+2]])
			}
			rows[i*h+y] = [3]float64{r, g, b}
		}
	}
	cosY := make([]float64, h)
	factors := make([][3]float64, 0, xc*yc)
	for j := range yc {
		for y := range h {
			cosY[y] = math.Cos(math.Pi * float64(j) * float64(y) / float64(h))
		}
		for i := range xc {
			var r, g, b float64
			for y := range h {
				s := &rows[i*h+y]
				r += float64(cosY[y] * s[0])
				g += float64(cosY[y] * s[1])
				b += float64(cosY[y] * s[2])
			}
			norm := 2.0
			if i == 0 && j == 0 {
				norm = 1
			}
			scale := norm / float64(w*h)
			factors = append(factors, [3]float64{r * scale, g * scale, b * scale})
		}
	}

	out := make([]byte, 0, 4+2*len(factors))
	out = appendBase83(out, (xc-1)+(yc-1)*9, 1)
	maxValue := 1.0
	if len(factors) > 1 {
		actual := 0.0
		for _, f := range factors[1:] {
			actual = max(actual, math.Abs(f[0]), math.Abs(f[1]), math.Abs(f[2]))
		}
		q := int(max(0, min(82, math.Floor(float64(actual*166)-0.5))))
		maxValue = float64(q+1) / 166
		out = appendBase83(out, q, 1)
	} else {
		out = appendBase83(out, 0, 1)
	}
	dc := factors[0]
	out = appendBase83(out, linearToSRGB(dc[0])<<16|linearToSRGB(dc[1])<<8|linearToSRGB(dc[2]), 4)
	for _, f := range factors[1:] {
		out = appendBase83(out, quantAC(f[0], maxValue)*19*19+quantAC(f[1], maxValue)*19+quantAC(f[2], maxValue), 2)
	}
	return string(out), nil
}

func quantAC(v, maxValue float64) int {
	return int(max(0, min(18, math.Floor(float64(signPow(v/maxValue, 0.5)*9)+9.5))))
}

func signPow(v, exp float64) float64 {
	return math.Copysign(math.Pow(math.Abs(v), exp), v)
}

func srgbToLinear(v int) float64 {
	f := float64(v) / 255
	if f <= 0.04045 {
		return f / 12.92
	}
	return math.Pow((f+0.055)/1.055, 2.4)
}

func linearToSRGB(v float64) int {
	v = max(0, min(1, v))
	if v <= 0.0031308 {
		return int(float64(v*12.92*255) + 0.5)
	}
	return int(float64((float64(1.055*math.Pow(v, 1/2.4))-0.055)*255) + 0.5)
}

func appendBase83(b []byte, v, length int) []byte {
	div := 1
	for range length - 1 {
		div *= 83
	}
	for range length {
		b = append(b, base83[(v/div)%83])
		div /= 83
	}
	return b
}
