package simple

// astro is Astro's image endpoint (astro:assets, sharp service):
//
//	/_image?href=<source>&w=&h=&q=&f=&fit=&position=
//
// fit follows Astro's mapping onto sharp: contain and scale-down fit
// inside, none covers without cropping; the service never enlarges
// (https://docs.astro.build/en/reference/modules/astro-assets/; unpic
// providers/astro.ts).
func astro() *Spec {
	return &Spec{
		Name:     "astro",
		Doc:      "https://docs.astro.build/en/reference/modules/astro-assets/",
		Prefixes: []string{"/_image"},
		Extract:  QuerySource("href"),
		Params: map[string]Param{
			"w": Width("w"),
			"h": Height("h"),
			"q": Quality("q"),
			"f": Format("f"),
			"fit": FitMap("fit", map[string]Fit{
				"cover": FitCover, "contain": FitInside, "fill": FitFill, "inside": FitInside,
				"outside": FitOutside, "none": FitOutside, "scale-down": FitScaleDown,
			}),
			"position":   Gravity("position"),
			"background": Background("background", false),
		},
		Fit: FitCover,
	}
}
