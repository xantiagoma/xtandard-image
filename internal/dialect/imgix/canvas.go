package imgix

import (
	"math"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// --- canvas: background, padding, border, trim, mask ----------------------

// applyCanvas maps bg, pad*, border*, trim* and mask*.
func (st *state) applyCanvas() error {
	pl := st.pl
	if v, ok := st.get("bg"); ok {
		c, err := parseColor("bg", v)
		if err != nil {
			return err
		}
		pl.Background = &c
	}
	if err := st.padding(); err != nil {
		return err
	}
	if err := st.border(); err != nil {
		return err
	}
	if err := st.trim(); err != nil {
		return err
	}
	return st.mask()
}

// sides applies the per-side overrides <prefix>-top/right/bottom/left.
func (st *state) sides(prefix string, top, right, bottom, left *int) error {
	for _, s := range []struct {
		key string
		dst *int
	}{{prefix + "-top", top}, {prefix + "-right", right}, {prefix + "-bottom", bottom}, {prefix + "-left", left}} {
		if v, ok := st.get(s.key); ok {
			n, err := intRange(s.key, v, 0, transform.MaxDimension)
			if err != nil {
				return err
			}
			*s.dst = n
		}
	}
	return nil
}

// padding maps pad and pad-top/right/bottom/left (pixels, filled with bg).
func (st *state) padding() error {
	if !st.has("pad") && !st.has("pad-top") && !st.has("pad-right") && !st.has("pad-bottom") && !st.has("pad-left") {
		return nil
	}
	p := &transform.Padding{}
	if v, ok := st.get("pad"); ok {
		n, err := intRange("pad", v, 0, transform.MaxDimension)
		if err != nil {
			return err
		}
		*p = transform.Padding{Top: n, Right: n, Bottom: n, Left: n}
	}
	if err := st.sides("pad", &p.Top, &p.Right, &p.Bottom, &p.Left); err != nil {
		return err
	}
	st.pl.Padding = p
	return nil
}

// border maps border=width,color with border-top/right/bottom/left
// overriding widths, and border-radius (M2). border-radius-inner is not
// implemented.
func (st *state) border() error {
	if v, ok := st.get("border"); ok {
		w, c, found := strings.Cut(v, ",")
		if !found {
			return dialect.InvalidArgument("border", v, expBorder)
		}
		n, err := intRange("border", w, 0, transform.MaxDimension)
		if err != nil {
			return err
		}
		col, err := parseColor("border", c)
		if err != nil {
			return err
		}
		b := &transform.Border{Top: n, Right: n, Bottom: n, Left: n, Color: col}
		if err := st.sides("border", &b.Top, &b.Right, &b.Bottom, &b.Left); err != nil {
			return err
		}
		st.pl.Border = b
	} else {
		var scratch transform.Border
		if err := st.sides("border", &scratch.Top, &scratch.Right, &scratch.Bottom, &scratch.Left); err != nil {
			return err
		}
		st.ignoreIf(reasonNeedsParent, "border-top", "border-right", "border-bottom", "border-left")
	}
	if v, ok := st.get("border-radius"); ok {
		r, err := radius("border-radius", v)
		if err != nil {
			return err
		}
		st.pl.Radius = r
	}
	st.ignoreIf(reasonNotImplemented, "border-radius-inner")
	return nil
}

// radius parses "r" or "tl,tr,br,bl".
func radius(field, v string) (*transform.Radius, error) {
	parts := list(v)
	if len(parts) != 1 && len(parts) != 4 {
		return nil, dialect.InvalidArgument(field, v, expRadius)
	}
	var n [4]int
	for i, p := range parts {
		x, err := intRange(field, p, 0, transform.MaxDimension)
		if err != nil {
			return nil, err
		}
		n[i] = x
	}
	if len(parts) == 1 {
		n[1], n[2], n[3] = n[0], n[0], n[0]
	}
	return &transform.Radius{TopLeft: n[0], TopRight: n[1], BottomRight: n[2], BottomLeft: n[3]}, nil
}

// trim maps trim=auto (threshold trim-md, default 11; trim-sd is not
// modelled) and trim=color (trim-color, default the corner pixel;
// threshold trim-tol, default 0). trim-pad is not implemented.
func (st *state) trim() error {
	v, ok := st.get("trim")
	md, err := st.optNum("trim-md", 0, 255, 11)
	if err != nil {
		return err
	}
	tol, err := st.optNum("trim-tol", 0, 255, 0)
	if err != nil {
		return err
	}
	if _, err := st.optNum("trim-sd", 0, 255, 0); err != nil {
		return err
	}
	var col *transform.Color
	if c, ok := st.get("trim-color"); ok {
		x, err := parseColor("trim-color", c)
		if err != nil {
			return err
		}
		col = &x
	}
	if !ok {
		st.ignoreIf(reasonNeedsParent, "trim-color", "trim-tol", "trim-md", "trim-sd", "trim-pad")
		return nil
	}
	switch v {
	case "auto":
		st.pl.Trim = &transform.Trim{Threshold: md}
		st.ignoreIf(reasonApproximated, "trim-sd")
		st.ignoreIf(reasonNeedsParent, "trim-color", "trim-tol")
	case "color":
		st.pl.Trim = &transform.Trim{Threshold: tol, Color: col}
		st.ignoreIf(reasonNeedsParent, "trim-md", "trim-sd")
	default:
		return dialect.InvalidArgument("trim", v, expTrim)
	}
	st.ignoreIf(reasonNotImplemented, "trim-pad")
	return nil
}

// mask maps mask=ellipse (+ mask-bg) and mask=corners (+ corner-radius).
// Image masks (a mask URL) are unsupported.
func (st *state) mask() error {
	v, ok := st.get("mask")
	var bg *transform.Color
	if c, has := st.get("mask-bg"); has {
		x, err := parseColor("mask-bg", c)
		if err != nil {
			return err
		}
		bg = &x
	}
	var r *transform.Radius
	if c, has := st.get("corner-radius"); has {
		var err error
		if r, err = radius("corner-radius", c); err != nil {
			return err
		}
	}
	switch {
	case !ok:
		st.ignoreIf(reasonNeedsParent, "mask-bg", "corner-radius")
	case v == "ellipse":
		st.pl.Mask = &transform.Mask{Shape: transform.MaskEllipse, Background: bg}
		st.ignoreIf(reasonNeedsParent, "corner-radius")
	case v == "corners":
		if r != nil {
			st.pl.Radius = r
		}
		st.ignoreIf(reasonNotImplemented, "mask-bg")
	default:
		st.b.Ignore("mask", reasonUnsupported)
		st.ignoreIf(reasonNeedsParent, "mask-bg", "corner-radius")
	}
	return nil
}

// --- pages ----------------------------------------------------------------

// applyPages maps page (1-based PDF page) and frame (1-based animation
// frame, which also selects a single frame).
func (st *state) applyPages() error {
	if v, ok := st.get("page"); ok {
		n, err := intRange("page", v, 1, math.MaxInt32)
		if err != nil {
			return err
		}
		n--
		st.pl.Page = &n
	}
	if v, ok := st.get("frame"); ok {
		n, err := intRange("frame", v, 1, math.MaxInt32)
		if err != nil {
			return err
		}
		n--
		one := 1
		st.pl.Page, st.pl.Pages = &n, &one
	}
	return nil
}
