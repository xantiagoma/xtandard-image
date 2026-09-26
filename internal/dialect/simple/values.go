package simple

import (
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Expectation phrases (client-safe, static).
const (
	expDim     = "number 0..65536"
	expQuality = "integer 0..100"
	expFactor  = "number > 0"
	expSigma   = "number 0..1000"
	expBool    = "1, 0, true or false"
	expColor   = "hex colour (rgb, rrggbb or aarrggbb)"
	expAngle   = "degrees"
	expFormat  = "an image format"
	expFit     = "a fit mode of the provider"
	expGravity = "a position of the provider"
)

// Unsupported is a documented parameter xi does not apply.
func Unsupported() Param { return Param{} }

// Silent is a parameter accepted without effect.
func Silent() Param { return Param{Silent: true} }

// KeepInSource is a parameter that belongs to the source reference.
func KeepInSource() Param { return Param{Source: true} }

// Func wraps a mapping function.
func Func(fn func(st *State, value string) error) Param { return Param{Apply: fn} }

// parseNumber parses a finite decimal number.
func parseNumber(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// parseDim parses a pixel dimension: a non-negative number, rounded (unpic
// rounds fractional sizes); 0 or "" is auto.
func parseDim(field, s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	f, ok := parseNumber(s)
	if !ok || f < 0 || f > transform.MaxDimension {
		return 0, dialect.InvalidArgument(field, s, expDim)
	}
	return int(math.Round(f)), nil
}

// Width sets the target width.
func Width(field string) Param {
	return Func(func(st *State, v string) error {
		w, err := parseDim(field, v)
		st.Width = w
		return err
	})
}

// Height sets the target height.
func Height(field string) Param {
	return Func(func(st *State, v string) error {
		h, err := parseDim(field, v)
		st.Height = h
		return err
	})
}

// parseQuality parses 0..100 (0 = the format default); decimals round.
func parseQuality(field, s string) (int, error) {
	f, ok := parseNumber(s)
	if !ok || f < 0 || f > transform.MaxQuality {
		return 0, dialect.InvalidArgument(field, s, expQuality)
	}
	return int(math.Round(f)), nil
}

// Quality sets the output quality.
func Quality(field string) Param {
	return Func(func(st *State, v string) error {
		q, err := parseQuality(field, v)
		st.Request.Output.Quality = q
		return err
	})
}

// DPR sets the device pixel ratio.
func DPR(field string) Param {
	return Func(func(st *State, v string) error {
		f, ok := parseNumber(v)
		if !ok || f <= 0 || f > transform.MaxFactor {
			return dialect.InvalidArgument(field, v, expFactor)
		}
		st.Pipeline.Resize.DPR = f
		return nil
	})
}

// formatAuto lists the values that mean "the best format for the client"
// or "keep the source format": both leave the format to negotiation.
func formatAuto(s string) bool {
	switch s {
	case "", "auto", "origin", "original", "preserve", "source":
		return true
	}
	return false
}

// parseFormat maps the common format vocabulary. ok is false for a value
// that names no format xi encodes (jp2, eps, blurhash, ...).
func parseFormat(s string, out *transform.Output) bool {
	switch strings.ToLower(s) {
	case "jpg", "jpeg", "jpe":
		out.Format = transform.FormatJPEG
	case "pjpg", "pjpeg", "progressive-jpeg":
		out.Format = transform.FormatJPEG
		out.Progressive = new(true)
	case "png", "png24", "png32", "png-alpha":
		out.Format = transform.FormatPNG
	case "png8":
		out.Format = transform.FormatPNG
		out.PNG = &transform.PNGOptions{Quantize: new(true)}
	case "webp", "webp-alpha", "webply":
		out.Format = transform.FormatWebP
	case "webpll":
		out.Format = transform.FormatWebP
		out.Lossless = new(true)
	case "avif", "avif-alpha":
		out.Format = transform.FormatAVIF
	case "gif", "gif-alpha":
		out.Format = transform.FormatGIF
	case "jxl":
		out.Format = transform.FormatJXL
	case "heic", "heif":
		out.Format = transform.FormatHEIC
	case "tif", "tiff":
		out.Format = transform.FormatTIFF
	case "bmp":
		out.Format = transform.FormatBMP
	default:
		if !formatAuto(strings.ToLower(s)) {
			return false
		}
		out.Format = transform.FormatUnset
	}
	return true
}

// Format sets the output format. A format xi does not encode is recorded
// as ignored (the source format or negotiation applies).
func Format(field string) Param {
	return Func(func(st *State, v string) error {
		if !parseFormat(v, &st.Request.Output) {
			if strings.ContainsFunc(v, func(r rune) bool { return !isWordRune(r) }) {
				return dialect.InvalidArgument(field, v, expFormat)
			}
			st.Ignore(field, "unsupported format")
		}
		return nil
	})
}

func isWordRune(r rune) bool {
	return r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// FitMap sets the fit mode from the provider's vocabulary.
func FitMap(field string, m map[string]Fit) Param {
	return Func(func(st *State, v string) error {
		f, ok := m[strings.ToLower(v)]
		if !ok {
			return dialect.InvalidArgument(field, v, expFit)
		}
		st.Fit = f
		return nil
	})
}

// gravityWords maps position words to compass bits.
const (
	gN = 1 << iota
	gS
	gE
	gW
	gC
)

// parseGravity maps the common position vocabulary: compass words in any
// order and separator ("top-left", "left top", "top_left", "topleft",
// "northwest", "nw"), center, face(s), smart/auto/attention and entropy.
func parseGravity(s string) (transform.GravityType, bool) {
	l := strings.ToLower(strings.TrimSpace(s))
	switch l {
	case "face", "faces":
		return transform.GravityFace, true
	case "smart", "auto", "attention":
		return transform.GravitySmart, true
	case "entropy":
		return transform.GravityEntropy, true
	case "nw":
		return transform.GravityNorthWest, true
	case "ne":
		return transform.GravityNorthEast, true
	case "sw":
		return transform.GravitySouthWest, true
	case "se":
		return transform.GravitySouthEast, true
	}
	bits := 0
	for w := range strings.FieldsFuncSeq(l, func(r rune) bool { return r == '-' || r == '_' || r == ' ' || r == ',' }) {
		for w != "" {
			var n int
			switch {
			case strings.HasPrefix(w, "north"):
				bits, n = bits|gN, 5
			case strings.HasPrefix(w, "south"):
				bits, n = bits|gS, 5
			case strings.HasPrefix(w, "east"):
				bits, n = bits|gE, 4
			case strings.HasPrefix(w, "west"):
				bits, n = bits|gW, 4
			case strings.HasPrefix(w, "top"):
				bits, n = bits|gN, 3
			case strings.HasPrefix(w, "bottom"):
				bits, n = bits|gS, 6
			case strings.HasPrefix(w, "left"):
				bits, n = bits|gW, 4
			case strings.HasPrefix(w, "right"):
				bits, n = bits|gE, 5
			case strings.HasPrefix(w, "center"), strings.HasPrefix(w, "centre"), strings.HasPrefix(w, "middle"):
				bits, n = bits|gC, 6
			case w == "n" || w == "t":
				bits, n = bits|gN, 1
			case w == "s" || w == "b":
				bits, n = bits|gS, 1
			case w == "e" || w == "r":
				bits, n = bits|gE, 1
			case w == "w" || w == "l":
				bits, n = bits|gW, 1
			case w == "c":
				bits, n = bits|gC, 1
			default:
				return transform.GravityUnset, false
			}
			w = w[n:]
		}
	}
	switch bits &^ gC {
	case 0:
		if bits == gC {
			return transform.GravityCenter, true
		}
	case gN:
		return transform.GravityNorth, true
	case gS:
		return transform.GravitySouth, true
	case gE:
		return transform.GravityEast, true
	case gW:
		return transform.GravityWest, true
	case gN | gE:
		return transform.GravityNorthEast, true
	case gN | gW:
		return transform.GravityNorthWest, true
	case gS | gE:
		return transform.GravitySouthEast, true
	case gS | gW:
		return transform.GravitySouthWest, true
	}
	return transform.GravityUnset, false
}

// Gravity sets the crop anchor from the common position vocabulary.
func Gravity(field string) Param {
	return Func(func(st *State, v string) error {
		g, ok := parseGravity(v)
		if !ok {
			return dialect.InvalidArgument(field, v, expGravity)
		}
		st.Pipeline.Gravity = transform.Gravity{Type: g}
		return nil
	})
}

// FocusPoint sets a focal point from a relative coordinate (0..1).
// axis is 'x' or 'y'; the other coordinate defaults to 0.5.
func FocusPoint(field string, axis byte) Param {
	return Func(func(st *State, v string) error {
		f, ok := parseNumber(v)
		if !ok || f < 0 || f > 1 {
			return dialect.InvalidArgument(field, v, "number 0..1")
		}
		g := &st.Pipeline.Gravity
		if g.Type != transform.GravityFocusPoint {
			*g = transform.Gravity{Type: transform.GravityFocusPoint, FocusX: 0.5, FocusY: 0.5}
		}
		if axis == 'x' {
			g.FocusX = f
		} else {
			g.FocusY = f
		}
		return nil
	})
}

// Blur sets a Gaussian blur; scale converts the provider's unit to sigma.
func Blur(field string, scale float64) Param {
	return Func(func(st *State, v string) error {
		f, ok := parseNumber(v)
		if !ok || f < 0 || f*scale > transform.MaxSigma {
			return dialect.InvalidArgument(field, v, expSigma)
		}
		st.Pipeline.Blur = f * scale
		return nil
	})
}

// Sharpen sets sharpening; scale converts the provider's unit to sigma.
func Sharpen(field string, scale float64) Param {
	return Func(func(st *State, v string) error {
		f, ok := parseNumber(v)
		if !ok || f < 0 || f*scale > transform.MaxSigma {
			return dialect.InvalidArgument(field, v, expSigma)
		}
		st.Pipeline.Sharpen = f * scale
		return nil
	})
}

// setRotate applies a clockwise rotation in degrees: multiples of 90 go
// to Rotate, anything else to RotateAny.
func setRotate(st *State, deg float64) {
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	if deg == math.Trunc(deg) && int(deg)%90 == 0 {
		st.Pipeline.Rotate, st.Pipeline.RotateAny = int(deg), 0
		return
	}
	st.Pipeline.Rotate, st.Pipeline.RotateAny = 0, deg
}

// Rotate rotates by degrees; clockwise false means the provider counts
// counter-clockwise.
func Rotate(field string, clockwise bool) Param {
	return Func(func(st *State, v string) error {
		f, ok := parseNumber(v)
		if !ok || math.Abs(f) > 3600 {
			return dialect.InvalidArgument(field, v, expAngle)
		}
		if !clockwise {
			f = -f
		}
		setRotate(st, f)
		return nil
	})
}

// parseBool accepts 1/0, true/false, yes/no, on/off; "" is true (a bare
// flag).
func parseBool(s string) (v, ok bool) {
	switch strings.ToLower(s) {
	case "", "1", "true", "yes", "on", "t", "y":
		return true, true
	case "0", "false", "no", "off", "f", "n":
		return false, true
	}
	return false, false
}

// Bool maps a boolean flag.
func Bool(field string, fn func(st *State, v bool)) Param {
	return Func(func(st *State, s string) error {
		v, ok := parseBool(s)
		if !ok {
			return dialect.InvalidArgument(field, s, expBool)
		}
		fn(st, v)
		return nil
	})
}

// Enlarge maps a flag that allows (or, inverted, forbids) upscaling.
func Enlarge(field string, invert bool) Param {
	return Bool(field, func(st *State, v bool) { st.SetEnlarge(v != invert) })
}

// Grayscale maps a grayscale flag.
func Grayscale(field string) Param {
	return Bool(field, func(st *State, v bool) { st.Pipeline.Grayscale = v })
}

// FlipH / FlipV map mirror flags.
func FlipH(field string) Param {
	return Bool(field, func(st *State, v bool) { st.Pipeline.Flip.H = v })
}

// FlipV maps a vertical flip flag.
func FlipV(field string) Param {
	return Bool(field, func(st *State, v bool) { st.Pipeline.Flip.V = v })
}

// Lossless maps a lossless-encoding flag.
func Lossless(field string) Param {
	return Bool(field, func(st *State, v bool) { st.Request.Output.Lossless = &v })
}

// Progressive maps a progressive-encoding flag.
func Progressive(field string) Param {
	return Bool(field, func(st *State, v bool) { st.Request.Output.Progressive = &v })
}

// parseColor parses "rgb", "rrggbb", "aarrggbb" (alpha first, the
// Kontent.ai/imgix order) or "rrggbbaa" when argbFirst is false, with an
// optional "#" or "rgb:" prefix.
func parseColor(s string, argbFirst bool) (transform.Color, bool) {
	s = strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(s), "rgb:"), "#")
	var b [8]uint8
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			c -= '0'
		case c >= 'a' && c <= 'f':
			c = c - 'a' + 10
		default:
			return transform.Color{}, false
		}
		if i >= len(b) {
			return transform.Color{}, false
		}
		b[i] = c
	}
	switch len(s) {
	case 3:
		return transform.Color{R: b[0] * 17, G: b[1] * 17, B: b[2] * 17, A: 255}, true
	case 6:
		return transform.Color{R: b[0]<<4 | b[1], G: b[2]<<4 | b[3], B: b[4]<<4 | b[5], A: 255}, true
	case 8:
		if argbFirst {
			return transform.Color{A: b[0]<<4 | b[1], R: b[2]<<4 | b[3], G: b[4]<<4 | b[5], B: b[6]<<4 | b[7]}, true
		}
		return transform.Color{R: b[0]<<4 | b[1], G: b[2]<<4 | b[3], B: b[4]<<4 | b[5], A: b[6]<<4 | b[7]}, true
	}
	return transform.Color{}, false
}

// Background sets the background (padding, flattening) colour.
func Background(field string, argbFirst bool) Param {
	return Func(func(st *State, v string) error {
		c, ok := parseColor(v, argbFirst)
		if !ok {
			return dialect.InvalidArgument(field, v, expColor)
		}
		st.Pipeline.Background = &c
		return nil
	})
}
