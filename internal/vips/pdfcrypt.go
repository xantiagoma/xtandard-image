package vips

import "bytes"

// pdfHeaderWindow mirrors libvips' vips__pdf_is_a_buffer: "%PDF" may start
// anywhere in the first 32 bytes (PDF 2.0 allows offset headers).
const pdfHeaderWindow = 32

// pdfMaxEncryptChecks bounds the work on a hostile file that repeats the
// /Encrypt name: past this many candidates it is treated as encrypted.
const pdfMaxEncryptChecks = 64

var (
	pdfMagic       = []byte("%PDF")
	pdfEncryptName = []byte("/Encrypt")
	pdfTrailerKW   = []byte("trailer")
	pdfObjKW       = []byte("obj")
)

// pdfEncrypted reports whether data is a PDF whose trailer carries an
// /Encrypt entry: a classic "trailer << … >>" dictionary or the dictionary
// of an object (a cross-reference stream, PDF 1.5+). Every trailer of an
// incrementally updated file counts. It is conservative: an /Encrypt name
// whose enclosing structure cannot be found also counts, so a false positive
// is possible on a malformed file but a real encryption dictionary is never
// missed.
//
// libvips reports a PDF that needs a password only through the text of the
// process-global error buffer, which concurrent requests share; this check
// replaces that text match. Callers use it after pdfload failed, so PDFium
// has the last word on whether the (possibly empty) user password opens the
// document.
func pdfEncrypted(data []byte) bool {
	if !bytes.Contains(data[:min(len(data), pdfHeaderWindow+len(pdfMagic)-1)], pdfMagic) {
		return false
	}
	off := 0
	for range pdfMaxEncryptChecks {
		i := bytes.Index(data[off:], pdfEncryptName)
		if i < 0 {
			return false
		}
		pos := off + i
		off = pos + len(pdfEncryptName)
		// A longer name (/EncryptMetadata) is not the entry.
		if off < len(data) && !pdfIsDelimiter(data[off]) && !pdfIsSpace(data[off]) {
			continue
		}
		if pdfInTrailer(data, pos) {
			return true
		}
	}
	return true
}

// pdfInTrailer reports whether the name at pos is a top-level key of the
// dictionary that follows the nearest "trailer" or "obj" keyword before it,
// or whether no such keyword exists.
func pdfInTrailer(data []byte, pos int) bool {
	start := max(pdfLastKeyword(data[:pos], pdfTrailerKW), pdfLastKeyword(data[:pos], pdfObjKW))
	if start < 0 {
		return true
	}
	return pdfDictHasTopLevel(data, pdfSkipSpace(data, start), pos)
}

// pdfLastKeyword returns the offset just past the last occurrence of kw in
// data that stands as a token of its own, or -1.
func pdfLastKeyword(data, kw []byte) int {
	for end := len(data); ; {
		i := bytes.LastIndex(data[:end], kw)
		if i < 0 {
			return -1
		}
		after := i + len(kw)
		before := i == 0 || pdfIsSpace(data[i-1]) || pdfIsDelimiter(data[i-1])
		if before && (after == len(data) || pdfIsSpace(data[after]) || pdfIsDelimiter(data[after])) {
			return after
		}
		end = i
	}
}

// pdfSkipSpace skips white space and comments from i.
func pdfSkipSpace(data []byte, i int) int {
	for i < len(data) {
		switch {
		case pdfIsSpace(data[i]):
			i++
		case data[i] == '%':
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		default:
			return i
		}
	}
	return i
}

// pdfDictHasTopLevel walks the dictionary starting at i ("<<") and reports
// whether the name token at pos lies directly in it, not in a nested
// dictionary, array, string or comment.
func pdfDictHasTopLevel(data []byte, i, pos int) bool {
	if !bytes.HasPrefix(data[i:], []byte("<<")) {
		return false
	}
	level := 0
	for i < len(data) && i <= pos {
		c := data[i]
		switch {
		case c == '%':
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case c == '(':
			i = pdfSkipLiteral(data, i)
		case c == '<' && i+1 < len(data) && data[i+1] == '<':
			level++
			i += 2
		case c == '<':
			for i < len(data) && data[i] != '>' {
				i++
			}
			i++
		case c == '>' && i+1 < len(data) && data[i+1] == '>':
			level--
			i += 2
			if level == 0 {
				return false
			}
		case c == '[':
			level++
			i++
		case c == ']':
			level--
			i++
		case c == '/':
			if i == pos {
				return level == 1
			}
			i++
			for i < len(data) && !pdfIsSpace(data[i]) && !pdfIsDelimiter(data[i]) {
				i++
			}
		default:
			i++
		}
	}
	return false
}

// pdfSkipLiteral returns the offset past the literal string opening at i:
// parentheses nest and a backslash escapes the next byte.
func pdfSkipLiteral(data []byte, i int) int {
	depth := 0
	for ; i < len(data); i++ {
		switch data[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return i
}

// pdfIsSpace reports PDF white space (ISO 32000-2 §7.2.3).
func pdfIsSpace(c byte) bool {
	switch c {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// pdfIsDelimiter reports PDF delimiter characters (ISO 32000-2 §7.2.3).
func pdfIsDelimiter(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}
