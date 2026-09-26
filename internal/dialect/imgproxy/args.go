// Copyright 2017 Sergey Alexandrovich (imgproxy), Apache License 2.0.
// Argument rules reimplemented from imgproxy options/parser/parse.go.
// Modifications copyright xtandard-image contributors, MIT. See NOTICE.

package imgproxy

import (
	"math"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Expectation phrases for error messages (client-safe, static).
const (
	expBool         = "boolean (1, t, true, 0, f, false)"
	expInt          = "integer"
	expNonNegInt    = "integer >= 0"
	expPositiveInt  = "integer > 0"
	expFloat        = "number"
	expNonNegFloat  = "number >= 0"
	expPositive     = "number > 0"
	expUnit         = "number in range 0-1"
	expQuality      = "integer in range 0-100"
	expByte         = "integer in range 0-255"
	expHexColor     = "hex color (rgb or rrggbb)"
	expFormat       = "supported image format"
	expResizeType   = "fit, fill, fill-down, force or auto"
	expAlgorithm    = "nearest, linear, cubic, lanczos2 or lanczos3"
	expRotate       = "multiple of 90"
	expBase64       = "url-safe base64 string"
	expNonEmpty     = "non-empty string"
	expPages        = "integer > 0 or -1"
	expMaxArgs      = "at most "
	expArgs         = " arguments"
	expGravityCrop  = "ce, no, so, ea, we, noea, nowe, soea, sowe, sm, fp, obj or objw"
	expGravityExt   = "ce, no, so, ea, we, noea, nowe, soea, sowe or fp"
	expWatermarkPos = "ce, no, so, ea, we, noea, nowe, soea, sowe, re or ch" //nolint:gosec // G101 false positive: an error message, not a credential
	expEvenPairs    = "format:quality pairs"
	expDimension    = "number >= 0 below 2^31"
)

// maxArgs returns a 400 when args has more than n elements.
func maxArgs(field string, args []string, n int) error {
	if len(args) > n {
		return dialect.InvalidArgument(field, joinArgs(args), expMaxArgs+strconv.Itoa(n)+expArgs)
	}
	return nil
}

func joinArgs(args []string) string {
	n := 0
	for _, a := range args {
		n += len(a) + 1
	}
	b := make([]byte, 0, n)
	for i, a := range args {
		if i > 0 {
			b = append(b, ':')
		}
		b = append(b, a...)
	}
	return string(b)
}

// parseBool follows strconv.ParseBool; "" is false as in imgproxy.
func parseBool(field, s string) (bool, error) {
	if s == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, dialect.InvalidArgument(field, s, expBool)
	}
	return b, nil
}

func parseInt(field, s string) (int, error) {
	i, err := strconv.Atoi(s)
	if err != nil || i < math.MinInt32 || i > math.MaxInt32 {
		return 0, dialect.InvalidArgument(field, s, expInt)
	}
	return i, nil
}

func parseNonNegInt(field, s string) (int, error) {
	i, err := strconv.Atoi(s)
	if err != nil || i < 0 || i > math.MaxInt32 {
		return 0, dialect.InvalidArgument(field, s, expNonNegInt)
	}
	return i, nil
}

func parsePositiveInt(field, s string) (int, error) {
	i, err := strconv.Atoi(s)
	if err != nil || i <= 0 || i > math.MaxInt32 {
		return 0, dialect.InvalidArgument(field, s, expPositiveInt)
	}
	return i, nil
}

