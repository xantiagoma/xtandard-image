package simple

import (
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// storyblok is the Storyblok Image Service, a thumbor subset:
//
//	https://a.storyblok.com/f/<space>/<WxH>/<hash>/<file>/m/[<crop>/][fit-in/][<W>x<H>/][smart/][filters:<f>(<v>):...]
//	https://img2.storyblok.com/[<crop>/][fit-in/][<W>x<H>/][smart/][filters:...]/f/<space>/...   (legacy)
//
// A negative width or height flips; 0 is auto. The size covers the box
// (thumbor's default crop) unless fit-in, which fits inside and pads with
// fill(). smart crops at the most interesting region. Filters format,
// quality, fill, grayscale, blur, rotate and no_upscale apply; focal,
// brightness and round_corner are not applied, nor is a manual crop
// rectangle. Each filter is the parameter "filters:<name>"
// (https://www.storyblok.com/docs/image-service; unpic providers/storyblok.ts).
func storyblok() *Spec {
	return &Spec{
		Name:    "storyblok",
		Doc:     "https://www.storyblok.com/docs/image-service",
		Domains: []string{"a.storyblok.com", "img2.storyblok.com", ".storyblok.com"},
		Extract: storyblokExtract,
		Params: map[string]Param{
			"size":   Func(storyblokSize),
			"fit-in": Func(func(st *State, _ string) error { st.Fit = FitInside; return nil }),
			"smart": Func(func(st *State, _ string) error {
				st.Pipeline.Gravity = transform.Gravity{Type: transform.GravitySmart}
				return nil
			}),
			"crop":                 Unsupported(),
			"filters:format":       Format("filters:format"),
			"filters:quality":      Quality("filters:quality"),
			"filters:fill":         Func(storyblokFill),
			"filters:grayscale":    Func(func(st *State, _ string) error { st.Pipeline.Grayscale = true; return nil }),
			"filters:blur":         Func(storyblokBlur),
			"filters:rotate":       Rotate("filters:rotate", true),
			"filters:no_upscale":   Func(func(st *State, _ string) error { st.SetEnlarge(false); return nil }),
			"filters:focal":        Unsupported(),
			"filters:brightness":   Unsupported(),
			"filters:round_corner": Unsupported(),
		},
		Fit:     FitCover,
		Enlarge: true,
	}
}

// storyblokExtract splits either URL layout into the asset path and the
// modifiers.
func storyblokExtract(path, rawQuery string) (*Extracted, error) {
	kvs, err := ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	var src, mods string
	if strings.HasPrefix(path, "/f/") {
		src = path
		if before, after, ok := strings.CutLast(path, "/m/"); ok {
			src, mods = before, after
		} else if s, ok := strings.CutSuffix(path, "/m"); ok {
			src = s
		}
	} else {
		i := strings.Index(path, "/f/")
		if i < 0 {
			return nil, dialect.InvalidURL("missing /f/ asset path")
		}
		src, mods = path[i:], path[:i]
	}
	ex := &Extracted{Source: src, Escaped: true, Params: kvs}
	mods, err = url.PathUnescape(strings.Trim(mods, "/"))
	if err != nil {
		return nil, dialect.InvalidURL("invalid path escaping")
	}
	for mods != "" {
		var seg string
		if strings.HasPrefix(mods, "filters:") {
			seg, mods = mods, ""
		} else {
			seg, mods, _ = strings.Cut(mods, "/")
		}
		switch {
		case seg == "":
		case seg == "fit-in" || seg == "smart":
			ex.Params = append(ex.Params, KV{Key: seg})
		case strings.HasPrefix(seg, "filters:"):
			fs, err := splitFilters(strings.TrimPrefix(strings.TrimSuffix(seg, "/"), "filters:"))
			if err != nil {
				return nil, err
			}
			ex.Params = append(ex.Params, fs...)
		case strings.Contains(seg, ":"):
			ex.Params = append(ex.Params, KV{Key: "crop", Value: seg})
		case strings.Contains(seg, "x"):
			ex.Params = append(ex.Params, KV{Key: "size", Value: seg})
		default:
			return nil, dialect.InvalidURL("invalid image service segment")
		}
		if len(ex.Params) > maxParams {
			return nil, dialect.InvalidURL("too many parameters")
		}
	}
	return ex, nil
}

// splitFilters parses "name(args):name(args)" (colons inside parentheses
// belong to the arguments) into "filters:<name>" parameters.
func splitFilters(s string) ([]KV, error) {
	var out []KV
	for s != "" {
		open := strings.IndexByte(s, '(')
		if open <= 0 {
			return nil, dialect.InvalidURL("invalid filters")
		}
		closing := strings.IndexByte(s[open:], ')')
		if closing < 0 {
			return nil, dialect.InvalidURL("invalid filters")
		}
		closing += open
		out = append(out, KV{Key: "filters:" + s[:open], Value: s[open+1 : closing]})
		s = s[closing+1:]
		if s != "" {
			if s[0] != ':' {
				return nil, dialect.InvalidURL("invalid filters")
			}
			s = s[1:]
		}
	}
	return out, nil
}

// storyblokSize parses [-]W x [-]H; a minus flips that axis.
func storyblokSize(st *State, v string) error {
	ws, hs, ok := strings.Cut(v, "x")
	if !ok {
		return dialect.InvalidArgument("size", v, "WxH")
	}
	if w, found := strings.CutPrefix(ws, "-"); found {
		ws, st.Pipeline.Flip.H = w, true
	}
	if h, found := strings.CutPrefix(hs, "-"); found {
		hs, st.Pipeline.Flip.V = h, true
	}
	w, err := parseDim("size", ws)
	if err != nil {
		return err
	}
	h, err := parseDim("size", hs)
	if err != nil {
		return err
	}
	st.Width, st.Height = w, h
	return nil
}

// storyblokFill sets the padding colour (a hex colour or "transparent").
// With fit-in the image is padded to the box.
func storyblokFill(st *State, v string) error {
	if v == "transparent" {
		st.Pipeline.Background = &transform.Color{}
	} else if err := Background("filters:fill", false).Apply(st, v); err != nil {
		return err
	}
	if st.Fit == FitInside {
		st.Fit = FitPad
	}
	return nil
}

// storyblokBlur maps blur(<radius>[,<sigma>]); thumbor's radius is about
// twice the sigma when sigma is omitted.
func storyblokBlur(st *State, v string) error {
	radius, sigma, hasSigma := strings.Cut(v, ",")
	if hasSigma {
		return Blur("filters:blur", 1).Apply(st, sigma)
	}
	return Blur("filters:blur", 0.5).Apply(st, radius)
}
