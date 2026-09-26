package svg

import (
	"strings"
	"unicode/utf8"
)

type cssVerdict uint8

const (
	cssUnchanged cssVerdict = iota
	cssRewritten
	cssRejected
)

// sanitizeCSS neutralises external references in a CSS fragment (a <style>
// body or an attribute value):
//
//   - url(x) and src(x) whose target is not a same-document fragment (or an
//     allowed data:image/* URI) become the keyword none, which is a valid
//     value for fill, stroke, filter, clip-path, mask, marker and
//     background-image, so the declaration degrades instead of fetching;
//   - image(), image-set(), cross-fade() and element(), including
//     vendor-prefixed forms, take string URLs and are replaced by none;
//   - @import rules are replaced by a single space.
//
// CSS escapes can spell any of those (\75 rl(…), @\69mport), and rewriting
// escaped text faithfully is not worth the risk: if the fragment contains a
// backslash it is decoded first and rejected as a whole when the decoded form
// holds anything that would be rewritten.
func sanitizeCSS(css string, allowData bool) (string, cssVerdict) {
	if strings.IndexByte(css, '\\') >= 0 {
		if _, changed := rewriteCSS(cssUnescape(css), allowData); changed {
			return "", cssRejected
		}
		return css, cssUnchanged
	}
	out, changed := rewriteCSS(css, allowData)
	if !changed {
		return css, cssUnchanged
	}
	return out, cssRewritten
}

// rewriteCSS scans css for identifiers at token boundaries and rewrites the
// dangerous functions and at-rules. It deliberately does not skip strings or
// comments: text inside them may be rewritten too, which only ever removes
// content. The output is built lazily; the common case allocates nothing.
func rewriteCSS(css string, allowData bool) (string, bool) {
	var b strings.Builder
	changed := false
	copied := 0 // css[:copied] has been written to b
	replace := func(from, to int, with string) {
		if !changed {
			b.Grow(len(css))
			changed = true
		}
		b.WriteString(css[copied:from])
		b.WriteString(with)
		copied = to
	}
	for i := 0; i < len(css); {
		c := css[i]
		switch {
		case c == '@':
			j := scanIdent(css, i+1)
			if strings.EqualFold(css[i+1:j], "import") {
				end := findRuleEnd(css, j)
				replace(i, end, " ")
				i = end
				continue
			}
			i = j
		case isIdentStart(c) && (i == 0 || !isIdentChar(css[i-1])):
			j := scanIdent(css, i)
			if j < len(css) && css[j] == '(' {
				switch stripVendor(strings.ToLower(css[i:j])) {
				case "url", "src":
					end, target := scanURLArg(css, j+1)
					t := trimURL(target)
					if t != "" && !isFragment(t) && (!allowData || !isDataImage(t)) {
						replace(i, end, "none")
					}
					i = end
					continue
				case "image", "image-set", "cross-fade", "element":
					end := findClose(css, j+1)
					replace(i, end, "none")
					i = end
					continue
				}
			}
			i = j
		default:
			i++
		}
	}
	if !changed {
		return css, false
	}
	b.WriteString(css[copied:])
	return b.String(), true
}

func isIdentStart(c byte) bool {
	return c == '-' || c == '_' || c >= 0x80 || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func scanIdent(s string, i int) int {
	for i < len(s) && isIdentChar(s[i]) {
		i++
	}
	return i
}

// stripVendor turns "-webkit-image-set" into "image-set".
func stripVendor(name string) string {
	if len(name) > 1 && name[0] == '-' && name[1] != '-' {
		if k := strings.IndexByte(name[1:], '-'); k >= 0 {
			return name[k+2:]
		}
	}
	return name
}

// scanURLArg parses the argument of url( or src( starting just after the
// parenthesis and returns the index just past the closing parenthesis (or
// len(s) when it is missing) and the raw target.
func scanURLArg(s string, i int) (int, string) {
	for i < len(s) && isCSSSpace(s[i]) {
		i++
	}
	if i < len(s) && (s[i] == '"' || s[i] == '\'') {
		q := s[i]
		k := strings.IndexByte(s[i+1:], q)
		if k < 0 {
			return len(s), s[i+1:]
		}
		target := s[i+1 : i+1+k]
		return findClose(s, i+2+k), target
	}
	k := strings.IndexByte(s[i:], ')')
	if k < 0 {
		return len(s), s[i:]
	}
	return i + k + 1, s[i : i+k]
}

// findClose returns the index just past the parenthesis that closes a function
// whose arguments start at i, honouring nesting and quoted strings, or len(s).
func findClose(s string, i int) int {
	depth := 1
	for i < len(s) {
		switch c := s[i]; c {
		case '"', '\'':
			k := strings.IndexByte(s[i+1:], c)
			if k < 0 {
				return len(s)
			}
			i += k + 2
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
		i++
	}
	return len(s)
}

// findRuleEnd returns the index just past the ';' ending an at-rule prelude
// that starts at i, or the index of a '{' or '}' that ends it, or len(s).
func findRuleEnd(s string, i int) int {
	for i < len(s) {
		switch c := s[i]; c {
		case '"', '\'':
			k := strings.IndexByte(s[i+1:], c)
			if k < 0 {
				return len(s)
			}
			i += k + 2
			continue
		case '(':
			i = findClose(s, i+1)
			continue
		case ';':
			return i + 1
		case '{', '}':
			return i
		}
		i++
	}
	return len(s)
}

func isCSSSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// cssUnescape decodes CSS escapes (CSS Syntax Level 3 §4.3.7): a backslash
// followed by 1-6 hex digits and one optional whitespace, a backslash-newline
// (dropped), or a backslash followed by any other character (that character).
func cssUnescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		if isHex(s[i]) {
			r := rune(0)
			n := 0
			for n < 6 && i < len(s) && isHex(s[i]) {
				r = r<<4 | rune(hexVal(s[i]))
				i++
				n++
			}
			if i < len(s) && isCSSSpace(s[i]) {
				if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
					i++
				}
				i++
			}
			if r == 0 || r > utf8.MaxRune || (r >= 0xD800 && r <= 0xDFFF) {
				r = utf8.RuneError
			}
			b.WriteRune(r)
			continue
		}
		if s[i] == '\n' || s[i] == '\r' || s[i] == '\f' {
			i++
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'f')
}

func hexVal(c byte) byte {
	if c <= '9' {
		return c - '0'
	}
	return (c | 0x20) - 'a' + 10
}
