// Package svg sanitises untrusted SVG documents before they reach librsvg or
// are passed through to clients.
//
// The sanitiser is a single streaming pass over the token stream produced by
// encoding/xml in strict mode. Tokens that need no change are copied to the
// output byte for byte from the source (attribute quoting, entity references,
// CDATA sections, comments and namespace prefixes survive unchanged); only
// start tags whose attributes change and <style> bodies whose CSS changes are
// re-serialised.
//
// Removed: <script> and other active or embedding elements, <foreignObject>,
// event-handler attributes (on*), xml:base, href/xlink:href values that are not
// same-document fragments (or, when configured, data:image/* URIs), <use>
// elements that reference anything but a fragment, animations that target
// href or event attributes, processing instructions other than the XML
// declaration, and external CSS references (@import, url(), image-set() and
// friends) in <style> elements and in attribute values.
//
// Rejected with an error: documents with a DTD or any other <!...> directive
// (XXE, billion laughs), undeclared entity references, non-UTF-8 documents,
// malformed XML, a root element other than svg, and documents that exceed the
// configured byte, nesting depth or element count limits.
package svg

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Default limits applied when the corresponding Options field is zero.
const (
	DefaultMaxBytes    = 5 << 20 // XI_LIMITS_MAX_SVG_BYTES default (docs/CONFIG.md)
	DefaultMaxDepth    = 256     // libxml2's default maximum nesting depth
	DefaultMaxElements = 200_000 // well below librsvg's 1,000,000 loaded elements
)

// Errors returned by Sanitize. Callers branch on them with errors.Is; all of
// them describe an unusable or hostile input (HTTP 422 at the edge).
var (
	ErrMalformed       = errors.New("svg: malformed document")
	ErrNotSVG          = errors.New("svg: root element is not svg")
	ErrDTD             = errors.New("svg: document type declarations are not allowed")
	ErrTooLarge        = errors.New("svg: document exceeds the byte limit")
	ErrTooDeep         = errors.New("svg: document exceeds the nesting depth limit")
	ErrTooManyElements = errors.New("svg: document exceeds the element count limit")
)

// Options configures Sanitize. The zero value is ready to use and applies the
// default limits with data: URIs disallowed.
type Options struct {
	// MaxBytes caps the size of the input document. Zero means DefaultMaxBytes.
	MaxBytes int64
	// MaxDepth caps element nesting depth (the root element is depth 1).
	// Zero means DefaultMaxDepth.
	MaxDepth int
	// MaxElements caps the number of elements in the document, removed ones
	// included. Zero means DefaultMaxElements.
	MaxElements int
	// AllowDataImages keeps href/xlink:href and CSS url() values that are
	// data:image/* URIs (except data:image/svg+xml, whose content would escape
	// sanitisation). <use> never accepts data: URIs.
	AllowDataImages bool
}

func (o Options) withDefaults() Options {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.MaxElements <= 0 {
		o.MaxElements = DefaultMaxElements
	}
	return o
}

// Sanitize reads an SVG document from src and writes the sanitised document to
// dst. On error, dst may already have received a partial document, which the
// caller must discard.
func Sanitize(dst io.Writer, src io.Reader, opts Options) error {
	opts = opts.withDefaults()
	rr := newRawReader(src, opts.MaxBytes)
	d := xml.NewDecoder(rr)
	d.Strict = true
	s := sanitizer{
		opts: opts,
		dec:  d,
		rr:   rr,
		w:    writer{bufio.NewWriterSize(dst, 8<<10)},
	}
	if err := s.run(); err != nil {
		return err
	}
	if err := s.w.Flush(); err != nil {
		return fmt.Errorf("svg: write: %w", err)
	}
	return nil
}

type sanitizer struct {
	opts Options
	dec  *xml.Decoder
	rr   *rawReader
	w    writer

	stack     []xml.Name // open elements, all of them (kept or skipped)
	skipDepth int        // len(stack) of the element whose subtree is dropped; 0 when not skipping
	elements  int
	tokens    int // tokens seen at depth 0 before the root, BOM excluded
	rootSeen  bool
	rootDone  bool

	styleDepth int    // len(stack) of the open <style> element; 0 when not in one
	styleRaw   []byte // raw bytes of the <style> body
	styleText  []byte // decoded character data of the <style> body

	attrs []xml.Attr // scratch for rewritten start tags
}

