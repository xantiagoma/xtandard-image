package awssih

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// colorExpected is the client-safe description of a colour argument.
const colorExpected = "a colour: {r,g,b,alpha} object, #hex, rgb()/rgba() or a CSS colour name"

// namedColors are the CSS level 2 colour keywords plus the ones SIH's own
// examples use. sharp accepts every CSS name through the color module; the
// long tail is rarely used in request JSON and is rejected with a 400.
var namedColors = map[string]transform.Color{
	"black":       {A: 255},
	"white":       {R: 255, G: 255, B: 255, A: 255},
	"transparent": {},
	"red":         {R: 255, A: 255},
	"lime":        {G: 255, A: 255},
	"green":       {G: 128, A: 255},
	"blue":        {B: 255, A: 255},
	"yellow":      {R: 255, G: 255, A: 255},
	"cyan":        {G: 255, B: 255, A: 255},
	"aqua":        {G: 255, B: 255, A: 255},
	"magenta":     {R: 255, B: 255, A: 255},
	"fuchsia":     {R: 255, B: 255, A: 255},
	"silver":      {R: 192, G: 192, B: 192, A: 255},
	"gray":        {R: 128, G: 128, B: 128, A: 255},
	"grey":        {R: 128, G: 128, B: 128, A: 255},
	"maroon":      {R: 128, A: 255},
	"olive":       {R: 128, G: 128, A: 255},
	"purple":      {R: 128, B: 128, A: 255},
	"teal":        {G: 128, B: 128, A: 255},
	"navy":        {B: 128, A: 255},
	"orange":      {R: 255, G: 165, A: 255},
}

// parseColor decodes a sharp colour argument: an object {r, g, b, alpha}
// (channels 0–255, alpha 0–1, default 1) or a CSS colour string.
func parseColor(name string, raw json.RawMessage) (transform.Color, error) {
	bad := func() (transform.Color, error) {
		return transform.Color{}, dialect.InvalidArgument(name, string(raw), colorExpected)
	}
	switch kind(raw) {
	case kindObject:
		m, err := objectMap(raw)
		if err != nil {
			return bad()
		}
		var c [3]uint8
		for i, ch := range []string{"r", "g", "b"} {
			v, ok := m[ch]
			if !ok {
				continue
			}
			f, ok := asNumber(v)
			if !ok || f < 0 || f > 255 {
				return bad()
			}
			c[i] = uint8(jsRound(f))
		}
		a := 1.0
		if v, ok := m["alpha"]; ok {
			f, ok := asNumber(v)
			if !ok || f < 0 || f > 1 {
				return bad()
			}
			a = f
		}
		return transform.Color{R: c[0], G: c[1], B: c[2], A: uint8(math.Round(a * 255))}, nil
	case kindString:
		s, _ := asString(raw)
		if c, ok := cssColor(s); ok {
			return c, nil
		}
		return bad()
	default:
		return bad()
	}
}

// cssColor parses #rgb, #rgba, #rrggbb, #rrggbbaa, rgb(r,g,b), rgba(r,g,b,a)
// and the names in namedColors.
func cssColor(s string) (transform.Color, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if c, ok := namedColors[s]; ok {
		return c, true
	}
	if hex, ok := strings.CutPrefix(s, "#"); ok {
		return hexColor(hex)
	}
	var args string
	if a, ok := strings.CutPrefix(s, "rgba("); ok {
		args = a
	} else if a, ok := strings.CutPrefix(s, "rgb("); ok {
		args = a
	} else {
		return transform.Color{}, false
	}
	args, ok := strings.CutSuffix(args, ")")
	if !ok {
		return transform.Color{}, false
	}
	parts := strings.Split(args, ",")
	if len(parts) != 3 && len(parts) != 4 {
		return transform.Color{}, false
	}
	var ch [4]uint8
	ch[3] = 255
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(f) {
			return transform.Color{}, false
		}
		if i == 3 {
			if f < 0 || f > 1 {
				return transform.Color{}, false
			}
			ch[3] = uint8(math.Round(f * 255))
			continue
		}
		if f < 0 || f > 255 {
			return transform.Color{}, false
		}
		ch[i] = uint8(math.Round(f))
	}
	return transform.Color{R: ch[0], G: ch[1], B: ch[2], A: ch[3]}, true
}

func hexColor(h string) (transform.Color, bool) {
	switch len(h) {
	case 3, 4:
		var b strings.Builder
		for i := range len(h) {
			b.WriteByte(h[i])
			b.WriteByte(h[i])
		}
		h = b.String()
	case 6, 8:
	default:
		return transform.Color{}, false
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return transform.Color{}, false
	}
	if len(b) == 3 {
		b = append(b, 0xff)
	}
	return transform.Color{R: b[0], G: b[1], B: b[2], A: b[3]}, true
}
