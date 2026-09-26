package simple

// kontentai is the Kontent.ai Image Transformation API (unpic "kontent.ai"),
// an imgix-like query grammar:
//
//	https://assets-us-01.kc-usercontent.com/<project>/<asset>/<file>?w=&h=&fit=clip|scale|crop&fp-x=&fp-y=&fm=&q=&lossless=&auto=format&dpr=&bg=
//
// fit clip fits inside (the default), scale stretches, crop covers at the
// focal point; bg is RGB or ARGB hex. rect and fp-z are not applied
// (https://kontent.ai/learn/docs/apis/image-transformation-api; unpic
// providers/kontent.ai.ts).
func kontentai() *Spec {
	return &Spec{
		Name:    "kontentai",
		Doc:     "https://kontent.ai/learn/docs/apis/image-transformation-api",
		Domains: []string{".kc-usercontent.com"},
		Extract: PathSource,
		Params: map[string]Param{
			"w":        Width("w"),
			"h":        Height("h"),
			"q":        Quality("q"),
			"fm":       Format("fm"),
			"fit":      FitMap("fit", map[string]Fit{"clip": FitInside, "scale": FitFill, "crop": FitCover}),
			"fp-x":     FocusPoint("fp-x", 'x'),
			"fp-y":     FocusPoint("fp-y", 'y'),
			"dpr":      DPR("dpr"),
			"bg":       Background("bg", true),
			"lossless": Lossless("lossless"),
			"auto":     Silent(),
			"fp-z":     Unsupported(),
			"rect":     Unsupported(),
			"crop":     Unsupported(),
		},
		Fit: FitInside,
	}
}
