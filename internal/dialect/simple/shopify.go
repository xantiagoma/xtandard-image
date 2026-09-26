package simple

import (
	"regexp"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// shopify is the Shopify CDN:
//
//	https://cdn.shopify.com/s/files/<shop>/products/<name>.jpg?width=&height=&crop=center|top|bottom|left|right&pad_color=&format=
//	https://cdn.shopify.com/s/files/<shop>/products/<name>_<W>x<H>[_crop_<pos>].jpg   (legacy size suffix)
//
// A legacy "_WxH" suffix gives the size unless the query sets width or
// height; named sizes ("_medium") and a second extension are dropped from
// the source like unpic does. Without crop the image fits inside; crop
// covers at the position and pad_color letterboxes; Shopify never
// enlarges. v (the asset version) is dropped
// (https://shopify.dev/docs/api/liquid/filters/image_url; unpic
// providers/shopify.ts).
func shopify() *Spec {
	// unpic's shopifyRegex.
	re := regexp.MustCompile(`^(.+?)(?:_(?:(pico|icon|thumb|small|compact|medium|large|grande|original|master)|(\d*)x(\d*)))?(?:_crop_([a-z]+))?(\.[a-zA-Z]+)(\.png|\.jpg|\.webp|\.avif)?$`)
	return &Spec{
		Name:    "shopify",
		Doc:     "https://shopify.dev/docs/api/liquid/filters/image_url",
		Domains: []string{"cdn.shopify.com"},
		Extract: func(path, rawQuery string) (*Extracted, error) { return shopifyExtract(re, path, rawQuery) },
		Params: map[string]Param{
			"width":     Width("width"),
			"height":    Height("height"),
			"crop":      Func(shopifyCrop),
			"pad_color": Func(shopifyPad),
			"format":    Format("format"),
			"quality":   Quality("quality"),
			"v":         Silent(),
		},
		Fit: FitInside,
	}
}

// shopifyExtract strips the legacy size/crop suffix from the file name and
// turns it into width/height/crop parameters placed before the query's.
func shopifyExtract(re *regexp.Regexp, path, rawQuery string) (*Extracted, error) {
	kvs, err := ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	ex := &Extracted{Source: path, Escaped: true}
	dir, file := "", path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		dir, file = path[:i+1], path[i+1:]
	}
	m := re.FindStringSubmatch(file)
	if m == nil {
		ex.Params = kvs
		return ex, nil
	}
	ex.Source = dir + m[1] + m[6]
	hasSize := false
	for _, kv := range kvs {
		hasSize = hasSize || kv.Key == "width" || kv.Key == "height"
	}
	var suffix []KV
	if m[3] != "" && m[4] != "" && !hasSize {
		suffix = append(suffix, KV{"width", m[3]}, KV{"height", m[4]})
	}
	if m[5] != "" {
		suffix = append(suffix, KV{"crop", m[5]})
	}
	suffix = append(suffix, kvs...)
	ex.Params = suffix
	return ex, nil
}

// shopifyCrop crops to the box at the position.
func shopifyCrop(st *State, v string) error {
	g, ok := parseGravity(v)
	if !ok {
		return dialect.InvalidArgument("crop", v, "center, top, bottom, left or right")
	}
	st.Fit = FitCover
	st.Pipeline.Gravity = transform.Gravity{Type: g}
	return nil
}

// shopifyPad letterboxes with the colour.
func shopifyPad(st *State, v string) error {
	if err := Background("pad_color", false).Apply(st, v); err != nil {
		return err
	}
	st.Fit = FitPad
	return nil
}
