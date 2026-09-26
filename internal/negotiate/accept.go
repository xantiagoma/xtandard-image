package negotiate

import (
	"net/http"
	"strconv"
	"strings"
)

// MaxAcceptRanges caps how many media ranges ParseAccept keeps. Real browsers
// send fewer than ten; the cap bounds work on hostile headers (the header
// itself is already bounded by http.Server.MaxHeaderBytes).
const MaxAcceptRanges = 64

// qScale is the fixed-point scale of q-values: RFC 9110 §12.4.2 allows at
// most three decimals, so q is stored as an integer 0..1000.
const qScale = 1000

// Param is one media-type parameter of a media range. Name is lowercase;
// Value is unquoted and keeps its case.
type Param struct {
	Name, Value string
}

// MediaRange is one element of an Accept header (RFC 9110 §12.5.1).
type MediaRange struct {
	Type    string  // lowercase; "*" for */*
	Subtype string  // lowercase; "*" for type/* and */*
	Params  []Param // media-type parameters before the weight; nil when none
	Q       int     // weight × 1000, 0..1000; 1000 when absent
}

// String renders r in canonical form: lowercase type, parameters in order, and
// ";q=" only when the weight is not 1.
func (r MediaRange) String() string {
	var b strings.Builder
	b.WriteString(r.Type)
	b.WriteByte('/')
	b.WriteString(r.Subtype)
	for _, p := range r.Params {
		b.WriteByte(';')
		b.WriteString(p.Name)
		b.WriteByte('=')
		if isToken(p.Value) {
			b.WriteString(p.Value)
		} else {
			writeQuoted(&b, p.Value)
		}
	}
	if r.Q != qScale {
		b.WriteString(";q=")
		writeQ(&b, r.Q)
	}
	return b.String()
}

// AcceptList is a parsed Accept header in header order. An empty list means
// the header was absent, empty or entirely invalid, which RFC 9110 §12.5.1
// treats as "any media type is acceptable".
type AcceptList []MediaRange

// String renders the list in canonical form; ParseAccept(l.String()) equals l
// for any l returned by ParseAccept.
func (l AcceptList) String() string {
	parts := make([]string, len(l))
	for i, r := range l {
		parts[i] = r.String()
	}
	return strings.Join(parts, ", ")
}

// ParseAcceptHeader parses every Accept field line of h as one list (field
// lines combine with commas, RFC 9110 §5.3).
func ParseAcceptHeader(h http.Header) AcceptList {
	vals := h.Values("Accept")
	switch len(vals) {
	case 0:
		return nil
	case 1:
		return ParseAccept(vals[0])
	}
	return ParseAccept(strings.Join(vals, ","))
}

// ParseAccept parses an Accept field value. Parsing is lenient per element:
// a malformed media range, a malformed parameter or an invalid q-value drops
// that element and parsing continues with the next one. Type, subtype and
// parameter names are lowercased. Parameters after the weight (RFC 7231
// accept-ext) are ignored. At most MaxAcceptRanges elements are kept.
func ParseAccept(s string) AcceptList {
	if trimOWS(s) == "" {
		return nil
	}
	l := make(AcceptList, 0, min(strings.Count(s, ",")+1, MaxAcceptRanges))
	for s != "" && len(l) < MaxAcceptRanges {
		var elem string
		elem, s = nextElement(s)
		if r, ok := parseRange(elem); ok {
			l = append(l, r)
		}
	}
	if len(l) == 0 {
		return nil
	}
	return l
}

