package simple

import "strings"

// supabase is Supabase Storage image transformations:
//
//	/storage/v1/render/image/public/<bucket>/<path>?width=&height=&quality=&resize=cover|contain|fill&format=origin
//	/storage/v1/object/public/<bucket>/<path>   (the untransformed object; options still apply)
//
// The source is the public object (/storage/v1/object/public/...) on the
// bound source. resize contain fits inside, fill stretches, cover (the
// default) covers; format=origin keeps the source format, anything else is
// negotiated (https://supabase.com/docs/guides/storage/serving/image-transformations;
// unpic providers/supabase.ts).
func supabase() *Spec {
	return &Spec{
		Name:     "supabase",
		Doc:      "https://supabase.com/docs/guides/storage/serving/image-transformations",
		Prefixes: []string{"/storage/v1/render/image/public/", "/storage/v1/object/public/"},
		Domains:  []string{".supabase.co"},
		Extract: func(path, rawQuery string) (*Extracted, error) {
			if rest, ok := strings.CutPrefix(path, "/storage/v1/render/image/public/"); ok {
				path = "/storage/v1/object/public/" + rest
			}
			return PathSource(path, rawQuery)
		},
		Params: map[string]Param{
			"width":   Width("width"),
			"height":  Height("height"),
			"quality": Quality("quality"),
			"format":  Format("format"),
			"resize":  FitMap("resize", map[string]Fit{"cover": FitCover, "contain": FitInside, "fill": FitFill}),
		},
		Fit: FitCover,
	}
}
