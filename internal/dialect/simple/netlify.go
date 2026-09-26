package simple

// netlify is the Netlify Image CDN:
//
//	/.netlify/images?url=<source>&w=&h=&fit=contain|cover|fill&position=&fm=&q=
//
// fm=blurhash is recorded as an unsupported format
// (https://docs.netlify.com/image-cdn/overview/; unpic providers/netlify.ts).
func netlify() *Spec {
	return &Spec{
		Name:     "netlify",
		Doc:      "https://docs.netlify.com/image-cdn/overview/",
		Prefixes: []string{"/.netlify/images"},
		Extract:  QuerySource("url"),
		Params: map[string]Param{
			"w":        Width("w"),
			"h":        Height("h"),
			"q":        Quality("q"),
			"fm":       Format("fm"),
			"fit":      FitMap("fit", map[string]Fit{"contain": FitInside, "cover": FitCover, "fill": FitFill}),
			"position": Gravity("position"),
		},
		Fit: FitInside,
	}
}
