package wsrv

import (
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/security/sign"
)

// maxParams bounds the number of query parameters looked at; wsrv URLs
// use a few dozen at most.
const maxParams = 256

// params is the decoded query with wsrv's lookup semantics
// (.refs/weserv-images src/api/parsers/query.cpp): synonyms are folded
// into their canonical key, the FIRST occurrence of a key wins (the
// upstream map uses emplace), a key without "=" has the empty value, and
// compound keys (mod, sharp, crop, a=focal-x-y, fit=squaredown) expand into
// the keys they set, again without overwriting.
type params struct {
	m       map[string]string
	unknown []string // unknown keys in query order, for Ignored / strict mode
	sig     bool     // xi_sig present
}

// canonicalKey folds wsrv's synonyms (query.cpp synonym_map plus the
// deprecated nginx errorredirect).
func canonicalKey(k string) string {
	switch k {
	case "shape":
		return "mask"
	case "strim":
		return "mtrim"
	case "or":
		return "ro"
	case "t":
		return "fit"
	case "bri":
		return "mod"
	case "pages":
		return "n"
	case "width":
		return "w"
	case "height":
		return "h"
	case "align":
		return "a"
	case "level":
		return "l"
	case "quality":
		return "q"
	case "lossless":
		return "ll"
	}
	return k
}

// knownKey reports whether k (canonical) is a wsrv parameter.
func knownKey(k string) bool {
	switch k {
	case "url", "default", "errorredirect", "filename", "encoding", "maxage",
		"w", "h", "dpr", "fit", "we", "crop", "cx", "cy", "cw", "ch", "precrop",
		"a", "fpx", "fpy", "mask", "mtrim", "mbg", "ro", "flip", "flop",
		"mod", "sat", "hue", "con", "gam", "sharp", "sharpf", "sharpj",
		"trim", "tbg", "blur", "filt", "start", "stop", "bg", "cbg", "rbg", "tint",
		"q", "l", "output", "il", "ll", "af", "page", "n", "loop", "delay", "fsol":
		return true
	}
	return false
}

// parseParams decodes rawQuery. Decoding is lenient like nginx's: a "%"
// that does not start a valid escape is kept literally, so "w=50%" works.
func parseParams(rawQuery string) *params {
	p := &params{m: make(map[string]string, 8)}
	n := 0
	for part := range strings.SplitSeq(rawQuery, "&") {
		if part == "" {
			continue
		}
		if n++; n > maxParams {
			break
		}
		k, v, _ := strings.Cut(part, "=")
		key := unescape(k)
		if key == sign.XIParam {
			p.sig = true
			continue
		}
		key = canonicalKey(key)
		if !knownKey(key) {
			p.unknown = append(p.unknown, key)
			continue
		}
		p.add(key, unescape(v))
	}
	return p
}

// set stores v under k unless k is already set (first wins).
func (p *params) set(k, v string) {
	if _, ok := p.m[k]; !ok {
		p.m[k] = v
	}
}

// get returns the value of k and whether it is present.
func (p *params) get(k string) (string, bool) {
	v, ok := p.m[k]
	return v, ok
}

// has reports whether k is present.
func (p *params) has(k string) bool {
	_, ok := p.m[k]
	return ok
}

// add stores one parameter, expanding the compound ones.
func (p *params) add(k, v string) {
	switch k {
	case "mod":
		// brightness, saturation, hue
		for i, t := range tokens(v, 3) {
			p.set([...]string{"mod.b", "sat", "hue"}[i], t)
		}
		p.set(k, v)
	case "sharp":
		t := tokens(v, 3)
		if len(t) <= 1 {
			p.set("sharp", v)
			return
		}
		for i, s := range t {
			p.set([...]string{"sharpf", "sharpj", "sharp"}[i], s)
		}
	case "crop": // deprecated: w,h,x,y
		if t := tokens(v, 4); len(t) == 4 {
			p.set("cw", t[0])
			p.set("ch", t[1])
			p.set("cx", t[2])
			p.set("cy", t[3])
		}
		p.set(k, v)
	case "a":
		p.set(k, v)
		// Deprecated a=focal-X-Y / crop-X-Y with percentages.
		if isFocal(v) {
			if _, rest, ok := strings.Cut(v, "-"); ok {
				xy := strings.FieldsFunc(rest, func(r rune) bool { return r == '-' })
				for i, s := range xy[:min(len(xy), 2)] {
					if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 100 {
						p.set([...]string{"fpx", "fpy"}[i], strconv.FormatFloat(float64(n)/100, 'f', -1, 64))
					}
				}
			}
		}
	case "fit":
		// Deprecated values that also mean "without enlargement".
		if v == "fit" || v == "squaredown" {
			p.set("we", "1")
		}
		p.set(k, v)
	case "errorredirect":
		p.set("default", v)
	default:
		p.set(k, v)
	}
}

// tokens splits v on "," skipping empty tokens, at most n of them (the
// upstream tokenizer).
func tokens(v string, n int) []string {
	t := strings.FieldsFunc(v, func(r rune) bool { return r == ',' })
	return t[:min(len(t), n)]
}

// unescape decodes %XX escapes and "+" leniently.
func unescape(s string) string {
	if !strings.ContainsAny(s, "%+") {
		return s
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '+':
			b = append(b, ' ')
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b = append(b, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
		default:
			b = append(b, c)
		}
	}
	return string(b)
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