func parseFloatRaw(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func parseFloat(field, s string) (float64, error) {
	f, ok := parseFloatRaw(s)
	if !ok {
		return 0, dialect.InvalidArgument(field, s, expFloat)
	}
	return f, nil
}

func parseNonNegFloat(field, s string) (float64, error) {
	f, ok := parseFloatRaw(s)
	if !ok || f < 0 {
		return 0, dialect.InvalidArgument(field, s, expNonNegFloat)
	}
	return f, nil
}

func parsePositiveFloat(field, s string) (float64, error) {
	f, ok := parseFloatRaw(s)
	if !ok || f <= 0 {
		return 0, dialect.InvalidArgument(field, s, expPositive)
	}
	return f, nil
}

func parseUnitFloat(field, s string) (float64, error) {
	f, ok := parseFloatRaw(s)
	if !ok || f < 0 || f > 1 {
		return 0, dialect.InvalidArgument(field, s, expUnit)
	}
	return f, nil
}

func parseQuality(field, s string) (int, error) {
	i, err := strconv.Atoi(s)
	if err != nil || i < 0 || i > 100 {
		return 0, dialect.InvalidArgument(field, s, expQuality)
	}
	return i, nil
}

// parseDim parses imgproxy's crop size: 0 = full, >= 1 absolute pixels
// (truncated, imgproxy processing.CalcCropSize), < 1 relative.
func parseDim(field, s string) (transform.Dim, error) {
	f, ok := parseFloatRaw(s)
	if !ok || f < 0 || f >= math.MaxInt32 {
		return transform.Dim{}, dialect.InvalidArgument(field, s, expDimension)
	}
	if f >= 1 {
		return transform.Dim{Px: int(f)}, nil
	}
	return transform.Dim{Rel: f}, nil
}

// parseHexColor accepts "rgb" and "rrggbb" (imgproxy color.RGBFromHex).
func parseHexColor(field, s string) (transform.Color, error) {
	var v [6]byte
	switch len(s) {
	case 3:
		for i := range 3 {
			h, ok := hexNibble(s[i])
			if !ok {
				return transform.Color{}, dialect.InvalidArgument(field, s, expHexColor)
			}
			v[2*i], v[2*i+1] = h, h
		}
	case 6:
		for i := range 6 {
			h, ok := hexNibble(s[i])
			if !ok {
				return transform.Color{}, dialect.InvalidArgument(field, s, expHexColor)
			}
			v[i] = h
		}
	default:
		return transform.Color{}, dialect.InvalidArgument(field, s, expHexColor)
	}
	return transform.Color{R: v[0]<<4 | v[1], G: v[2]<<4 | v[3], B: v[4]<<4 | v[5], A: 255}, nil
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func parseByte(field, s string) (uint8, error) {
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return 0, dialect.InvalidArgument(field, s, expByte)
	}
	return uint8(v), nil
}

// parseFormat maps an imgproxy image type name to a Format. best is
// accepted where allowBest is set.
func parseFormat(field, s string, allowBest bool) (transform.Format, error) {
	switch s {
	case "jpeg", "jpg":
		return transform.FormatJPEG, nil
	case "png":
		return transform.FormatPNG, nil
	case "webp":
		return transform.FormatWebP, nil
	case "avif":
		return transform.FormatAVIF, nil
	case "jxl":
		return transform.FormatJXL, nil
	case "heic":
		return transform.FormatHEIC, nil
	case "gif":
		return transform.FormatGIF, nil
	case "tiff":
		return transform.FormatTIFF, nil
	case "bmp":
		return transform.FormatBMP, nil
	case "ico":
		return transform.FormatICO, nil
	case "svg":
		return transform.FormatSVG, nil
	case "best":
		if allowBest {
			return transform.FormatBest, nil
		}
	}
	return transform.FormatUnset, dialect.InvalidArgument(field, s, expFormat)
}

var resizingTypes = map[string]transform.ResizeType{
	"fit":       transform.ResizeFit,
	"fill":      transform.ResizeFill,
	"fill-down": transform.ResizeFillDown,
	"force":     transform.ResizeForce,
	"auto":      transform.ResizeAuto,
}

func isResizingType(s string) bool {
	_, ok := resizingTypes[s]
	return ok
}

var kernels = map[string]transform.Kernel{
	"nearest":  transform.KernelNearest,
	"linear":   transform.KernelLinear,
	"cubic":    transform.KernelCubic,
	"lanczos2": transform.KernelLanczos2,
	"lanczos3": transform.KernelLanczos3,
}

var compass = map[string]transform.GravityType{
	"ce":   transform.GravityCenter,
	"no":   transform.GravityNorth,
	"so":   transform.GravitySouth,
	"ea":   transform.GravityEast,
	"we":   transform.GravityWest,
	"noea": transform.GravityNorthEast,
	"nowe": transform.GravityNorthWest,
	"soea": transform.GravitySouthEast,
	"sowe": transform.GravitySouthWest,
}

// gravityKind selects which gravity types an option accepts (imgproxy
// CropGravityTypes, ExtendGravityTypes).
type gravityKind uint8

const (
	gravityCrop   gravityKind = iota // compass, sm, fp, obj, objw
	gravityExtend                    // compass, fp
)

// parseGravity applies "type[:args...]" to g, keeping unspecified offsets
// as imgproxy does. field prefixes error field names. It reports the
// milestone of the parsed type ("" for M1).
func parseGravity(g *transform.Gravity, field string, kind gravityKind, args []string) (string, error) {
	t := args[0]
	if gt, ok := compass[t]; ok {
		if err := maxArgs(field, args, 3); err != nil {
			return "", err
		}
		ng := transform.Gravity{Type: gt, XOffset: g.XOffset, YOffset: g.YOffset}
		if len(args) > 1 && args[1] != "" {
			x, err := parseFloat(field+".x_offset", args[1])
			if err != nil {
				return "", err
			}
			ng.XOffset = x
		}
		if len(args) > 2 && args[2] != "" {
			y, err := parseFloat(field+".y_offset", args[2])
			if err != nil {
				return "", err
			}
			ng.YOffset = y
		}
		*g = ng
		return "", nil
	}
	expected := expGravityCrop
	if kind == gravityExtend {
		expected = expGravityExt
	}
	switch {
	case t == "fp":
		if len(args) != 3 {
			return "", dialect.InvalidArgument(field, joinArgs(args), "fp:x:y")
		}
		x, err := parseUnitFloat(field+".x", args[1])
		if err != nil {
			return "", err
		}
		y, err := parseUnitFloat(field+".y", args[2])
		if err != nil {
			return "", err
		}
		*g = transform.Gravity{Type: transform.GravityFocusPoint, FocusX: x, FocusY: y}
		return "", nil
	case t == "sm" && kind == gravityCrop:
		if len(args) > 1 {
			return "", dialect.InvalidArgument(field, joinArgs(args), "sm without arguments")
		}
		*g = transform.Gravity{Type: transform.GravitySmart}
		return "", nil
	case t == "obj" && kind == gravityCrop:
		ng := transform.Gravity{Type: transform.GravityObject}
		for _, c := range args[1:] {
			if c == "" {
				return "", dialect.InvalidArgument(field+".classes", c, expNonEmpty)
			}
			ng.Classes = append(ng.Classes, c)
		}
		*g = ng
		return "M3", nil
	case t == "objw" && kind == gravityCrop:
		rest := args[1:]
		if len(rest) == 0 || len(rest)%2 != 0 {
			return "", dialect.InvalidArgument(field, joinArgs(args), "objw:class:weight pairs")
		}
		ng := transform.Gravity{Type: transform.GravityObjectWeighted}
		for i := 0; i < len(rest); i += 2 {
			if rest[i] == "" {
				return "", dialect.InvalidArgument(field+".classes", rest[i], expNonEmpty)
			}
			w, err := parseNonNegFloat(field+".weights", rest[i+1])
			if err != nil {
				return "", err
			}
			ng.Classes = append(ng.Classes, rest[i])
			ng.Weights = append(ng.Weights, w)
		}
		*g = ng
		return "M3", nil
	}
	return "", dialect.InvalidArgument(field+".type", t, expected)
}
