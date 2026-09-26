package simple

// builderio is the Builder.io image API (unpic "builder.io"), sharp-backed:
//
//	https://cdn.builder.io/api/v1/image/assets%2F<space>%2F<id>?width=&height=&format=&quality=&fit=&position=&sharp=
//
// fit follows sharp (contain letterboxes); sharp=true (unpic's default) is
// accepted silently (https://www.builder.io/c/docs/image-api; unpic
// providers/builder.io.ts).
func builderio() *Spec {
	return &Spec{
		Name:    "builderio",
		Doc:     "https://www.builder.io/c/docs/image-api",
		Domains: []string{"cdn.builder.io"},
		Extract: PathSource,
		Params: map[string]Param{
			"width":   Width("width"),
			"height":  Height("height"),
			"quality": Quality("quality"),
			"format":  Format("format"),
			"fit": FitMap("fit", map[string]Fit{
				"cover": FitCover, "contain": FitPad, "fill": FitFill, "inside": FitInside, "outside": FitOutside,
			}),
			"position": Gravity("position"),
			"sharp":    Silent(),
		},
		Fit: FitCover,
	}
}
