package imgix

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// --- size and crop (https://docs.imgix.com/apis/rendering/size) -----------

// applySize maps w, h, dpr, the min/max clamps, rect, fit and its
// companions (crop, ar, fp-*, fill*, face*).
func (st *state) applySize() error {
	rs := &st.pl.Resize
	for _, d := range []struct {
		key string
		dst *transform.Dim
	}{{"w", &rs.Width}, {"h", &rs.Height}} {
		if v, ok := st.get(d.key); ok {
			x, err := dim(d.key, v)
			if err != nil {
				return err
			}
			*d.dst = x
		}
	}
	if v, ok := st.get("dpr"); ok {
		f, ok := finite(v)
		if !ok || f <= 0 || f > 10 {
			return dialect.InvalidArgument("dpr", v, "number > 0 and <= 10")
		}
		rs.DPR = f
	}
	for _, c := range []struct {
		key string
		dst *int
	}{{"max-w", &rs.MaxWidth}, {"max-h", &rs.MaxHeight}, {"min-w", &rs.MinWidth}, {"min-h", &rs.MinHeight}} {
		if v, ok := st.get(c.key); ok {
			n, err := intRange(c.key, v, 0, transform.MaxDimension)
			if err != nil {
				return err
			}
			*c.dst = n
		}
	}
	if err := st.applyRect(); err != nil {
		return err
	}
	return st.applyFit()
}

// applyRect maps rect=x,y,w,h to a pre-resize crop anchored at its top-left
// corner (values < 1 are fractions of the source).
func (st *state) applyRect() error {
	v, ok := st.get("rect")
	if !ok {
		return nil
	}
	parts := list(v)
	if len(parts) != 4 {
		return dialect.InvalidArgument("rect", v, expRect)
	}
	x, err := offset("rect.x", parts[0])
	if err != nil {
		return err
	}
	y, err := offset("rect.y", parts[1])
	if err != nil {
		return err
	}
	w, err := dim("rect.w", parts[2])
	if err != nil {
		return err
	}
	h, err := dim("rect.h", parts[3])
	if err != nil {
		return err
	}
	st.pl.Crop = &transform.Crop{
		Width: w, Height: h,
		Gravity: transform.Gravity{Type: transform.GravityNorthWest, XOffset: x, YOffset: y},
	}
	return nil
}

