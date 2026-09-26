package simple

import (
	"net/url"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// hygraph is the Hygraph asset CDN (Filestack transformations):
//
//	https://<region>.graphassets.com/<env id>/[<task>=<k>:<v>,<k>:<v>/...][auto_image/]<handle>
//
// Each task argument is the parameter "<task>.<key>": resize.width,
// resize.height, resize.fit (clip fits inside, the default; crop covers;
// scale stretches; max fits inside without enlarging), resize.align,
// output.format, quality.value, blur.amount, sharpen.amount, rotate.deg.
// auto_image and compress leave the format to negotiation. The source is
// "<env id>/<handle>" (https://hygraph.com/docs/api-reference/assets/transformations;
// unpic providers/hygraph.ts).
func hygraph() *Spec {
	return &Spec{
		Name:    "hygraph",
		Doc:     "https://hygraph.com/docs/api-reference/assets/transformations",
		Domains: []string{".graphassets.com"},
		Extract: hygraphExtract,
		Params: map[string]Param{
			"resize.width":  Width("resize.width"),
			"resize.height": Height("resize.height"),
			"resize.fit": FitMap("resize.fit", map[string]Fit{
				"clip": FitInside, "crop": FitCover, "scale": FitFill, "max": FitScaleDown,
			}),
			"resize.align":   Gravity("resize.align"),
			"output.format":  Format("output.format"),
			"output.quality": Quality("output.quality"),
			"quality.value":  Quality("quality.value"),
			"blur.amount":    Blur("blur.amount", 1),
			"sharpen.amount": Sharpen("sharpen.amount", 0.5),
			"rotate.deg":     Rotate("rotate.deg", true),
			"auto_image":     Silent(),
			"compress":       Silent(),
		},
		Fit: FitInside,
	}
}

// hygraphExtract splits "/<env>/<tasks...>/<handle>" into the source and
// "<task>.<key>" parameters.
func hygraphExtract(path, rawQuery string) (*Extracted, error) {
	kvs, err := ParseQuery(rawQuery)
	if err != nil {
		return nil, err
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) < 2 || segs[0] == "" || segs[len(segs)-1] == "" {
		return nil, dialect.InvalidURL("missing environment or handle")
	}
	if len(segs) > maxParams {
		return nil, dialect.InvalidURL("too many parameters")
	}
	ex := &Extracted{Source: segs[0] + "/" + segs[len(segs)-1], Escaped: true, Params: kvs}
	for _, seg := range segs[1 : len(segs)-1] {
		seg, err := url.PathUnescape(seg)
		if err != nil {
			return nil, dialect.InvalidURL("invalid path escaping")
		}
		task, args, found := strings.Cut(seg, "=")
		if !found {
			ex.Params = append(ex.Params, KV{Key: task})
			continue
		}
		for arg := range strings.SplitSeq(args, ",") {
			k, v, _ := strings.Cut(arg, ":")
			ex.Params = append(ex.Params, KV{Key: task + "." + k, Value: v})
		}
		if len(ex.Params) > maxParams {
			return nil, dialect.InvalidURL("too many parameters")
		}
	}
	return ex, nil
}
