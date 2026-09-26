package simple

// directus is the Directus assets endpoint, sharp-backed:
//
//	/assets/<file id>[/<filename>]?width=&height=&quality=&format=&fit=cover|contain|inside|outside&withoutEnlargement=
//
// fit contain letterboxes like sharp; Directus enlarges unless
// withoutEnlargement is set. transforms (raw sharp calls) and key (a
// server-side preset) are not applied; access_token stays on the source
// (https://docs.directus.io/reference/files.html#requesting-a-thumbnail;
// unpic providers/directus.ts).
func directus() *Spec {
	return &Spec{
		Name:     "directus",
		Doc:      "https://docs.directus.io/reference/files.html#requesting-a-thumbnail",
		Prefixes: []string{"/assets/"},
		Extract:  PathSource,
		Params: map[string]Param{
			"width":   Width("width"),
			"height":  Height("height"),
			"quality": Quality("quality"),
			"format":  Format("format"),
			"fit": FitMap("fit", map[string]Fit{
				"cover": FitCover, "contain": FitPad, "inside": FitInside, "outside": FitOutside,
			}),
			"withoutEnlargement": Enlarge("withoutEnlargement", true),
			"download": Bool("download", func(st *State, v bool) {
				st.Request.Delivery.Attachment = &v
			}),
			"access_token": KeepInSource(),
			"transforms":   Unsupported(),
			"key":          Unsupported(),
		},
		Fit:     FitCover,
		Enlarge: true,
	}
}
