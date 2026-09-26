package simple

import (
	"math"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// imageengine is ImageEngine directives:
//
//	https://<token>.cdn.imgeng.in/<path>?imgeng=/w_300/h_200/f_webp/cmpr_20/m_cropbox
//
// Directives are "<name>_<value>" separated by "/"; every other query
// parameter belongs to the source. m: cropbox covers, box fits inside (the
// default), letterbox pads, stretch stretches, outside covers without
// cropping. cmpr (compression 0..99) becomes quality 100-cmpr; quality_
// (written by unpic) is taken as is. s sharpens, r rotates, meta keeps
// metadata, dl downloads; pass, pc, cr and maxdpr are not applied
// (https://support.imageengine.io/hc/en-us/articles/360058880672-Directives;
// unpic providers/imageengine.ts).
func imageengine() *Spec {
	return &Spec{
		Name:    "imageengine",
		Doc:     "https://support.imageengine.io/hc/en-us/articles/360058880672-Directives",
		Domains: []string{".imgeng.in"},
		Extract: imageengineExtract,
		Params: map[string]Param{
			"w":       Width("w"),
			"h":       Height("h"),
			"f":       Format("f"),
			"quality": Quality("quality"),
			"cmpr":    Func(imageengineCompression),
			"m": FitMap("m", map[string]Fit{
				"cropbox": FitCover, "box": FitInside, "letterbox": FitPad, "stretch": FitFill, "outside": FitOutside,
			}),
			"s":      Sharpen("s", 0.05),
			"r":      Rotate("r", true),
			"meta":   Bool("meta", func(st *State, v bool) { strip := !v; st.Request.Output.StripMetadata = &strip }),
			"dl":     Bool("dl", func(st *State, v bool) { st.Request.Delivery.Attachment = &v }),
			"pass":   Unsupported(),
			"pc":     Unsupported(),
			"cr":     Unsupported(),
			"maxdpr": Unsupported(),
			"in":     Unsupported(),
		},
		Fit: FitInside,
	}
}

// imageengineExtract reads the imgeng directives; the rest of the query is
// part of the source.
func imageengineExtract(path, rawQuery string) (*Extracted, error) {
	kvs, err := ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	ex := &Extracted{Source: path, Escaped: true}
	var keep []string
	for part := range strings.SplitSeq(rawQuery, "&") {
		if k, _, _ := strings.Cut(part, "="); part != "" && k != "imgeng" {
			keep = append(keep, part)
		}
	}
	if len(keep) > 0 {
		ex.Source += "?" + strings.Join(keep, "&")
	}
	for _, kv := range kvs {
		if kv.Key != "imgeng" {
			continue
		}
		for d := range strings.SplitSeq(kv.Value, "/") {
			if d == "" {
				continue
			}
			k, v, _ := strings.Cut(d, "_")
			ex.Params = append(ex.Params, KV{Key: k, Value: v})
			if len(ex.Params) > maxParams {
				return nil, dialect.InvalidURL("too many parameters")
			}
		}
	}
	return ex, nil
}

// imageengineCompression maps cmpr (0..99, higher = smaller) to quality.
func imageengineCompression(st *State, v string) error {
	f, ok := parseNumber(v)
	if !ok || f < 0 || f > 99 {
		return dialect.InvalidArgument("cmpr", v, "number 0..99")
	}
	st.Request.Output.Quality = 100 - int(math.Round(f))
	return nil
}
