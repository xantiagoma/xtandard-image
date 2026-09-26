package cloudflare

import (
	"encoding/binary"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const expColor = "css color: #rgb, #rrggbb, #rrggbbaa, a css name, rgb() or rgba()"

// parseColor parses the CSS colours Cloudflare accepts for background,
// border and trim.border.color: "#rgb", "#rgba", "#rrggbb", "#rrggbbaa",
// named colours (and "transparent"), and rgb()/rgba() with comma or
// space separated channels (0–255 or percentages) and an optional alpha
// (0–1 or a percentage, after "," or "/"). Names are case-insensitive.
func parseColor(field, s string) (transform.Color, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch {
	case v == "transparent":
		return transform.Color{}, nil
	case strings.HasPrefix(v, "#"):
		if c, ok := hexColor(v[1:]); ok {
			return c, nil
		}
	case strings.HasPrefix(v, "rgb(") || strings.HasPrefix(v, "rgba("):
		if c, ok := rgbFunc(v); ok {
			return c, nil
		}
	default:
		if n, ok := namedColor(v); ok {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], n)
			return transform.Color{R: b[1], G: b[2], B: b[3], A: 255}, nil
		}
	}
	return transform.Color{}, dialect.InvalidArgument(field, s, expColor)
}

func hexColor(h string) (transform.Color, bool) {
	var nib [8]byte
	if len(h) != 3 && len(h) != 4 && len(h) != 6 && len(h) != 8 {
		return transform.Color{}, false
	}
	for i := range len(h) {
		n, ok := hexNibble(h[i])
		if !ok {
			return transform.Color{}, false
		}
		nib[i] = n
	}
	c := transform.Color{A: 255}
	switch len(h) {
	case 3, 4:
		c.R, c.G, c.B = nib[0]*17, nib[1]*17, nib[2]*17
		if len(h) == 4 {
			c.A = nib[3] * 17
		}
	default:
		c.R, c.G, c.B = nib[0]<<4|nib[1], nib[2]<<4|nib[3], nib[4]<<4|nib[5]
		if len(h) == 8 {
			c.A = nib[6]<<4 | nib[7]
		}
	}
	return c, true
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// rgbFunc parses rgb(r,g,b), rgba(r,g,b,a), rgb(r g b / a) (lower-case).
func rgbFunc(v string) (transform.Color, bool) {
	open := strings.IndexByte(v, '(')
	if !strings.HasSuffix(v, ")") {
		return transform.Color{}, false
	}
	body := v[open+1 : len(v)-1]
	var alpha string
	if main, a, ok := strings.Cut(body, "/"); ok {
		body, alpha = main, a
	}
	parts := strings.FieldsFunc(body, func(r rune) bool { return r == ',' || r == ' ' })
	if len(parts) == 4 && alpha == "" {
		alpha, parts = parts[3], parts[:3]
	}
	if len(parts) != 3 {
		return transform.Color{}, false
	}
	c := transform.Color{A: 255}
	for i, dst := range []*uint8{&c.R, &c.G, &c.B} {
		n, ok := channel(parts[i], 255)
		if !ok {
			return transform.Color{}, false
		}
		*dst = n
	}
	if a := strings.TrimSpace(alpha); a != "" {
		n, ok := channel(a, 1)
		if !ok {
			return transform.Color{}, false
		}
		c.A = n
	}
	return c, true
}

// channel parses a number in [0, max] or a percentage and scales it to
// 0–255.
func channel(s string, maxVal float64) (uint8, bool) {
	scale := 255 / maxVal
	if p, ok := strings.CutSuffix(s, "%"); ok {
		s, maxVal, scale = p, 100, 2.55
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || f < 0 || f > maxVal {
		return 0, false
	}
	return uint8(math.Round(f * scale)), true
}
