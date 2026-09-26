package simple

import (
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// cloudimage is Cloudimage (Scaleflex) v7:
//
//	https://<token>.cloudimg.io/[v7/]<origin host>/<path>?w=&h=&q=&force_format=&func=&gravity=&org_if_sml=1
//	https://<token>.cloudimg.io/<percent-encoded origin URL>?ci_url_encoded=1&...
//
// The path is the origin URL without its scheme (https is assumed) or a
// full http(s) URL. func: crop and cropfit cover, fit pads, bound fits
// inside (the default), cover stretches, face covers at the face.
// org_if_sml=1 never enlarges. gravity takes compass words, auto/smart,
// face, or a relative focal point "Xp,Yp". r rotates counter-clockwise.
// Pixel crops (tl_px, br_px), trim, radius and face_margin are not applied
// (https://docs.cloudimage.io/go/cloudimage-documentation-v7/en/introduction;
// unpic providers/cloudimage.ts).
func cloudimage() *Spec {
	return &Spec{
		Name:    "cloudimage",
		Doc:     "https://docs.cloudimage.io/go/cloudimage-documentation-v7/en/introduction",
		Domains: []string{".cloudimg.io"},
		Extract: cloudimageExtract,
		Params: map[string]Param{
			"w": Width("w"), "width": Width("width"),
			"h": Height("h"), "height": Height("height"),
			"q":              Quality("q"),
			"force_format":   Format("force_format"),
			"func":           Func(cloudimageFunc),
			"gravity":        Func(cloudimageGravity),
			"org_if_sml":     Enlarge("org_if_sml", true),
			"bg_color":       Background("bg_color", false),
			"r":              Rotate("r", false),
			"flip":           Func(cloudimageFlip),
			"blur":           Blur("blur", 1),
			"ci_url_encoded": Silent(),
			"tl_px":          Unsupported(),
			"br_px":          Unsupported(),
			"trim":           Unsupported(),
			"radius":         Unsupported(),
			"face_margin":    Unsupported(),
		},
		Fit:     FitInside,
		Enlarge: true,
	}
}

// cloudimageFunc maps func; face covers at the face unless gravity says
// otherwise.
func cloudimageFunc(st *State, v string) error {
	switch v {
	case "crop", "cropfit":
		st.Fit = FitCover
	case "face":
		st.Fit = FitCover
		if st.Pipeline.Gravity.Type == transform.GravityUnset {
			st.Pipeline.Gravity = transform.Gravity{Type: transform.GravityFace}
		}
	case "fit":
		st.Fit = FitPad
	case "bound":
		st.Fit = FitInside
	case "boundmin":
		st.Fit = FitOutside
	case "cover":
		st.Fit = FitFill
	default:
		return dialect.InvalidArgument("func", v, expFit)
	}
	return nil
}

// cloudimageExtract turns the path into the origin URL.
func cloudimageExtract(path, rawQuery string) (*Extracted, error) {
	kvs, err := ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	// With ci_url_encoded=1 the origin URL, query included, is
	// percent-encoded into the path; decoding the path covers both forms.
	src, err := url.PathUnescape(strings.TrimPrefix(strings.TrimPrefix(path, "/"), "v7/"))
	if err != nil {
		return nil, dialect.InvalidURL("invalid path escaping")
	}
	if l := strings.ToLower(src); !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
		src = "https://" + src
	}
	return &Extracted{Source: src, Params: kvs}, nil
}

// cloudimageGravity maps compass words, auto/smart, face and "Xp,Yp".
func cloudimageGravity(st *State, v string) error {
	if xs, ys, ok := strings.Cut(v, ","); ok {
		xp, okx := strings.CutSuffix(xs, "p")
		yp, oky := strings.CutSuffix(ys, "p")
		x, ok1 := parseNumber(xp)
		y, ok2 := parseNumber(yp)
		if !okx || !oky || !ok1 || !ok2 || x < 0 || x > 100 || y < 0 || y > 100 {
			st.Ignore("gravity", reasonUnsupported) // pixel focal points need the source size
			return nil
		}
		st.Pipeline.Gravity = transform.Gravity{Type: transform.GravityFocusPoint, FocusX: x / 100, FocusY: y / 100}
		return nil
	}
	return Gravity("gravity").Apply(st, v)
}

// cloudimageFlip maps flip=h|v|hv.
func cloudimageFlip(st *State, v string) error {
	switch v {
	case "h":
		st.Pipeline.Flip.H = true
	case "v":
		st.Pipeline.Flip.V = true
	case "hv", "vh":
		st.Pipeline.Flip = transform.Flip{H: true, V: true}
	default:
		return dialect.InvalidArgument("flip", v, "h, v or hv")
	}
	return nil
}
