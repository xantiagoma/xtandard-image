package simple

// nextjs is the Next.js image optimizer (next/image):
//
//	/_next/image?url=<encoded source>&w=<width>&q=<quality>
//
// Next.js picks the format from Accept and never upscales
// (https://nextjs.org/docs/app/api-reference/components/image; unpic
// providers/nextjs.ts).
func nextjs() *Spec {
	return &Spec{
		Name:     "nextjs",
		Doc:      "https://nextjs.org/docs/app/api-reference/components/image",
		Prefixes: []string{"/_next/image"},
		Extract:  QuerySource("url"),
		Params: map[string]Param{
			"w": Width("w"),
			"q": Quality("q"),
		},
		Fit: FitInside,
	}
}
