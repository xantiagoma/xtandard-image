package simple

// appwrite is Appwrite Storage file previews:
//
//	/v1/storage/buckets/<bucket>/files/<file>/preview?project=&width=&height=&gravity=&quality=&rotation=&background=&output=
//
// With both sides the image is cropped at gravity (Appwrite's default
// center). project stays on the source (the origin needs it); borders,
// border radius and opacity are not applied
// (https://appwrite.io/docs/products/storage/images; unpic
// providers/appwrite.ts).
func appwrite() *Spec {
	return &Spec{
		Name:     "appwrite",
		Doc:      "https://appwrite.io/docs/products/storage/images",
		Prefixes: []string{"/v1/storage/buckets/"},
		Domains:  []string{"cloud.appwrite.io", ".cloud.appwrite.io"},
		Extract:  PathSource,
		Params: map[string]Param{
			"project":      KeepInSource(),
			"width":        Width("width"),
			"height":       Height("height"),
			"quality":      Quality("quality"),
			"output":       Format("output"),
			"gravity":      Gravity("gravity"),
			"rotation":     Rotate("rotation", true),
			"background":   Background("background", false),
			"borderWidth":  Unsupported(),
			"borderColor":  Unsupported(),
			"borderRadius": Unsupported(),
			"opacity":      Unsupported(),
		},
		Fit: FitCover,
	}
}
