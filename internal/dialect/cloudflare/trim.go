package cloudflare

import (
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// defaultTrimThreshold is the colour distance used by trim=border without
// trim.border.tolerance (imgproxy's trim default).
const defaultTrimThreshold = 10

// maxTolerance is the largest trim.border.tolerance ("must match exactly").
const maxTolerance = 255

// side is one trim value: pixels (≥ 1, integer) or a fraction of the
// source dimension (< 1).
type side struct {
	set bool
	v   float64
}

func (s side) frac() bool { return s.v < 1 }

// trimSides collects the per-side trim options; finishTrim turns them into
// a pre-resize crop once every option is known.
type trimSides struct {
	top, right, bottom, left, width, height side
}

func (t *trimSides) any() bool {
	return t.top.set || t.right.set || t.bottom.set || t.left.set || t.width.set || t.height.set
}

func parseSide(field, s string) (side, error) {
	f, err := parseFloat(s)
	if err != nil || f < 0 || (f >= 1 && f != float64(int64(f))) || f > transform.MaxDimension {
		return side{}, dialect.InvalidArgument(field, s, expTrimSide)
	}
	return side{set: true, v: f}, nil
}

// applyTrim handles trim=border, trim=top;right;bottom;left and the
// trim.* options (developers.cloudflare.com/images/optimization/features/#trim).
func (st *state) applyTrim(key, v string) error {
	pl := st.pl()
	switch key {
	case "trim":
		if v == "border" {
			return st.trimBorder()
		}
		parts := strings.Split(v, ";")
		if len(parts) != 4 {
			return dialect.InvalidArgument("trim", v, expTrim)
		}
		dst := []*side{&st.trim.top, &st.trim.right, &st.trim.bottom, &st.trim.left}
		for i, p := range parts {
			s, err := parseSide("trim", p)
			if err != nil {
				return err
			}
			*dst[i] = s
		}
	case "trim.border":
		b, err := parseBool(key, v)
		if err != nil {
			return err
		}
		if !b {
			pl.Trim = nil
			return nil
		}
		return st.trimBorder()
	case "trim.border.color":
		c, err := parseColor(key, v)
		if err != nil {
			return err
		}
		if err := st.trimBorder(); err != nil {
			return err
		}
		pl.Trim.Color = &c
	case "trim.border.tolerance":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > maxTolerance {
			return dialect.InvalidArgument(key, v, expTolerance)
		}
		if err := st.trimBorder(); err != nil {
			return err
		}
		// Cloudflare: 255 must match exactly; imgproxy: 0 must match exactly.
		pl.Trim.Threshold = float64(maxTolerance - n)
	case "trim.border.keep":
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return dialect.InvalidArgument(key, v, expBorderW)
		}
		st.b.Ignore(key, reasonNotImplemented)
	default:
		s, err := parseSide(key, v)
		if err != nil {
			return err
		}
		switch key {
		case "trim.top":
			st.trim.top = s
		case "trim.right":
			st.trim.right = s
		case "trim.bottom":
			st.trim.bottom = s
		case "trim.left":
			st.trim.left = s
		case "trim.width":
			st.trim.width = s
		default:
			st.trim.height = s
		}
	}
	return nil
}

func (st *state) trimBorder() error {
	pl := st.pl()
	if pl.Trim == nil {
		pl.Trim = &transform.Trim{Threshold: defaultTrimThreshold}
	}
	return nil
}

// finishTrim expresses the per-side trims as a crop anchored at the top
// left: left and top become gravity offsets (imgproxy semantics, ≥ 1 is
// pixels, < 1 a fraction of the source) and width/height the crop size.
// right/bottom need the source size: fractions are folded into a relative
// size when left/top are fractions too; pixel values are recorded as not
// implemented. A zero crop size keeps the rest of the source.
func (st *state) finishTrim() error {
	t := &st.trim
	if !t.any() {
		return nil
	}
	w, err := st.trimSize("trim.width", "trim.right", t.width, t.left, t.right)
	if err != nil {
		return err
	}
	h, err := st.trimSize("trim.height", "trim.bottom", t.height, t.top, t.bottom)
	if err != nil {
		return err
	}
	if w.IsZero() && h.IsZero() && t.left.v == 0 && t.top.v == 0 {
		return nil
	}
	st.pl().Crop = &transform.Crop{
		Width:  w,
		Height: h,
		Gravity: transform.Gravity{
			Type:    transform.GravityNorthWest,
			XOffset: t.left.v,
			YOffset: t.top.v,
		},
	}
	return nil
}

// trimSize returns the crop size along one axis.
func (st *state) trimSize(sizeField, farField string, size, near, far side) (transform.Dim, error) {
	switch {
	case size.set && size.v > 0:
		if size.frac() {
			return transform.Dim{Rel: size.v}, nil
		}
		return transform.Dim{Px: int(size.v)}, nil
	case size.set:
		return transform.Dim{}, dialect.InvalidArgument(sizeField, "0", "positive size")
	case !far.set || far.v == 0:
		return transform.Dim{}, nil
	case far.frac() && near.frac():
		// Round away binary noise (1 - 0.2 - 0.2 = 0.6000000000000001).
		rel := math.Round((1-near.v-far.v)*1e9) / 1e9
		if rel <= 0 {
			return transform.Dim{}, dialect.InvalidArgument(farField, strconv.FormatFloat(far.v, 'f', -1, 64), "trims that leave part of the image")
		}
		return transform.Dim{Rel: rel}, nil
	}
	st.b.Ignore(farField, reasonNotImplemented)
	return transform.Dim{}, nil
}

// applyBorder handles border=color;width, border=color;top;right;bottom;left
// and border.* (a Workers option in Cloudflare; accepted in URLs here).
func (st *state) applyBorder(key, v string) error {
	if st.border == nil {
		st.border = &transform.Border{Color: transform.Color{A: 255}}
		st.b.Ignore("border", reasonM2)
	}
	b := st.border
	switch key {
	case "border":
		parts := strings.Split(v, ";")
		if len(parts) != 2 && len(parts) != 5 {
			return dialect.InvalidArgument("border", v, expBorder)
		}
		c, err := parseColor("border", parts[0])
		if err != nil {
			return err
		}
		b.Color = c
		widths := make([]int, len(parts)-1)
		for i, p := range parts[1:] {
			if widths[i], err = borderWidth("border", p); err != nil {
				return err
			}
		}
		if len(widths) == 1 {
			b.Top, b.Right, b.Bottom, b.Left = widths[0], widths[0], widths[0], widths[0]
		} else {
			b.Top, b.Right, b.Bottom, b.Left = widths[0], widths[1], widths[2], widths[3]
		}
	case "border.color":
		c, err := parseColor(key, v)
		if err != nil {
			return err
		}
		b.Color = c
	default:
		n, err := borderWidth(key, v)
		if err != nil {
			return err
		}
		switch key {
		case "border.width":
			b.Top, b.Right, b.Bottom, b.Left = n, n, n, n
		case "border.top":
			b.Top = n
		case "border.right":
			b.Right = n
		case "border.bottom":
			b.Bottom = n
		default:
			b.Left = n
		}
	}
	return nil
}

func borderWidth(field, s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > transform.MaxDimension {
		return 0, dialect.InvalidArgument(field, s, expBorderW)
	}
	return n, nil
}
