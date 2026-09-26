package simple

import (
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// scene7 is Adobe Dynamic Media Classic (Scene7) Image Serving:
//
//	https://s7d1.scene7.com/is/image/<company>/<asset>?wid=&hei=&qlt=&fmt=&fit=&dpr=&bgColor=&rotate=&flip=&op_blur=
//
// With both sides and no fit the image fits inside and is padded with
// bgColor (Image Serving's default); fit=crop covers, fit=stretch
// stretches, fit=wrap covers without cropping, fit=fit|constrain|hfit|vfit
// fit inside. A ",1" suffix on fit allows upscaling (",0", the default,
// does not). Layers, text, masks, ICC and crop are not applied
// (https://experienceleague.adobe.com/docs/dynamic-media-developer-resources/image-serving-api/image-serving-api/http-protocol-reference/command-reference/c-command-reference.html;
// unpic providers/scene7.ts).
func scene7() *Spec {
	return &Spec{
		Name:     "scene7",
		Doc:      "https://experienceleague.adobe.com/docs/dynamic-media-developer-resources/image-serving-api/image-serving-api/http-protocol-reference/command-reference/c-command-reference.html",
		Prefixes: []string{"/is/image/"},
		Domains:  []string{".scene7.com"},
		Extract:  PathSource,
		Params: map[string]Param{
			"wid":        Width("wid"),
			"hei":        Height("hei"),
			"qlt":        Func(scene7Quality),
			"fmt":        Func(scene7Format),
			"fit":        Func(scene7Fit),
			"dpr":        Func(scene7DPR),
			"bgColor":    Background("bgColor", false),
			"bgc":        Background("bgc", false),
			"rotate":     Rotate("rotate", true),
			"flip":       Func(scene7Flip),
			"op_blur":    Blur("op_blur", 1),
			"res":        Unsupported(),
			"cache":      Silent(),
			"req":        Unsupported(),
			"scale":      Unsupported(),
			"crop":       Unsupported(),
			"mask":       Unsupported(),
			"blendMode":  Unsupported(),
			"layer":      Unsupported(),
			"opac":       Unsupported(),
			"pos":        Unsupported(),
			"text":       Unsupported(),
			"textAngle":  Unsupported(),
			"textAttr":   Unsupported(),
			"icc":        Unsupported(),
			"iccEmbed":   Unsupported(),
			"op_usm":     Unsupported(),
			"op_sharpen": Unsupported(),
		},
		Fit: FitPad,
	}
}

// scene7Quality parses qlt=<quality>[,<chroma>].
func scene7Quality(st *State, v string) error {
	q, _, _ := strings.Cut(v, ",")
	n, err := parseQuality("qlt", q)
	st.Request.Output.Quality = n
	return err
}

// scene7Format parses fmt=<format>[,<pixel type>[,<compression>]].
func scene7Format(st *State, v string) error {
	f, _, _ := strings.Cut(v, ",")
	return Format("fmt").Apply(st, f)
}

// scene7Fit parses fit=<mode>[,0|1].
func scene7Fit(st *State, v string) error {
	mode, up, _ := strings.Cut(v, ",")
	switch strings.ToLower(mode) {
	case "crop":
		st.Fit = FitCover
	case "stretch":
		st.Fit = FitFill
	case "wrap":
		st.Fit = FitOutside
	case "fit", "constrain", "hfit", "vfit":
		st.Fit = FitInside
	default:
		return dialect.InvalidArgument("fit", v, expFit)
	}
	switch up {
	case "", "0":
		st.SetEnlarge(false)
	case "1":
		st.SetEnlarge(true)
	default:
		return dialect.InvalidArgument("fit", v, expFit)
	}
	return nil
}

// scene7DPR parses dpr=<ratio>|on|off.
func scene7DPR(st *State, v string) error {
	if v == "on" || v == "off" {
		return nil
	}
	return DPR("dpr").Apply(st, v)
}

// scene7Flip parses flip=lr|ud|lrud (and h|v|hv).
func scene7Flip(st *State, v string) error {
	switch strings.ToLower(v) {
	case "lr", "h":
		st.Pipeline.Flip = transform.Flip{H: true}
	case "ud", "v":
		st.Pipeline.Flip = transform.Flip{V: true}
	case "lrud", "hv", "vh":
		st.Pipeline.Flip = transform.Flip{H: true, V: true}
	case "none":
		st.Pipeline.Flip = transform.Flip{}
	default:
		return dialect.InvalidArgument("flip", v, "lr, ud or lrud")
	}
	return nil
}
