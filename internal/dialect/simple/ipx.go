package simple

import (
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// ipx is IPX (Nuxt Image, unjs/ipx), sharp-backed:
//
//	/_ipx/<modifiers>/<source path or URL>
//	modifiers: "_" (none) or k_v pairs joined by "," (k:v and k=v work too)
//
// Modifiers follow sharp: fit contain letterboxes (FitPad), flip is
// vertical and flop horizontal, blur and sharpen are sigma, and nothing is
// enlarged unless enlarge is set (https://github.com/unjs/ipx#modifiers;
// unpic providers/ipx.ts).
func ipx() *Spec {
	fit := FitMap("fit", map[string]Fit{
		"cover": FitCover, "contain": FitPad, "fill": FitFill, "inside": FitInside, "outside": FitOutside,
	})
	return &Spec{
		Name:     "ipx",
		Doc:      "https://github.com/unjs/ipx#modifiers",
		Prefixes: []string{"/_ipx/"},
		Extract:  ipxExtract,
		Params: map[string]Param{
			"w": Width("w"), "width": Width("width"),
			"h": Height("h"), "height": Height("height"),
			"s": Func(ipxSize), "resize": Func(ipxSize),
			"q": Quality("q"), "quality": Quality("quality"),
			"f": Format("f"), "format": Format("format"),
			"fit":      fit,
			"pos":      Gravity("pos"),
			"position": Gravity("position"),
			"b":        Background("b", false), "background": Background("background", false),
			"blur":      Blur("blur", 1),
			"sharpen":   Sharpen("sharpen", 1),
			"rotate":    Rotate("rotate", true),
			"flip":      FlipV("flip"),
			"flop":      FlipH("flop"),
			"grayscale": Grayscale("grayscale"),
			"negate":    Bool("negate", func(st *State, v bool) { st.Pipeline.Negate = v }),
			"enlarge":   Enlarge("enlarge", false),
			"animated":  Unsupported(),
			"kernel":    Unsupported(),
			"trim":      Unsupported(),
			"extend":    Unsupported(),
			"extract":   Unsupported(),
			"crop":      Unsupported(),
			"median":    Unsupported(),
			"gamma":     Unsupported(),
			"normalize": Unsupported(),
			"threshold": Unsupported(),
			"modulate":  Unsupported(),
			"tint":      Unsupported(),
		},
		Fit: FitCover,
	}
}

// ipxExtract splits "/_ipx/<modifiers>/<source>". The source keeps its
// escaping; an embedded "https://host/x" survives the path because its
// empty segment is kept.
func ipxExtract(path, _ string) (*Extracted, error) {
	rest := strings.TrimPrefix(path, "/")
	_, rest, _ = strings.Cut(rest, "/") // the endpoint segment ("_ipx" or a custom mount)
	mods, src, ok := strings.Cut(rest, "/")
	if !ok || src == "" {
		return nil, dialect.InvalidURL("missing source path")
	}
	ex := &Extracted{Source: src, Escaped: true}
	if mods == "_" || mods == "" {
		return ex, nil
	}
	mods, err := url.PathUnescape(mods)
	if err != nil {
		return nil, dialect.InvalidURL("invalid path escaping")
	}
	for part := range strings.SplitSeq(mods, ",") {
		if part == "" {
			continue
		}
		i := strings.IndexAny(part, "_:=")
		k, v := part, ""
		if i >= 0 {
			k, v = part[:i], part[i+1:]
		}
		ex.Params = append(ex.Params, KV{Key: k, Value: v})
		if len(ex.Params) > maxParams {
			return nil, dialect.InvalidURL("too many parameters")
		}
	}
	return ex, nil
}

// ipxSize parses s_<W>x<H>.
func ipxSize(st *State, v string) error {
	ws, hs, ok := strings.Cut(v, "x")
	if !ok {
		return dialect.InvalidArgument("s", v, "WxH")
	}
	w, err := parseDim("s", ws)
	if err != nil {
		return err
	}
	h, err := parseDim("s", hs)
	if err != nil {
		return err
	}
	st.Width, st.Height = w, h
	return nil
}
