package simple

// contentful is the Contentful Images API:
//
//	https://images.ctfassets.net/<space>/<asset>/<token>/<file>?w=&h=&fm=&q=&fit=&f=&bg=&r=&fl=
//
// fit: pad letterboxes with bg, fill crops to the box at f, crop and thumb
// are approximated by fill, scale stretches; without fit the image fits inside and is never enlarged.
// fl=progressive and fl=png8 map onto the encoder; r (corner radius) is not
// applied (https://www.contentful.com/developers/docs/references/images-api/;
// unpic providers/contentful.ts).
func contentful() *Spec {
	return &Spec{
		Name:    "contentful",
		Doc:     "https://www.contentful.com/developers/docs/references/images-api/",
		Domains: []string{"images.ctfassets.net", "downloads.ctfassets.net"},
		Extract: PathSource,
		Params: map[string]Param{
			"w":  Width("w"),
			"h":  Height("h"),
			"q":  Quality("q"),
			"fm": Format("fm"),
			"fit": FitMap("fit", map[string]Fit{
				"pad": FitPad, "fill": FitCover, "crop": FitCover, "thumb": FitCover, "scale": FitFill,
			}),
			"f":  Gravity("f"),
			"bg": Background("bg", false),
			"fl": Func(contentfulFlag),
			"r":  Unsupported(),
		},
		Fit: FitInside,
	}
}

// contentfulFlag maps fl=progressive|png8.
func contentfulFlag(st *State, v string) error {
	switch v {
	case "progressive":
		st.Request.Output.Progressive = new(true)
	case "png8":
		parseFormat("png8", &st.Request.Output)
	default:
		st.Ignore("fl", reasonUnsupported)
	}
	return nil
}
