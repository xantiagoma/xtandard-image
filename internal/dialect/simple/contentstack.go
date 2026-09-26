package simple

// contentstack is the Contentstack Image Delivery API:
//
//	https://images.contentstack.io/v3/assets/<stack>/<uid>/<version>/<file>?width=&height=&format=&quality=&fit=bounds|crop&auto=webp&disable=upscale&dpr=
//
// fit=bounds fits inside, crop covers; images are enlarged unless
// disable=upscale. auto=webp|avif leaves the format to negotiation unless
// format is also given. Overlays, padding, canvas, trim and colour
// adjustments are not applied
// (https://www.contentstack.com/docs/developers/apis/image-delivery-api;
// unpic providers/contentstack.ts).
func contentstack() *Spec {
	return &Spec{
		Name: "contentstack",
		Doc:  "https://www.contentstack.com/docs/developers/apis/image-delivery-api",
		Domains: []string{
			"images.contentstack.io", "eu-images.contentstack.com",
			"azure-na-images.contentstack.com", "azure-eu-images.contentstack.com", "gcp-na-images.contentstack.com",
		},
		Extract: PathSource,
		Params: map[string]Param{
			"width":   Width("width"),
			"height":  Height("height"),
			"quality": Quality("quality"),
			"format":  Format("format"),
			"fit":     FitMap("fit", map[string]Fit{"bounds": FitInside, "crop": FitCover}),
			"auto":    Silent(),
			"disable": Func(func(st *State, v string) error {
				if v == "upscale" {
					st.SetEnlarge(false)
				}
				return nil
			}),
			"dpr":               DPR("dpr"),
			"blur":              Blur("blur", 1),
			"bg-color":          Background("bg-color", false),
			"bgcolor":           Background("bgcolor", false),
			"orient":            Unsupported(),
			"crop":              Unsupported(),
			"trim":              Unsupported(),
			"pad":               Unsupported(),
			"padding":           Unsupported(),
			"canvas":            Unsupported(),
			"overlay":           Unsupported(),
			"overlay-align":     Unsupported(),
			"overlay-repeat":    Unsupported(),
			"overlay-width":     Unsupported(),
			"overlay-height":    Unsupported(),
			"overlay-padding":   Unsupported(),
			"sharpen":           Unsupported(),
			"saturation":        Unsupported(),
			"contrast":          Unsupported(),
			"brightness":        Unsupported(),
			"resize-filter":     Unsupported(),
			"filter":            Unsupported(),
			"frame":             Unsupported(),
			"disposition":       Unsupported(),
			"environment":       KeepInSource(),
			"include_dimension": Unsupported(),
		},
		Fit:     FitInside,
		Enlarge: true,
	}
}
