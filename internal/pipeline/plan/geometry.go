package plan

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Integer helpers with imgproxy's rounding (imath): Round is half away
// from zero, RoundToEven is banker's rounding.

func minNonZero(a, b int) int {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	}
	return min(a, b)
}

func nonZero(a, b float64) float64 {
	if a != 0 {
		return a
	}
	return b
}

func roundInt(f float64) int        { return int(math.Round(f)) }
func roundEvenInt(f float64) int    { return int(math.RoundToEven(f)) }
func scaleDim(a int, s float64) int { return scaleWith(a, s, math.Round) }

func scaleDimEven(a int, s float64) int { return scaleWith(a, s, math.RoundToEven) }

func scaleWith(a int, s float64, round func(float64) float64) int {
	if a == 0 {
		return 0
	}
	return int(round(float64(a) * s))
}

func shrinkDim(a int, s float64) int {
	if a == 0 {
		return 0
	}
	return roundInt(float64(a) / s)
}

func shrinkDimEven(a int, s float64) int {
	if a == 0 {
		return 0
	}
	return roundEvenInt(float64(a) / s)
}

// position is imgproxy's calcPosition without overflow: where an inner
// innerW×innerH box goes inside width×height.
func position(width, height, innerW, innerH int, g Gravity, dpr float64) (left, top int) {
	if g.Type == transform.GravityFocusPoint {
		left = scaleDimEven(width, g.X) - innerW/2
		top = scaleDimEven(height, g.Y) - innerH/2
	} else {
		offX := offset(width, g.X, dpr)
		offY := offset(height, g.Y, dpr)

		left = shrinkDimEven(width-innerW+1, 2) + offX
		top = shrinkDimEven(height-innerH+1, 2) + offY

		switch g.Type {
		case transform.GravityNorth, transform.GravityNorthEast, transform.GravityNorthWest:
			top = offY
		case transform.GravitySouth, transform.GravitySouthEast, transform.GravitySouthWest:
			top = height - innerH - offY
		default:
		}
		switch g.Type {
		case transform.GravityEast, transform.GravityNorthEast, transform.GravitySouthEast:
			left = width - innerW - offX
		case transform.GravityWest, transform.GravityNorthWest, transform.GravitySouthWest:
			left = offX
		default:
		}
	}
	left = max(0, min(left, width-innerW))
	top = max(0, min(top, height-innerH))
	return left, top
}

// offset resolves an imgproxy gravity offset: |v| ≥ 1 is pixels scaled by
// dpr, smaller values are a fraction of size.
func offset(size int, v, dpr float64) int {
	if math.Abs(v) >= 1 {
		return roundEvenInt(v * dpr)
	}
	return scaleDimEven(size, v)
}

// rotateGravity maps compass gravities for a clockwise rotation by angle;
// flipGravityX/Y mirror them.
func rotateGravity(t transform.GravityType, angle int) transform.GravityType {
	for range angle / 90 {
		switch t {
		case transform.GravityNorth:
			t = transform.GravityWest
		case transform.GravityEast:
			t = transform.GravityNorth
		case transform.GravitySouth:
			t = transform.GravityEast
		case transform.GravityWest:
			t = transform.GravitySouth
		case transform.GravityNorthWest:
			t = transform.GravitySouthWest
		case transform.GravityNorthEast:
			t = transform.GravityNorthWest
		case transform.GravitySouthWest:
			t = transform.GravitySouthEast
		case transform.GravitySouthEast:
			t = transform.GravityNorthEast
		default:
			return t
		}
	}
	return t
}

func flipGravityX(t transform.GravityType) transform.GravityType {
	switch t {
	case transform.GravityEast:
		return transform.GravityWest
	case transform.GravityWest:
		return transform.GravityEast
	case transform.GravityNorthWest:
		return transform.GravityNorthEast
	case transform.GravityNorthEast:
		return transform.GravityNorthWest
	case transform.GravitySouthWest:
		return transform.GravitySouthEast
	case transform.GravitySouthEast:
		return transform.GravitySouthWest
	default:
		return t
	}
}

func flipGravityY(t transform.GravityType) transform.GravityType {
	switch t {
	case transform.GravityNorth:
		return transform.GravitySouth
	case transform.GravitySouth:
		return transform.GravityNorth
	case transform.GravityNorthWest:
		return transform.GravitySouthWest
	case transform.GravityNorthEast:
		return transform.GravitySouthEast
	case transform.GravitySouthWest:
		return transform.GravityNorthWest
	case transform.GravitySouthEast:
		return transform.GravityNorthEast
	default:
		return t
	}
}

