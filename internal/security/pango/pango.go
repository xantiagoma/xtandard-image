// Package pango sanitises untrusted Pango markup and font descriptions
// before libvips renders them (text watermarks).
//
// Pango markup has no external references (no files, images or URLs), but
// it controls font sizes, and the rendered image grows with them: nested
// <big> elements, huge size attributes or letter spacing could make one
// URL allocate gigabytes. The sanitiser therefore re-serialises the markup
// from a strict encoding/xml token stream and keeps only a whitelist:
//
//   - elements b, big, i, s, small, sub, sup, tt, u without attributes, and
//     span with the font, colour, weight, style and decoration attributes
//     listed in spanAttrs, each value checked against its grammar;
//   - character data (the five XML entities and character references are
//     decoded and escaped again).
//
// Everything else is an error: other elements or attributes, namespaces,
// comments, processing instructions, directives (DTDs), CDATA sections,
// undeclared entities, markup over the byte or nesting limits, and font
// sizes above the limit. The result also reports how many characters the
// text has and the largest font size any of them can get, so the caller can
// bound the rendered size before rendering.
package pango

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Defaults of the Options fields.
const (
	DefaultMaxBytes = 4096
	DefaultMaxDepth = 8
	DefaultMaxSize  = 1000.0 // points
)

// Errors returned by Sanitize and ParseFont; they are wrapped with detail.
var (
	ErrInvalid  = errors.New("pango: invalid markup")
	ErrTooLarge = errors.New("pango: markup exceeds limits")
)

// Options bound the markup. Zero fields take the defaults.
type Options struct {
	MaxBytes int     // markup length
	MaxDepth int     // element nesting
	MaxSize  float64 // any font size, in points (big and larger compound)
}

func (o Options) withDefaults() Options {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.MaxSize <= 0 {
		o.MaxSize = DefaultMaxSize
	}
	return o
}

// Markup is sanitised markup.
type Markup struct {
	// Text is well-formed markup made only of whitelisted elements and
	// attributes.
	Text string
	// Runes is the number of characters of text (without markup), and
	// Visible the number of them that are not white space.
	Runes, Visible int
	// MaxSize is the largest font size in points a character can be
	// rendered at, starting from the base size given to Sanitize.
	MaxSize float64
}

// sizeStep is Pango's scale between named sizes (PANGO_SCALE_LARGE).
const sizeStep = 1.2

// root wraps the markup so that top-level text parses; it is not a
// whitelisted element, so markup cannot open or close it.
const root = "xi-markup"

// Sanitize checks markup against the whitelist and returns it
// re-serialised. base is the font size (points) the text starts at.
func Sanitize(markup string, base float64, o Options) (Markup, error) {
	o = o.withDefaults()
	switch {
	case len(markup) > o.MaxBytes:
		return Markup{}, wrapf(ErrTooLarge, "%d bytes, limit %d", len(markup), o.MaxBytes)
	case !utf8.ValidString(markup):
		return Markup{}, wrapf(ErrInvalid, "not UTF-8")
	case base <= 0 || base > o.MaxSize:
		return Markup{}, wrapf(ErrTooLarge, "font size %g, limit %g", base, o.MaxSize)
	}
	d := xml.NewDecoder(io.MultiReader(strings.NewReader("<"+root+">"), strings.NewReader(markup), strings.NewReader("</"+root+">")))
	d.Strict = true
	s := sanitizer{o: o, sizes: []float64{base}, max: base}
	s.out.Grow(len(markup))
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Markup{}, wrapf(ErrInvalid, "%v", err)
		}
		if err := s.token(tok); err != nil {
			return Markup{}, err
		}
	}
	if !s.closed {
		return Markup{}, wrapf(ErrInvalid, "unterminated markup")
	}
	return Markup{Text: s.out.String(), Runes: s.runes, Visible: s.visible, MaxSize: s.max}, nil
}

type sanitizer struct {
	o       Options
	out     strings.Builder
	open    []string  // element names; open[0] is root
	sizes   []float64 // font size per open element (sizes[0] is base)
	max     float64
	runes   int
	visible int
	closed  bool // root ended; nothing may follow
}

func (s *sanitizer) token(tok xml.Token) error {
	if s.closed {
		return wrapf(ErrInvalid, "content after the markup")
	}
	switch t := tok.(type) {
	case xml.StartElement:
		return s.start(t)
	case xml.EndElement:
		return s.end(t)
	case xml.CharData:
		if len(s.open) == 0 {
			return wrapf(ErrInvalid, "text outside the markup")
		}
		s.runes += utf8.RuneCount(t)
		for _, r := range string(t) {
			if !unicode.IsSpace(r) {
				s.visible++
			}
		}
		if err := xml.EscapeText(&s.out, t); err != nil {
			return wrapf(ErrInvalid, "%v", err)
		}
		return nil
	case xml.Comment:
		return wrapf(ErrInvalid, "comment")
	case xml.ProcInst:
		return wrapf(ErrInvalid, "processing instruction")
	case xml.Directive:
		return wrapf(ErrInvalid, "directive")
	default:
		return wrapf(ErrInvalid, "unexpected token")
	}
}