// applyFit maps fit (default clip once a size is given):
//
//	clip       fit inside the box, upscaling            (imgix default)
//	max        fit inside the box, never upscaling
//	min        fill-down: crop to the box's aspect ratio, never upscaling
//	crop       fill: cover the box and crop, gravity from crop
//	smart-crop fill with crop=auto (smart) unless crop says otherwise
//	scale      force: stretch to the box
//	fill       fit + extend to the box with fill=solid (fill-color) or blur (M3)
//	fillmax    fill without upscaling
//	clamp      fit + extend by replicating edge pixels
//	facearea   fill on the faces, facepad and faceindex (M3)
//
// imgix's fit=crop never upscales past the source; xi upscales like fill
// with enlarge, which imgix does for every mode except max, min and
// fillmax.
func (st *state) applyFit() error {
	rs := &st.pl.Resize
	fit, hasFit := st.get("fit")
	if !hasFit {
		if !st.has("w") && !st.has("h") {
			st.ignoreIf(reasonCropFit, "crop")
			st.ignoreIf(reasonARFit, "ar")
			st.ignoreIf(reasonFillFit, "fill", "fill-color")
			st.ignoreIf(reasonFaceFit, "facepad", "faceindex")
			return st.checkFocal(false)
		}
		fit = "clip"
	}
	var cropMode, fillMode, face, ar bool
	defGravity := transform.GravityUnset
	switch fit {
	case "clip":
		rs.Type, rs.Enlarge = transform.ResizeFit, true
	case "max":
		rs.Type = transform.ResizeFit
	case "min":
		rs.Type, cropMode = transform.ResizeFillDown, true
	case "crop":
		rs.Type, rs.Enlarge, cropMode, ar = transform.ResizeFill, true, true, true
	case "smart-crop":
		rs.Type, rs.Enlarge, cropMode, ar = transform.ResizeFill, true, true, true
		defGravity = transform.GravitySmart
	case "scale":
		rs.Type, rs.Enlarge = transform.ResizeForce, true
	case "fill":
		rs.Type, rs.Enlarge, fillMode = transform.ResizeFit, true, true
	case "fillmax":
		rs.Type, fillMode = transform.ResizeFit, true
	case "clamp":
		rs.Type, rs.Enlarge = transform.ResizeFit, true
		st.pl.Extend = transform.Extend{Enabled: true, Mode: transform.ExtendCopy}
	case "facearea":
		rs.Type, rs.Enlarge, cropMode, face = transform.ResizeFill, true, true, true
	default:
		return dialect.InvalidArgument("fit", fit, expFit)
	}

	if err := st.applyAR(ar); err != nil {
		return err
	}
	g, err := st.cropGravity(defGravity)
	if err != nil {
		return err
	}
	if face {
		if g, err = st.faceGravity(); err != nil {
			return err
		}
	} else {
		st.ignoreIf(reasonFaceFit, "facepad", "faceindex")
	}
	if cropMode {
		st.pl.Gravity = g
	} else {
		st.ignoreIf(reasonCropFit, "crop")
	}
	if err := st.checkFocal(cropMode && g.Type == transform.GravityFocusPoint); err != nil {
		return err
	}
	if fillMode {
		return st.applyFill()
	}
	st.ignoreIf(reasonFillFit, "fill", "fill-color")
	return nil
}

// applyAR maps ar=w:h (applied only with fit=crop and fit=smart-crop, as
// in imgix).
func (st *state) applyAR(apply bool) error {
	v, ok := st.get("ar")
	if !ok {
		return nil
	}
	a, b, found := strings.Cut(v, ":")
	num, ok1 := finite(a)
	den, ok2 := 1.0, true
	if found {
		den, ok2 = finite(b)
	}
	if !ok1 || !ok2 || num <= 0 || den <= 0 || num/den > transform.MaxFactor || num/den < 1/transform.MaxFactor {
		return dialect.InvalidArgument("ar", v, expAR)
	}
	if apply {
		st.pl.Resize.AspectRatio = num / den
	} else {
		st.b.Ignore("ar", reasonARFit)
	}
	return nil
}

// cropGravity maps the crop fallback chain. The first detector in the list
// wins (focalpoint, faces, entropy, edges, auto or object classes, which
// all fall back to the others in imgix); without one, top/bottom/left/right
// combine into a compass point. faces and object classes take effect in M3
// (until then the executor falls back to smart).
func (st *state) cropGravity(def transform.GravityType) (transform.Gravity, error) {
	v, ok := st.get("crop")
	if !ok {
		return transform.Gravity{Type: def}, nil
	}
	var (
		hor, ver int
		detector string
		classes  []string
	)
	for _, tok := range list(v) {
		switch tok = strings.TrimSpace(tok); tok {
		case "top":
			ver = -1
		case "bottom":
			ver = 1
		case "left":
			hor = -1
		case "right":
			hor = 1
		case "faces", "focalpoint", "entropy", "edges", "auto":
			if detector == "" {
				detector = tok
			}
		default:
			if !objectClass(tok) {
				return transform.Gravity{}, dialect.InvalidArgument("crop", v, expCrop)
			}
			if detector == "" {
				detector = "object"
			}
			if detector == "object" && len(classes) < transform.MaxListLen {
				classes = append(classes, tok)
			}
		}
	}
	switch detector {
	case "focalpoint":
		return st.focalPoint()
	case "faces":
		st.b.Ignore("crop", reasonM3)
		return transform.Gravity{Type: transform.GravityFace}, nil
	case "entropy":
		return transform.Gravity{Type: transform.GravityEntropy}, nil
	case "edges", "auto":
		return transform.Gravity{Type: transform.GravitySmart}, nil
	case "object":
		st.b.Ignore("crop", reasonM3)
		return transform.Gravity{Type: transform.GravityObject, Classes: classes}, nil
	}
	if hor == 0 && ver == 0 {
		return transform.Gravity{Type: def}, nil
	}
	return transform.Gravity{Type: compass(hor, ver)}, nil
}

