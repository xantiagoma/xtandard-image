package negotiate

import (
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Key returns a canonical fingerprint of l for the result-cache key
// (internal/cache ResultKey.Format): two lists with equal keys make Decide
// return the same decision for every request, config and source, because
// Decide reads the Accept header only through Quality and Explicit of the
// output formats' media types.
//
// The form is, for each concrete output format in enum order,
// "<format>=<Quality>/<Explicit>" joined by ";", e.g.
// "jpeg=1000/0;png=1000/0;webp=1000/1000;…". Differently written but
// equivalent headers ("image/webp,*/*" and "*/*;q=1, image/webp") share a
// key, so they share cache entries.
func (l AcceptList) Key() string {
	b := make([]byte, 0, 256)
	for f := transform.FormatJPEG; f < transform.FormatBest; f++ {
		mt, ok := mediaTypeOf(f)
		if !ok || f == transform.FormatJSON {
			continue
		}
		if len(b) > 0 {
			b = append(b, ';')
		}
		b = append(b, f.String()...)
		b = append(b, '=')
		b = strconv.AppendInt(b, int64(l.Quality(mt.typ, mt.sub)), 10)
		b = append(b, '/')
		b = strconv.AppendInt(b, int64(l.Explicit(mt.typ, mt.sub)), 10)
	}
	return string(b)
}
