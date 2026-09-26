package svg

import (
	"bytes"
)

// SniffLen is how much of the document IsSVG looks at.
const SniffLen = 4 << 10

// IsSVG reports whether head, the first bytes of a document, looks like an SVG
// document: after an optional UTF-8 BOM, whitespace, an XML declaration,
// comments, processing instructions and a DOCTYPE, the first element within
// the first SniffLen bytes must be svg (with or without a namespace prefix).
// It only sniffs; Sanitize still rejects DOCTYPEs and malformed input.
func IsSVG(head []byte) bool {
	b := head[:min(len(head), SniffLen)]
	b = bytes.TrimPrefix(b, []byte(utf8BOM))
	for {
		b = bytes.TrimLeft(b, " \t\r\n")
		switch {
		case bytes.HasPrefix(b, []byte("<?")):
			i := bytes.Index(b, []byte("?>"))
			if i < 0 {
				return false
			}
			b = b[i+2:]
		case bytes.HasPrefix(b, []byte("<!--")):
			i := bytes.Index(b[4:], []byte("-->"))
			if i < 0 {
				return false
			}
			b = b[4+i+3:]
		case bytes.HasPrefix(b, []byte("<!")):
			i := directiveEnd(b)
			if i < 0 {
				return false
			}
			b = b[i:]
		case bytes.HasPrefix(b, []byte("<")):
			return isSVGStartTag(b[1:])
		default:
			return false
		}
	}
}

// directiveEnd returns the index just past the '>' closing a <!...>
// declaration that starts b, skipping quoted strings and an internal subset,
// or -1 when it does not end within b.
func directiveEnd(b []byte) int {
	depth := 0
	for i := 2; i < len(b); i++ {
		switch c := b[i]; c {
		case '"', '\'':
			k := bytes.IndexByte(b[i+1:], c)
			if k < 0 {
				return -1
			}
			i += k + 1
		case '[':
			depth++
		case ']':
			depth--
		case '>':
			if depth <= 0 {
				return i + 1
			}
		}
	}
	return -1
}

// isSVGStartTag reports whether b (a start tag without its '<') names an
// element whose local name is svg.
func isSVGStartTag(b []byte) bool {
	end := bytes.IndexAny(b, " \t\r\n/>")
	if end < 0 {
		// The name runs to the end of the sniffed window: undecidable.
		return false
	}
	name := b[:end]
	if i := bytes.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return string(name) == "svg"
}