// objectClass reports whether s looks like a COCO class or supercategory
// name ("car", "traffic light", "hair drier").
func objectClass(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < 'a' || c > 'z') && c != ' ' && c != '-' {
			return false
		}
	}
	return true
}

// compass maps horizontal (-1 left, 1 right) and vertical (-1 top,
// 1 bottom) alignment to a gravity.
func compass(hor, ver int) transform.GravityType {
	return [3][3]transform.GravityType{
		{transform.GravityNorthWest, transform.GravityNorth, transform.GravityNorthEast},
		{transform.GravityWest, transform.GravityCenter, transform.GravityEast},
		{transform.GravitySouthWest, transform.GravitySouth, transform.GravitySouthEast},
	}[ver+1][hor+1]
}

// focalPoint maps fp-x, fp-y (0..1, default 0.5) and fp-z (1..100).
func (st *state) focalPoint() (transform.Gravity, error) {
	x, err := st.optNum("fp-x", 0, 1, 0.5)
	if err != nil {
		return transform.Gravity{}, err
	}
	y, err := st.optNum("fp-y", 0, 1, 0.5)
	if err != nil {
		return transform.Gravity{}, err
	}
	z, err := st.optNum("fp-z", 1, 100, 0)
	if err != nil {
		return transform.Gravity{}, err
	}
	if z == 1 {
		z = 0
	}
	return transform.Gravity{Type: transform.GravityFocusPoint, FocusX: x, FocusY: y, FocusZoom: z}, nil
}

// checkFocal validates fp-* and records them as ignored unless used.
func (st *state) checkFocal(used bool) error {
	if used {
		return nil
	}
	if _, err := st.focalPoint(); err != nil {
		return err
	}
	st.ignoreIf(reasonFocalPoint, "fp-x", "fp-y", "fp-z")
	return nil
}

// faceGravity maps fit=facearea with facepad (>= 0, imgix default 1) and
// faceindex (1-based). Face detection takes effect in M3.
func (st *state) faceGravity() (transform.Gravity, error) {
	g := transform.Gravity{Type: transform.GravityFace}
	pad, err := st.optNum("facepad", 0, transform.MaxFactor, 0)
	if err != nil {
		return g, err
	}
	g.FacePad = pad
	if v, ok := st.get("faceindex"); ok {
		i, err := intRange("faceindex", v, 1, transform.MaxListLen+1)
		if err != nil {
			return g, err
		}
		i--
		g.FaceIndex = &i
	}
	st.b.Ignore("fit", reasonM3)
	return g, nil
}

// applyFill maps fill=solid|blur|gen and fill-color for fit=fill and
// fit=fillmax: the canvas is extended to the box and filled with
// fill-color (which takes precedence over bg) or, for blur, a blurred copy
// of the image (M3). Generative fill is unsupported and falls back to solid.
func (st *state) applyFill() error {
	st.pl.Extend = transform.Extend{Enabled: true}
	switch v, _ := st.get("fill"); v {
	case "", "solid":
	case "blur":
		st.pl.Extend.Mode = transform.ExtendBlur
		st.b.Ignore("fill", reasonM3)
	case "gen":
		st.b.Ignore("fill", reasonUnsupported)
	default:
		return dialect.InvalidArgument("fill", v, expFill)
	}
	if v, ok := st.get("fill-color"); ok {
		c, err := parseColor("fill-color", v)
		if err != nil {
			return err
		}
		st.ignoreIf(reasonFillColor, "bg")
		st.pl.Background = &c
	}
	return nil
}
