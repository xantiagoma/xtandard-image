package simple

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// wordpress is WordPress.com / Jetpack Photon (i0.wp.com):
//
//	https://i0.wp.com/<origin host>/<path>?w=&h=&crop=1&resize=W,H&fit=W,H&lb=W,H&quality=&strip=&zoom=
//
// w and h alone scale; crop=1 (unpic's default) covers the box; resize=W,H
// covers, fit=W,H fits inside, lb=W,H letterboxes; zoom is the pixel
// ratio. Photon never enlarges. A pixel crop (crop=x,y,w,h) and the
// filter/brightness/contrast/colorize/smooth effects are not applied
// (https://developer.wordpress.com/docs/photon/api/; unpic
// providers/wordpress.ts).
func wordpress() *Spec {
	return &Spec{
		Name:    "wordpress",
		Doc:     "https://developer.wordpress.com/docs/photon/api/",
		Domains: []string{".wp.com", "files.wordpress.com", ".files.wordpress.com"},
		Extract: PathSource,
		Params: map[string]Param{
			"w":       Width("w"),
			"h":       Height("h"),
			"crop":    Func(wordpressCrop),
			"resize":  Func(wordpressBox("resize", FitCover)),
			"fit":     Func(wordpressBox("fit", FitInside)),
			"lb":      Func(wordpressBox("lb", FitPad)),
			"quality": Quality("quality"),
			"format":  Format("format"),
			"zoom":    DPR("zoom"),
			"strip": Func(func(st *State, v string) error {
				strip := v == "all" || v == "info"
				st.Request.Output.StripMetadata = &strip
				return nil
			}),
			"ssl":        Silent(),
			"ulb":        Unsupported(),
			"filter":     Unsupported(),
			"brightness": Unsupported(),
			"contrast":   Unsupported(),
			"colorize":   Unsupported(),
			"smooth":     Unsupported(),
		},
		Fit: FitInside,
	}
}

// wordpressCrop maps crop=1|0|true|false; a pixel rectangle is ignored.
func wordpressCrop(st *State, v string) error {
	if b, ok := parseBool(v); ok {
		if b {
			st.Fit = FitCover
		}
		return nil
	}
	st.Ignore("crop", reasonUnsupported)
	return nil
}

// wordpressBox parses "W,H" and sets the fit.
func wordpressBox(field string, fit Fit) func(st *State, v string) error {
	return func(st *State, v string) error {
		ws, hs, ok := strings.Cut(v, ",")
		if !ok {
			return dialect.InvalidArgument(field, v, "W,H")
		}
		w, err := parseDim(field, ws)
		if err != nil {
			return err
		}
		h, err := parseDim(field, hs)
		if err != nil {
			return err
		}
		st.Width, st.Height, st.Fit = w, h, fit
		return nil
	}
}