func (s *sanitizer) run() error {
	for {
		tok, err := s.dec.RawToken()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return s.readError(err)
		}
		raw := s.rr.take(s.dec.InputOffset())
		if err := s.token(tok, raw); err != nil {
			return err
		}
	}
	if !s.rootSeen {
		return ErrNotSVG
	}
	if len(s.stack) > 0 {
		return fmt.Errorf("%w: unexpected end of document", ErrMalformed)
	}
	return nil
}

func (s *sanitizer) readError(err error) error {
	if errors.Is(err, ErrTooLarge) {
		return ErrTooLarge
	}
	if s.rr.err != nil && !errors.Is(s.rr.err, io.EOF) && errors.Is(err, s.rr.err) {
		return fmt.Errorf("svg: read: %w", err)
	}
	// Syntax errors, undeclared entities, unsupported encodings.
	return fmt.Errorf("%w: %w", ErrMalformed, err)
}

func (s *sanitizer) token(tok xml.Token, raw []byte) error {
	switch t := tok.(type) {
	case xml.StartElement:
		return s.start(t, raw)
	case xml.EndElement:
		return s.end(t, raw)
	case xml.CharData:
		return s.charData(t, raw)
	case xml.Comment:
		if len(s.stack) == 0 && !s.rootSeen {
			s.tokens++
		}
		s.emitContent(raw, nil)
	case xml.ProcInst:
		// Only the XML declaration survives, and only as the first token.
		// Everything else (xml-stylesheet in particular) is dropped.
		if t.Target == "xml" && len(s.stack) == 0 && !s.rootSeen && s.tokens == 0 {
			s.write(raw)
		}
		if len(s.stack) == 0 && !s.rootSeen {
			s.tokens++
		}
	case xml.Directive:
		return ErrDTD
	}
	return nil
}

func (s *sanitizer) start(t xml.StartElement, raw []byte) error {
	s.elements++
	if s.elements > s.opts.MaxElements {
		return ErrTooManyElements
	}
	if len(s.stack) >= s.opts.MaxDepth {
		return ErrTooDeep
	}
	if len(s.stack) == 0 {
		if s.rootDone {
			return fmt.Errorf("%w: more than one root element", ErrMalformed)
		}
		if t.Name.Local != "svg" {
			return ErrNotSVG
		}
		s.rootSeen = true
	}
	s.stack = append(s.stack, t.Name)
	switch {
	case s.skipDepth > 0:
		return nil
	case s.styleDepth > 0 || dropElement(t):
		// Elements inside <style> are not CSS; drop them with their content.
		s.skipDepth = len(s.stack)
		return nil
	}
	s.startTag(t, raw)
	if strings.EqualFold(t.Name.Local, "style") && !selfClosing(raw) {
		s.styleDepth = len(s.stack)
		s.styleRaw = s.styleRaw[:0]
		s.styleText = s.styleText[:0]
	}
	return nil
}

func (s *sanitizer) end(t xml.EndElement, raw []byte) error {
	n := len(s.stack)
	if n == 0 || s.stack[n-1] != t.Name {
		return fmt.Errorf("%w: unexpected end element", ErrMalformed)
	}
	s.stack = s.stack[:n-1]
	if n == 1 {
		s.rootDone = true
	}
	if s.skipDepth > 0 {
		if n == s.skipDepth {
			s.skipDepth = 0
		}
		return nil
	}
	if n == s.styleDepth {
		s.styleDepth = 0
		s.flushStyle()
	}
	s.write(raw)
	return nil
}