// undoRotateFlip turns a gravity meant for the rotated and flipped image
// into the one that gives the same crop before rotating and flipping
// (imgproxy GravityOptions.RotateAndFlip).
func (g *Gravity) undoRotateFlip(angle int, flipX, flipY bool) {
	angle %= 360
	if flipX {
		g.Type = flipGravityX(g.Type)
		switch g.Type {
		case transform.GravityCenter, transform.GravityNorth, transform.GravitySouth:
			g.X = -g.X
		case transform.GravityFocusPoint:
			g.X = 1 - g.X
		default:
		}
	}
	if flipY {
		g.Type = flipGravityY(g.Type)
		switch g.Type {
		case transform.GravityCenter, transform.GravityEast, transform.GravityWest:
			g.Y = -g.Y
		case transform.GravityFocusPoint:
			g.Y = 1 - g.Y
		default:
		}
	}
	if angle != 90 && angle != 180 && angle != 270 {
		return
	}
	g.Type = rotateGravity(g.Type, angle)
	switch angle {
	case 90:
		switch g.Type {
		case transform.GravityCenter, transform.GravityEast, transform.GravityWest:
			g.X, g.Y = g.Y, -g.X
		case transform.GravityFocusPoint:
			g.X, g.Y = g.Y, 1-g.X
		default:
			g.X, g.Y = g.Y, g.X
		}
	case 180:
		switch g.Type {
		case transform.GravityCenter:
			g.X, g.Y = -g.X, -g.Y
		case transform.GravityNorth, transform.GravitySouth:
			g.X = -g.X
		case transform.GravityEast, transform.GravityWest:
			g.Y = -g.Y
		case transform.GravityFocusPoint:
			g.X, g.Y = 1-g.X, 1-g.Y
		default:
		}
	case 270:
		switch g.Type {
		case transform.GravityCenter, transform.GravityNorth, transform.GravitySouth:
			g.X, g.Y = -g.Y, g.X
		case transform.GravityFocusPoint:
			g.X, g.Y = 1-g.Y, g.X
		default:
			g.X, g.Y = g.Y, g.X
		}
	}
}

// resolveGravity converts a request gravity. Content-aware types report a
// smart-crop strategy; face and object detection (M3) fall back to
// attention.
func resolveGravity(g *transform.Gravity) (Gravity, Interesting) {
	switch g.Type {
	case transform.GravityFocusPoint:
		return Gravity{Type: g.Type, X: g.FocusX, Y: g.FocusY}, InterestingNone
	case transform.GravitySmart, transform.GravityFace, transform.GravityObject, transform.GravityObjectWeighted:
		return Gravity{Type: transform.GravityCenter}, InterestingAttention
	case transform.GravityEntropy:
		return Gravity{Type: transform.GravityCenter}, InterestingEntropy
	case transform.GravityUnset:
		return Gravity{Type: transform.GravityCenter, X: g.XOffset, Y: g.YOffset}, InterestingNone
	default:
		return Gravity{Type: g.Type, X: g.XOffset, Y: g.YOffset}, InterestingNone
	}
}

// orientation returns the EXIF rotation and flip (imgproxy angleFlip).
func orientation(o int, autoRotate bool) (angle int, flip bool) {
	if !autoRotate {
		return 0, false
	}
	switch o {
	case 3, 4:
		angle = 180
	case 5, 6:
		angle = 90
	case 7, 8:
		angle = 270
	}
	return angle, o == 2 || o == 4 || o == 5 || o == 7
}

// cropSize is imgproxy's CalcCropSize for a transform.Dim.
func cropSize(orig int, d transform.Dim) int {
	switch {
	case d.Px > 0:
		return d.Px
	case d.Rel >= 1:
		return orig
	case d.Rel > 0:
		return max(1, scaleDim(orig, d.Rel))
	}
	return 0
}

// resizedDim predicts libvips vips_resize's output size on one axis.
func resizedDim(n int, s float64) int {
	switch {
	case s == 1:
		return n
	case s < 1:
		// reduce: VIPS_ROUND_UINT(in / (1/scale)); never below 1 px.
		s = max(s, 1/float64(n))
		return max(1, int(float64(n)/(1/s)+0.5))
	default:
		return max(1, roundInt(float64(n)*s))
	}
}