func (s *sanitizer) start(t xml.StartElement) error {
	name := t.Name.Local
	if len(s.open) == 0 {
		if name != root || t.Name.Space != "" {
			return wrapf(ErrInvalid, "unexpected root")
		}
		s.open = append(s.open, name)
		return nil
	}
	if t.Name.Space != "" {
		return wrapf(ErrInvalid, "namespaced element")
	}
	if len(s.open) > s.o.MaxDepth {
		return wrapf(ErrTooLarge, "nesting deeper than %d", s.o.MaxDepth)
	}
	size := s.sizes[len(s.sizes)-1]
	switch name {
	case "b", "i", "s", "tt", "u":
	case "big":
		size *= sizeStep
	case "small", "sub", "sup":
		size /= sizeStep
	case "span":
	default:
		return wrapf(ErrInvalid, "element %q", truncate(name))
	}
	if name != "span" && len(t.Attr) > 0 {
		return wrapf(ErrInvalid, "attribute on %q", name)
	}
	s.out.WriteByte('<')
	s.out.WriteString(name)
	for _, a := range t.Attr {
		v, sz, err := spanAttr(a, size, s.o.MaxSize)
		if err != nil {
			return err
		}
		size = sz
		s.out.WriteByte(' ')
		s.out.WriteString(a.Name.Local)
		s.out.WriteString(`="`)
		_ = xml.EscapeText(&s.out, []byte(v)) // strings.Builder never fails
		s.out.WriteByte('"')
	}
	s.out.WriteByte('>')
	if size > s.o.MaxSize {
		return wrapf(ErrTooLarge, "font size %g, limit %g", size, s.o.MaxSize)
	}
	s.open = append(s.open, name)
	s.sizes = append(s.sizes, size)
	s.max = max(s.max, size)
	return nil
}

func (s *sanitizer) end(t xml.EndElement) error {
	n := len(s.open)
	if n == 0 || t.Name.Space != "" || t.Name.Local != s.open[n-1] {
		return wrapf(ErrInvalid, "mismatched end element")
	}
	s.open = s.open[:n-1]
	if n == 1 {
		s.closed = true
		return nil
	}
	s.sizes = s.sizes[:len(s.sizes)-1]
	s.out.WriteString("</")
	s.out.WriteString(t.Name.Local)
	s.out.WriteByte('>')
	return nil
}

// spanAttr validates one span attribute and returns its value and the font
// size it leaves (size is the current one).
func spanAttr(a xml.Attr, size, maxSize float64) (string, float64, error) {
	if a.Name.Space != "" {
		return "", 0, wrapf(ErrInvalid, "namespaced attribute")
	}
	v := strings.TrimSpace(a.Value)
	ok := true
	switch a.Name.Local {
	case "font", "font_desc":
		f, err := ParseFont(v, maxSize)
		if err != nil {
			return "", 0, err
		}
		if f.Size > 0 {
			size = f.Size
		}
		v = f.String()
	case "font_family", "face":
		ok = family(v)
	case "size", "font_size":
		sz, err := fontSize(v, size)
		if err != nil {
			return "", 0, err
		}
		size = sz
	case "style", "font_style":
		ok = oneOf(v, "normal", "oblique", "italic")
	case "weight", "font_weight":
		ok = oneOf(v, "thin", "ultralight", "light", "semilight", "book", "normal", "medium",
			"semibold", "bold", "ultrabold", "heavy", "ultraheavy") || intIn(v, 100, 1000)
	case "variant", "font_variant":
		ok = oneOf(v, "normal", "small-caps", "all-small-caps", "petite-caps", "all-petite-caps",
			"unicase", "title-caps")
	case "stretch", "font_stretch":
		ok = oneOf(v, "ultracondensed", "extracondensed", "condensed", "semicondensed", "normal",
			"semiexpanded", "expanded", "extraexpanded", "ultraexpanded")
	case "foreground", "fgcolor", "color", "background", "bgcolor",
		"underline_color", "overline_color", "strikethrough_color":
		ok = colour(v)
	case "alpha", "fgalpha", "bgalpha":
		ok = alpha(v)
	case "underline":
		ok = oneOf(v, "none", "single", "double", "low", "error")
	case "overline":
		ok = oneOf(v, "none", "single")
	case "strikethrough", "allow_breaks", "insert_hyphens":
		ok = oneOf(v, "true", "false")
	default:
		return "", 0, wrapf(ErrInvalid, "span attribute %q", truncate(a.Name.Local))
	}
	if !ok {
		return "", 0, wrapf(ErrInvalid, "span %s=%q", a.Name.Local, truncate(v))
	}
	return v, size, nil
}