func (s *sanitizer) charData(t xml.CharData, raw []byte) error {
	if len(s.stack) == 0 {
		// Outside the root only whitespace is allowed, plus a leading BOM.
		if !s.rootSeen && s.tokens == 0 && bytes.HasPrefix(raw, []byte(utf8BOM)) {
			raw = raw[len(utf8BOM):]
			s.w.str(utf8BOM)
		}
		if !isXMLSpace(raw) {
			return fmt.Errorf("%w: character data outside the root element", ErrMalformed)
		}
		if len(raw) > 0 && !s.rootSeen {
			s.tokens++
		}
		s.write(raw)
		return nil
	}
	s.emitContent(raw, t)
	return nil
}

// emitContent writes character data or a comment inside the document,
// diverting it into the style buffer inside <style>.
func (s *sanitizer) emitContent(raw []byte, text []byte) {
	switch {
	case s.skipDepth > 0:
	case s.styleDepth > 0:
		s.styleRaw = append(s.styleRaw, raw...)
		s.styleText = append(s.styleText, text...)
	default:
		s.write(raw)
	}
}

// flushStyle writes the buffered <style> body. The CSS is checked as the
// concatenation of all character data so that tokens split by comments or
// CDATA boundaries (for example "@imp<!---->ort") are seen as the browser
// sees them.
func (s *sanitizer) flushStyle() {
	out, verdict := sanitizeCSS(string(s.styleText), s.opts.AllowDataImages)
	switch verdict {
	case cssUnchanged:
		s.write(s.styleRaw)
	case cssRewritten:
		s.w.escape(out, false)
	case cssRejected:
	}
}

// startTag writes a start tag, re-serialising it only when an attribute is
// removed or rewritten.
func (s *sanitizer) startTag(t xml.StartElement, raw []byte) {
	s.attrs = s.attrs[:0]
	changed := false
	for _, a := range t.Attr {
		v, keep := sanitizeAttr(a, s.opts.AllowDataImages)
		if !keep {
			changed = true
			continue
		}
		if v != a.Value {
			changed = true
			a.Value = v
		}
		s.attrs = append(s.attrs, a)
	}
	if !changed {
		s.write(raw)
		return
	}
	s.w.str("<")
	s.w.name(t.Name)
	for _, a := range s.attrs {
		s.w.str(" ")
		s.w.name(a.Name)
		s.w.str(`="`)
		s.w.escape(a.Value, true)
		s.w.str(`"`)
	}
	if selfClosing(raw) {
		s.w.str("/>")
	} else {
		s.w.str(">")
	}
}

func (s *sanitizer) write(b []byte) {
	s.w.raw(b)
}

// writer wraps the buffered output. bufio.Writer errors are sticky and are
// reported by Flush at the end of Sanitize, so individual writes ignore them.
type writer struct{ *bufio.Writer }

func (w writer) str(s string) { _, _ = w.WriteString(s) }

func (w writer) raw(b []byte) { _, _ = w.Write(b) }

const utf8BOM = "\xEF\xBB\xBF"

func selfClosing(raw []byte) bool {
	return bytes.HasSuffix(raw, []byte("/>"))
}

func isXMLSpace(b []byte) bool {
	for _, c := range b {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}

func (w writer) name(n xml.Name) {
	if n.Space != "" {
		w.str(n.Space)
		w.str(":")
	}
	w.str(n.Local)
}

// escape writes s with the XML metacharacters escaped. In attribute mode
// quotes and whitespace other than space are escaped too, so that attribute
// value normalisation gives back exactly s.
func (w writer) escape(s string, attr bool) {
	last := 0
	for i := range len(s) {
		var esc string
		switch s[i] {
		case '&':
			esc = "&amp;"
		case '<':
			esc = "&lt;"
		case '>':
			esc = "&gt;"
		case '\r':
			esc = "&#xD;"
		case '"':
			if !attr {
				continue
			}
			esc = "&quot;"
		case '\t':
			if !attr {
				continue
			}
			esc = "&#x9;"
		case '\n':
			if !attr {
				continue
			}
			esc = "&#xA;"
		default:
			continue
		}
		w.str(s[last:i])
		w.str(esc)
		last = i + 1
	}
	w.str(s[last:])
}
