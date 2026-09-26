package simple

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// uploadcare is the Uploadcare URL API:
//
//	https://ucarecdn.com/<uuid>/-/<operation>/<args>/-/<operation>/<args>/[<filename>]
//
// Operations: resize/WxH (either side may be empty; both sides stretch),
// preview/WxH (fits inside, never enlarges), scale_crop/WxH[/<pos>] and
// smart_resize/WxH (cover), format, quality (a number, or
// lightest|lighter|normal|better|best), progressive, stretch on|off,
// setfill, rotate (counter-clockwise), flip, mirror, grayscale,
// autorotate, strip_meta. crop, border_radius, blur, sharp and
// zoom_objects are not applied. The source is "<uuid>/"; the filename is
// dropped (https://uploadcare.com/docs/transformations/image/; unpic
// providers/uploadcare.ts).
func uploadcare() *Spec {
	return &Spec{
		Name:    "uploadcare",
		Doc:     "https://uploadcare.com/docs/transformations/image/",
		Domains: []string{"ucarecdn.com", ".ucarecdn.com", ".ucr.io"},
		Extract: uploadcareExtract,
		Params: map[string]Param{
			"resize":       Func(uploadcareSize(FitFill)),
			"preview":      Func(uploadcarePreview),
			"scale_crop":   Func(uploadcareScaleCrop),
			"smart_resize": Func(uploadcareSmart),
			"format":       Format("format"),
			"quality":      Func(uploadcareQuality),
			"progressive":  Progressive("progressive"),
			"stretch":      Func(uploadcareStretch),
			"setfill":      Background("setfill", false),
			"rotate":       Rotate("rotate", false),
			"flip":         Func(func(st *State, _ string) error { st.Pipeline.Flip.V = true; return nil }),
			"mirror":       Func(func(st *State, _ string) error { st.Pipeline.Flip.H = true; return nil }),
			"grayscale":    Func(func(st *State, _ string) error { st.Pipeline.Grayscale = true; return nil }),
			"autorotate": Bool("autorotate", func(st *State, v bool) {
				st.Pipeline.AutoRotate = &v
			}),
			"strip_meta": Func(func(st *State, v string) error {
				strip := v != "none"
				st.Request.Output.StripMetadata = &strip
				return nil
			}),
			"crop":          Unsupported(),
			"border_radius": Unsupported(),
			"blur":          Unsupported(),
			"sharp":         Unsupported(),
			"zoom_objects":  Unsupported(),
			"enhance":       Unsupported(),
			"filter":        Unsupported(),
		},
		Fit:     FitInside,
		Enlarge: true,
	}
}

// uploadcareExtract splits "/<uuid>/-/op/args/.../[filename]".
func uploadcareExtract(path, rawQuery string) (*Extracted, error) {
	kvs, err := ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	rest := strings.TrimPrefix(path, "/")
	uuid, rest, _ := strings.Cut(rest, "/")
	if uuid == "" {
		return nil, dialect.InvalidURL("missing file uuid")
	}
	ex := &Extracted{Source: uuid + "/", Escaped: true, Params: kvs}
	trailingSlash := strings.HasSuffix(rest, "/")
	chunks := strings.Split(strings.Trim(rest, "/"), "/-/")
	if len(chunks) > maxParams {
		return nil, dialect.InvalidURL("too many parameters")
	}
	for i, chunk := range chunks {
		if i == 0 {
			// Before the first operation: "" or "-" (operations follow) or
			// a bare filename.
			if chunk == "-" || chunk == "" || !strings.HasPrefix(chunk, "-/") {
				continue
			}
			chunk = strings.TrimPrefix(chunk, "-/")
		}
		parts := strings.Split(chunk, "/")
		if last := parts[len(parts)-1]; i == len(chunks)-1 && !trailingSlash && len(parts) > 1 &&
			strings.Contains(last, ".") && (last[0] < '0' || last[0] > '9') {
			parts = parts[:len(parts)-1] // the filename
		}
		ex.Params = append(ex.Params, KV{Key: parts[0], Value: strings.Join(parts[1:], "/")})
	}
	return ex, nil
}

// parseSizes parses "WxH" where either side may be empty.
func parseSizes(field, v string) (w, h int, err error) {
	ws, hs, ok := strings.Cut(v, "x")
	if !ok {
		return 0, 0, dialect.InvalidArgument(field, v, "WxH")
	}
	if w, err = parseDim(field, ws); err != nil {
		return 0, 0, err
	}
	h, err = parseDim(field, hs)
	return w, h, err
}

// uploadcareSize sets the size; fit applies when both sides are given.
func uploadcareSize(fit Fit) func(st *State, v string) error {
	return func(st *State, v string) error {
		w, h, err := parseSizes("resize", v)
		if err != nil {
			return err
		}
		st.Width, st.Height, st.Fit = w, h, FitInside
		if w > 0 && h > 0 {
			st.Fit = fit
		}
		return nil
	}
}

func uploadcarePreview(st *State, v string) error {
	if v == "" {
		return nil
	}
	if err := uploadcareSize(FitScaleDown)(st, v); err != nil {
		return err
	}
	st.Fit = FitScaleDown
	return nil
}

func uploadcareScaleCrop(st *State, v string) error {
	size, pos, _ := strings.Cut(v, "/")
	if err := uploadcareSize(FitCover)(st, size); err != nil {
		return err
	}
	st.Fit = FitCover
	if pos != "" {
		g, ok := parseGravity(pos)
		if !ok {
			if pos == "smart" || strings.HasPrefix(pos, "smart_") {
				g = transform.GravitySmart
			} else {
				st.Ignore("scale_crop", reasonUnsupported)
				return nil
			}
		}
		st.Pipeline.Gravity = transform.Gravity{Type: g}
	}
	return nil
}

func uploadcareSmart(st *State, v string) error {
	if err := uploadcareSize(FitCover)(st, v); err != nil {
		return err
	}
	st.Fit = FitCover
	st.Pipeline.Gravity = transform.Gravity{Type: transform.GravitySmart}
	return nil
}

// uploadcareQuality maps the named levels (and numbers, which unpic
// writes); smart and smart_retina leave the default.
func uploadcareQuality(st *State, v string) error {
	levels := map[string]int{"lightest": 50, "lighter": 65, "normal": 80, "better": 85, "best": 95, "smart": 0, "smart_retina": 0}
	if q, ok := levels[v]; ok {
		st.Request.Output.Quality = q
		return nil
	}
	return Quality("quality").Apply(st, v)
}

func uploadcareStretch(st *State, v string) error {
	switch v {
	case "on":
		st.SetEnlarge(true)
	case "off":
		st.SetEnlarge(false)
	case "fill":
		st.Ignore("stretch", reasonUnsupported)
	default:
		return dialect.InvalidArgument("stretch", v, "on, off or fill")
	}
	return nil
}