// Named sizes relative to the base size (PANGO_SCALE_*).
var namedSizes = map[string]float64{
	"xx-small": 1 / (sizeStep * sizeStep * sizeStep),
	"x-small":  1 / (sizeStep * sizeStep),
	"small":    1 / sizeStep,
	"medium":   1,
	"large":    sizeStep,
	"x-large":  sizeStep * sizeStep,
	"xx-large": sizeStep * sizeStep * sizeStep,
}

// fontSize resolves a span size: 1024ths of a point, "Npt", a named
// absolute size (relative to cur, an upper bound of Pango's base), or
// smaller/larger.
func fontSize(v string, cur float64) (float64, error) {
	switch v {
	case "smaller":
		return cur / sizeStep, nil
	case "larger":
		return cur * sizeStep, nil
	}
	if f, ok := namedSizes[v]; ok {
		return cur * f, nil
	}
	if pt, ok := strings.CutSuffix(v, "pt"); ok {
		f, err := strconv.ParseFloat(pt, 64)
		if err != nil || !(f > 0) || math.IsInf(f, 0) {
			return 0, wrapf(ErrInvalid, "size %q", truncate(v))
		}
		return f, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, wrapf(ErrInvalid, "size %q", truncate(v))
	}
	return float64(n) / 1024, nil
}

// Font is a parsed Pango font description: family and style words, then
// an optional size in points.
type Font struct {
	Words []string // family names (comma-separated lists kept) and style words
	Size  float64  // points; 0 = unset
}

// ParseFont parses and sanitises a font description ("DejaVu Sans Bold
// 16"). A size in pixels ("16px") is read as points: at the 72 DPI text is
// rendered at per CSS pixel they are the same, and points scale with the
// device pixel ratio. Sizes above maxSize (0 = DefaultMaxSize) and any
// character outside letters, digits, spaces and ",-_." are rejected, as
// are OpenType variations ("@wght=200").
func ParseFont(desc string, maxSize float64) (Font, error) {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	if len(desc) > 128 || !utf8.ValidString(desc) {
		return Font{}, wrapf(ErrInvalid, "font description")
	}
	for _, r := range desc {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" ,-_.", r) {
			return Font{}, wrapf(ErrInvalid, "font description %q", truncate(desc))
		}
	}
	words := strings.Fields(desc)
	var f Font
	if n := len(words); n > 0 {
		last := strings.TrimSuffix(words[n-1], "px")
		if sz, err := strconv.ParseFloat(last, 64); err == nil {
			if !(sz > 0) || sz > maxSize {
				return Font{}, wrapf(ErrTooLarge, "font size %q, limit %g", truncate(words[n-1]), maxSize)
			}
			f.Size = sz
			words = words[:n-1]
		}
	}
	f.Words = words
	return f, nil
}

// String renders f as a font description.
func (f Font) String() string {
	s := strings.Join(f.Words, " ")
	if f.Size > 0 {
		if s != "" {
			s += " "
		}
		s += strconv.FormatFloat(f.Size, 'f', -1, 64)
	}
	return s
}

// Family reports the family and style words ("" when only a size was given).
func (f Font) Family() string { return strings.Join(f.Words, " ") }

func family(v string) bool {
	f, err := ParseFont(v, 0)
	return err == nil && f.Size == 0 && len(f.Words) > 0
}

func oneOf(v string, set ...string) bool { return slices.Contains(set, v) }

func intIn(v string, lo, hi int) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n >= lo && n <= hi
}

// colour accepts #rgb, #rgba, #rrggbb, #rrggbbaa, #rrrgggbbb and
// #rrrrggggbbbb, or a colour name of ASCII letters (Pango looks names up in
// its own table).
func colour(v string) bool {
	if hex, ok := strings.CutPrefix(v, "#"); ok {
		switch len(hex) {
		case 3, 4, 6, 8, 9, 12:
		default:
			return false
		}
		for _, r := range hex {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
		return true
	}
	if v == "" || len(v) > 32 {
		return false
	}
	for _, r := range v {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// alpha accepts 1..65535 or a percentage.
func alpha(v string) bool {
	if p, ok := strings.CutSuffix(v, "%"); ok {
		return intIn(p, 0, 100)
	}
	return intIn(v, 1, 65535)
}

func truncate(s string) string {
	const n = 32
	if len(s) <= n {
		return s
	}
	for i := n; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i] + "…"
		}
	}
	return ""
}

// wrapf wraps a sentinel with detail; errors.Is matches the sentinel.
func wrapf(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", kind, fmt.Sprintf(format, args...))
}