// nextElement splits s at the first comma that is not inside a quoted string.
func nextElement(s string) (elem, rest string) {
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inQuote && c == '\\':
			i++
		case c == '"':
			inQuote = !inQuote
		case !inQuote && c == ',':
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func parseRange(s string) (MediaRange, bool) {
	s = trimOWS(s)
	r := MediaRange{Q: qScale}
	var ok bool
	r.Type, s, ok = cutToken(s)
	if !ok || s == "" || s[0] != '/' {
		return MediaRange{}, false
	}
	r.Subtype, s, ok = cutToken(s[1:])
	if !ok || (r.Type == "*" && r.Subtype != "*") {
		return MediaRange{}, false
	}
	r.Type = strings.ToLower(r.Type)
	r.Subtype = strings.ToLower(r.Subtype)
	for {
		s = trimOWS(s)
		if s == "" {
			return r, true
		}
		if s[0] != ';' {
			return MediaRange{}, false
		}
		s = trimOWS(s[1:])
		if s == "" {
			return r, true // a trailing ";" is tolerated (seen in the wild)
		}
		var name, value string
		name, s, ok = cutToken(s)
		if !ok || s == "" || s[0] != '=' {
			return MediaRange{}, false
		}
		name = strings.ToLower(name)
		value, s, ok = cutValue(s[1:])
		if !ok {
			return MediaRange{}, false
		}
		if name == "q" {
			// The weight ends the media range; anything after it is
			// accept-ext and carries no meaning for negotiation.
			r.Q, ok = parseQ(value)
			return r, ok
		}
		r.Params = append(r.Params, Param{Name: name, Value: value})
	}
}

// parseQ parses an RFC 9110 qvalue: "0" [ "." 0*3DIGIT ] / "1" [ "." 0*3("0") ].
func parseQ(s string) (int, bool) {
	if s == "" || len(s) > 5 || (s[0] != '0' && s[0] != '1') {
		return 0, false
	}
	q := int(s[0]-'0') * qScale
	if len(s) == 1 {
		return q, true
	}
	if s[1] != '.' {
		return 0, false
	}
	scale := qScale / 10
	for i := 2; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		q += int(c-'0') * scale
		scale /= 10
	}
	if q > qScale {
		return 0, false
	}
	return q, true
}

func writeQ(b *strings.Builder, q int) {
	switch q {
	case qScale:
		b.WriteByte('1')
		return
	case 0:
		b.WriteByte('0')
		return
	}
	// q+1000 is 1001..1999: its last three digits are q zero-padded.
	digits := strconv.Itoa(q + qScale)[1:]
	b.WriteString("0.")
	b.WriteString(strings.TrimRight(digits, "0"))
}

// cutToken returns the leading RFC 9110 token of s.
func cutToken(s string) (tok, rest string, ok bool) {
	i := 0
	for i < len(s) && isTChar(s[i]) {
		i++
	}
	return s[:i], s[i:], i > 0
}

// cutValue returns a leading token or quoted-string (unquoted).
func cutValue(s string) (val, rest string, ok bool) {
	if s == "" || s[0] != '"' {
		return cutToken(s)
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			return b.String(), s[i+1:], true
		case '\\':
			i++
			if i == len(s) || !isQuotedOctet(s[i]) {
				return "", "", false
			}
			c = s[i]
		default:
			if !isQuotedOctet(c) {
				return "", "", false
			}
		}
		b.WriteByte(c)
	}
	return "", "", false
}

func writeQuoted(b *strings.Builder, v string) {
	b.WriteByte('"')
	for i := range len(v) {
		if c := v[i]; c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(v[i])
	}
	b.WriteByte('"')
}

func trimOWS(s string) string {
	for s != "" && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for s != "" && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isTChar(s[i]) {
			return false
		}
	}
	return true
}

// isTChar reports whether c is an RFC 9110 tchar.
func isTChar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// isQuotedOctet accepts HTAB, SP, VCHAR and obs-text: every octet allowed
// inside a quoted-string, escaped or not.
func isQuotedOctet(c byte) bool {
	return c == '\t' || c == ' ' || (c >= 0x21 && c != 0x7f)
}

// Quality returns the weight (0..1000) the list gives the media type typ/sub
// without parameters, using the most specific matching range (RFC 9110
// §12.5.1): exact type/subtype, then type/*, then */*. Ranges that carry
// media-type parameters never match a parameterless representation. Among
// equally specific ranges the first one wins. An empty list accepts everything
// (1000); a non-empty list with no matching range gives 0. typ and sub must be
// lowercase.
func (l AcceptList) Quality(typ, sub string) int {
	if len(l) == 0 {
		return qScale
	}
	best, bestRank := 0, 0
	for i := range l {
		r := &l[i]
		if len(r.Params) > 0 {
			continue
		}
		rank := 0
		switch {
		case r.Type == typ && r.Subtype == sub:
			rank = 3
		case r.Type == typ && r.Subtype == "*":
			rank = 2
		case r.Type == "*":
			rank = 1
		}
		if rank > bestRank {
			best, bestRank = r.Q, rank
		}
	}
	return best
}

// Explicit returns the weight of the first range that names typ/sub exactly
// (no wildcard, no parameters), or 0 when there is none. Modern formats are
// only negotiated on an explicit mention: "*/*" and "image/*" do not imply
// AVIF, WebP or JPEG XL support (FORMATS §6.1).
func (l AcceptList) Explicit(typ, sub string) int {
	for i := range l {
		r := &l[i]
		if len(r.Params) == 0 && r.Type == typ && r.Subtype == sub {
			return r.Q
		}
	}
	return 0
}
