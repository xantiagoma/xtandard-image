package translate

import (
	"net/url"
	"strings"
)

// queryNative maps single-argument imgproxy options to the query
// dialect's native keys (URL-DIALECTS §4); every other option goes into
// o=, which takes imgproxy option paths.
var queryNative = map[string]string{
	"q": "q", "f": "f", "dpr": "dpr", "bl": "blur", "sh": "sharpen", "pix": "pixelate",
	"rot": "rotate", "pg": "page", "pgs": "pages", "cb": "cb", "vts": "vts", "vtk": "vtk",
}

// emitQuery writes /?url=<source>&<native keys>&o=<imgproxy options>.
func emitQuery(e *emitter) (string, string, error) {
	v := url.Values{}
	v.Set("url", e.src.abs)
	paths := ipPaths(e.r, e.sep)
	var rest []string
	if len(paths) == 1 {
		var native []string
		for _, seg := range paths[0] {
			name, arg, _ := strings.Cut(seg, e.sep)
			key, ok := queryNative[name]
			switch {
			case ok && !strings.Contains(arg, e.sep):
				native = append(native, key+"="+url.QueryEscape(arg))
			case name == "rs" && simpleResize(arg, e.sep):
				native = append(native, resizeKeys(arg, e.sep)...)
			default:
				rest = append(rest, seg)
			}
		}
		q := "url=" + url.QueryEscape(e.src.abs)
		for _, n := range native {
			q += "&" + n
		}
		if len(rest) > 0 {
			q += "&o=" + url.QueryEscape(strings.Join(rest, "/"))
		}
		return "/", q, nil
	}
	parts := make([]string, 0, len(paths))
	for _, segs := range paths {
		parts = append(parts, strings.Join(segs, "/"))
	}
	v.Set("o", strings.Join(parts, "/-/"))
	return "/", "url=" + url.QueryEscape(e.src.abs) + "&o=" + url.QueryEscape(v.Get("o")), nil
}

// simpleResize reports rs arguments type:w:h[:enlarge] without extend.
func simpleResize(arg, sep string) bool {
	return len(strings.Split(arg, sep)) <= 4
}

// resizeKeys turns rs:type:w:h:enlarge into fit, w, h and enlarge.
func resizeKeys(arg, sep string) []string {
	a := strings.Split(arg, sep)
	var out []string
	get := func(i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	if t := get(0); t != "" {
		out = append(out, "fit="+t)
	}
	if w := get(1); w != "" {
		out = append(out, "w="+w)
	}
	if h := get(2); h != "" {
		out = append(out, "h="+h)
	}
	if el := get(3); el != "" {
		out = append(out, "enlarge="+el)
	}
	return out
}
