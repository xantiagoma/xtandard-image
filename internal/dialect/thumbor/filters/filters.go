// Package filters parses thumbor's filter grammar (docs/URL-DIALECTS.md §10),
// shared by the thumbor dialect and Storyblok's image service:
//
//	filters:name(args)[:name(args)...]
//
// It is pure syntax: which filters exist and what their arguments mean is
// up to the dialect. The grammar follows thumbor (thumbor/filters/__init__.py
// FiltersFactory and BaseFilter, libthumbor url.py; MIT):
//
//   - A filter is a name made of letters, digits and "_" followed by its
//     arguments in parentheses; filters are separated by ":".
//   - Arguments are separated by ","; whitespace around each is ignored and
//     a value in single quotes may contain "," and ")".
//   - Parentheses nest, so a filter argument may itself contain a thumbor
//     path with filters (watermark(/unsafe/filters:quality(5)/w.png,0,0,0)).
//
// thumbor finds the end of the filters segment with a lazy regular
// expression (the first ")/"); [Cut] tracks nesting instead and falls back
// to thumbor's rule when the parentheses do not balance, so both agree on
// every URL whose parentheses balance and whose arguments contain no ")/".
package filters

import (
	"errors"
	"strings"
)

// Prefix introduces the filters segment.
const Prefix = "filters:"

// MaxFilters bounds the number of filters in one list.
const MaxFilters = 64

// ErrSyntax is returned for a filter list that does not follow the grammar.
var ErrSyntax = errors.New("filters: malformed filter list")

// ErrTooMany is returned for more than MaxFilters filters.
var ErrTooMany = errors.New("filters: too many filters")

// Filter is one name(args) call, exactly as written in the URL (arguments
// are not unescaped).
type Filter struct {
	Name string
	Args string // text between the outer parentheses
}

// Cut splits s, which must start with Prefix, into the filter list (without
// the prefix) and the rest of the path after the "/" that ends the list.
// The list always ends with ")". When the list runs to the end of s (a
// trailing filters segment, as Storyblok writes it) rest is "". ok is
// false when s does not start with a filters segment.
func Cut(s string) (list, rest string, ok bool) {
	body, found := strings.CutPrefix(s, Prefix)
	if !found {
		return "", "", false
	}
	depth := 0
	quoted := false
	for i := range len(body) {
		switch c := body[i]; {
		case quoted:
			quoted = c != '\''
		case c == '\'' && depth > 0:
			quoted = true
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth < 0 {
				return cutLazy(body)
			}
			if depth > 0 {
				continue
			}
			if i+1 == len(body) {
				return body, "", true
			}
			if body[i+1] == '/' {
				return body[:i+1], body[i+2:], true
			}
		case c == '/' && depth == 0:
			// A "/" outside any argument list: not a filters segment unless
			// thumbor's lazy rule finds one.
			return cutLazy(body)
		}
	}
	return cutLazy(body)
}

// cutLazy is thumbor's rule: the list runs to the first ")/".
func cutLazy(body string) (list, rest string, ok bool) {
	if i := strings.Index(body, ")/"); i > 0 {
		return body[:i+1], body[i+2:], true
	}
	if len(body) > 1 && body[len(body)-1] == ')' {
		return body, "", true
	}
	return "", "", false
}

// Parse parses a filter list such as "format(webp):quality(80)" (without
// Prefix). Empty argument lists are allowed ("grayscale()"); a filter
// without parentheses, an empty name or unbalanced parentheses is
// ErrSyntax.
func Parse(list string) ([]Filter, error) {
	if list == "" {
		return nil, ErrSyntax
	}
	var out []Filter
	for list != "" {
		if len(out) == MaxFilters {
			return nil, ErrTooMany
		}
		n := 0
		for n < len(list) && isNameByte(list[n]) {
			n++
		}
		if n == 0 || n == len(list) || list[n] != '(' {
			return nil, ErrSyntax
		}
		end := closing(list, n)
		if end < 0 {
			return nil, ErrSyntax
		}
		out = append(out, Filter{Name: list[:n], Args: list[n+1 : end]})
		list = list[end+1:]
		if list == "" {
			break
		}
		if list[0] != ':' || len(list) == 1 {
			return nil, ErrSyntax
		}
		list = list[1:]
	}
	return out, nil
}

// closing returns the index of the ")" matching the "(" at open, or -1.
// Single-quoted text inside the arguments is opaque.
func closing(s string, open int) int {
	depth := 0
	quoted := false
	for i := open; i < len(s); i++ {
		switch c := s[i]; {
		case quoted:
			quoted = c != '\''
		case c == '\'':
			quoted = true
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	// An unterminated quote: retry treating quotes as ordinary bytes, as
	// thumbor's regular expressions do.
	if quoted {
		depth = 0
		for i := open; i < len(s); i++ {
			switch s[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return i
				}
			}
		}
	}
	return -1
}

func isNameByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Args splits a filter's argument text on "," outside parentheses and
// single quotes, trims spaces and tabs around each argument and removes one
// pair of surrounding single quotes (thumbor BaseFilter.String). "" has no
// arguments; "a," has two, the second empty.
func Args(args string) []string {
	if strings.TrimSpace(args) == "" {
		return nil
	}
	out := make([]string, 0, 1+strings.Count(args, ","))
	depth := 0
	quoted := false
	start := 0
	for i := range len(args) {
		switch c := args[i]; {
		case quoted:
			quoted = c != '\''
		case c == '\'' && strings.TrimSpace(args[start:i]) == "":
			quoted = true
		case c == '(':
			depth++
		case c == ')':
			depth = max(0, depth-1)
		case c == ',' && depth == 0:
			out = append(out, unquote(args[start:i]))
			start = i + 1
		}
	}
	return append(out, unquote(args[start:]))
}

func unquote(s string) string {
	s = strings.Trim(s, " \t")
	if len(s) >= 3 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// Color is an 8-bit RGBA colour.
type Color struct {
	R, G, B, A uint8
}

// ParseColor parses a colour the way thumbor's PIL engine does for fill()
// and background_color(): a CSS3 keyword (case-insensitive), "transparent",
// or hexadecimal "rgb", "rgba", "rrggbb" or "rrggbbaa" with an optional
// leading "#".
func ParseColor(s string) (Color, bool) {
	if len(s) > 32 {
		return Color{}, false
	}
	lower := strings.ToLower(s)
	if lower == "transparent" {
		return Color{}, true
	}
	if r, g, b, ok := colorName(lower); ok {
		return Color{R: r, G: g, B: b, A: 255}, true
	}
	h := strings.TrimPrefix(lower, "#")
	var v [8]byte
	for i := range len(h) {
		d, ok := hexDigit(h[i])
		if !ok || i >= len(v) {
			return Color{}, false
		}
		v[i] = d
	}
	switch len(h) {
	case 3:
		return Color{R: v[0] * 17, G: v[1] * 17, B: v[2] * 17, A: 255}, true
	case 4:
		return Color{R: v[0] * 17, G: v[1] * 17, B: v[2] * 17, A: v[3] * 17}, true
	case 6:
		return Color{R: v[0]<<4 | v[1], G: v[2]<<4 | v[3], B: v[4]<<4 | v[5], A: 255}, true
	case 8:
		return Color{R: v[0]<<4 | v[1], G: v[2]<<4 | v[3], B: v[4]<<4 | v[5], A: v[6]<<4 | v[7]}, true
	}
	return Color{}, false
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}
