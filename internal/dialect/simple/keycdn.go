package simple

import "github.com/xantiagoma/xtandard-image/internal/transform"

// keycdn is KeyCDN Image Processing, sharp-backed:
//
//	https://<zone>.kxcdn.com/<path>?width=&height=&quality=&format=&fit=&position=&enlarge=&crop=smart&...
//
// fit and flip/flop follow sharp (contain letterboxes, flip is vertical);
// images are not enlarged unless enlarge=1. crop=smart crops at the most
// interesting region; pixel crops, overlays (ol*), trim, extend and tone
// operations are not applied (https://www.keycdn.com/support/image-processing;
// unpic providers/keycdn.ts).
func keycdn() *Spec {
	return &Spec{
		Name:    "keycdn",
		Doc:     "https://www.keycdn.com/support/image-processing",
		Domains: []string{"ip.keycdn.com", ".kxcdn.com"},
		Extract: PathSource,
		Params: map[string]Param{
			"width":   Width("width"),
			"height":  Height("height"),
			"quality": Quality("quality"),
			"format":  Format("format"),
			"fit": FitMap("fit", map[string]Fit{
				"cover": FitCover, "contain": FitPad, "fill": FitFill, "inside": FitInside, "outside": FitOutside,
			}),
			"position":    Gravity("position"),
			"enlarge":     Enlarge("enlarge", false),
			"crop":        Func(keycdnCrop),
			"bg":          Background("bg", false),
			"rotate":      Rotate("rotate", true),
			"flip":        FlipV("flip"),
			"flop":        FlipH("flop"),
			"sharpen":     Sharpen("sharpen", 1),
			"blur":        Blur("blur", 1),
			"negate":      Bool("negate", func(st *State, v bool) { st.Pipeline.Negate = v }),
			"grayscale":   Grayscale("grayscale"),
			"progressive": Progressive("progressive"),
			"lossless":    Lossless("lossless"),
			"metadata": Bool("metadata", func(st *State, v bool) {
				strip := !v
				st.Request.Output.StripMetadata = &strip
			}),
			"trim":         Unsupported(),
			"extend":       Unsupported(),
			"gamma":        Unsupported(),
			"normalize":    Unsupported(),
			"threshold":    Unsupported(),
			"tint":         Unsupported(),
			"removealpha":  Unsupported(),
			"olurl":        Unsupported(),
			"olalign":      Unsupported(),
			"olx":          Unsupported(),
			"oly":          Unsupported(),
			"olwidth":      Unsupported(),
			"olheight":     Unsupported(),
			"olalpha":      Unsupported(),
			"olrepeat":     Unsupported(),
			"compression":  Unsupported(),
			"adaptive":     Unsupported(),
			"alphaquality": Unsupported(),
			"nearlossless": Unsupported(),
		},
		Fit: FitCover,
	}
}

// keycdnCrop maps crop=smart; pixel and focal-point crops are ignored.
func keycdnCrop(st *State, v string) error {
	if v == "smart" {
		st.Pipeline.Gravity.Type = transform.GravitySmart
		return nil
	}
	st.Ignore("crop", reasonUnsupported)
	return nil
}
