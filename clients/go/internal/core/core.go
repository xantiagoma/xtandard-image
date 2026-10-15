// Package core holds what the imgproxy and query builders and the generated
// typed options share: argument and number formatting, option segments,
// separators, PDF password tokens and stream settings. It is internal: the
// public surface is in the xtandard, imgproxy and query packages.
package core

import (
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
)

// FormatNumber formats a number like ECMAScript's Number::toString (300, 1.5,
// 0.1, 1e-7, 1e+21), so URLs and their signatures match the other clients.
// bits is 64, or 32 for a float32 (its shortest decimal). v must be finite.
func FormatNumber(v float64, bits int) string {
	if v == 0 {
		return "0"
	}
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	// Shortest round-trip digits d.ddd and exponent: v = 0.<digits> × 10^n.
	e := strconv.FormatFloat(v, 'e', -1, bits)
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	n, k := x+1, len(digits)
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		es := "e+"
		if n-1 < 0 {
			es = "e-"
		}
		es += strconv.Itoa(abs(n - 1))
		if k == 1 {
			out = digits + es
		} else {
			out = digits[:1] + "." + digits[1:] + es
		}
	}
	return sign + out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// badArgRune reports the characters an option argument cannot contain besides
// the separator: "/", "?", "#", whitespace, control characters and U+FEFF.
func badArgRune(r rune) bool {
	return r == '/' || r == '?' || r == '#' || r < 0x20 || r == 0x7f || r == 0xFEFF || unicode.IsSpace(r)
}

// FormatArg formats one argument. Strings are checked, never escaped: the
// server does not unescape option arguments.
func FormatArg(a xtandard.Arg, sep, where string) (string, error) {
	if a == nil {
		return "", nil
	}
	v := reflect.ValueOf(a)
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return "1", nil
		}
		return "0", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return "", xtandard.Errorf(xtandard.ErrInvalidArgument, "%s: %v is not a finite number", where, f)
		}
		return FormatNumber(f, v.Type().Bits()), nil
	case reflect.String:
		s := v.String()
		if strings.Contains(s, sep) || strings.ContainsFunc(s, badArgRune) {
			return "", xtandard.Errorf(xtandard.ErrInvalidArgument, "%s: argument %q contains a character that cannot appear in an option", where, s)
		}
		return s, nil
	default:
		return "", xtandard.Errorf(xtandard.ErrInvalidArgument, "%s: unsupported argument type %T", where, a)
	}
}

// FormatOption formats one option segment (rs:fill:300:400).
func FormatOption(cat xtandard.Catalogue, o xtandard.Option, sep string, style xtandard.OptionNames) (string, error) {
	written, err := cat.Written(o.Name, style)
	if err != nil {
		return "", err
	}
	if len(o.Args) == 0 {
		return "", xtandard.Errorf(xtandard.ErrInvalidArgument, "option %s: at least one argument is required", o.Name)
	}
	var b strings.Builder
	b.WriteString(written)
	for i, a := range o.Args {
		s, err := FormatArg(a, sep, "option "+o.Name+" argument "+strconv.Itoa(i+1))
		if err != nil {
			return "", err
		}
		b.WriteString(sep)
		b.WriteString(s)
	}
	return b.String(), nil
}

// CheckSeparator rejects a separator that is not one character or that the grammar uses.
func CheckSeparator(sep string, forbidden []string) error {
	if utf8.RuneCountInString(sep) != 1 || slices.Contains(forbidden, sep) {
		return xtandard.Errorf(xtandard.ErrInvalidSeparator, "invalid argument separator %q", sep)
	}
	return nil
}

// PasswordToken returns the pdf_password token, checking that both keys are present.
func PasswordToken(enc *xtandard.EncryptionKey, sign *xtandard.SigningKey, password string) (string, error) {
	if enc == nil {
		return "", xtandard.Errorf(xtandard.ErrMissingEncryption, "pdf_password needs an encryption key")
	}
	if sign == nil {
		return "", xtandard.Errorf(xtandard.ErrMissingSigning, "pdf_password needs signing: the server refuses it on unsigned URLs")
	}
	return xtandard.EncryptPDFPassword(password, *enc)
}

// Stream is the stream settings of an HLS or DASH request.
type Stream struct {
	Protocol        xtandard.StreamProtocol
	Renditions      []int
	SegmentDuration int
	SegmentFormat   xtandard.SegmentFormat
}

// StreamOptions returns the /video options and the manifest container of a
// stream request: the given options, then stream, segment_duration and a
// non-default segment_format.
func StreamOptions(opts []xtandard.Option, s Stream) ([]xtandard.Option, xtandard.VideoContainer, error) {
	for _, o := range opts {
		switch c, _ := xtandard.CatalogueVideo.Canonical(o.Name); c {
		case "stream", "segment_duration", "segment_format", "format":
			return nil, "", xtandard.Errorf(xtandard.ErrInvalidRequest, "stream requests set %s through the protocol, renditions and segment settings", o.Name)
		}
	}
	container, _, ok := s.Protocol.Manifest()
	if !ok {
		return nil, "", xtandard.Errorf(xtandard.ErrInvalidRequest, "unknown stream protocol %q", s.Protocol)
	}
	out := append([]xtandard.Option(nil), opts...)
	if len(s.Renditions) > 0 {
		args := []xtandard.Arg{string(s.Protocol)}
		for _, r := range s.Renditions {
			args = append(args, r)
		}
		out = append(out, xtandard.Opt("stream", args...))
	}
	if s.SegmentDuration != 0 {
		out = append(out, xtandard.Opt("segment_duration", s.SegmentDuration))
	}
	if s.SegmentFormat != "" {
		if !slices.Contains(s.SegmentFormat.Protocols(), s.Protocol) {
			return nil, "", xtandard.Errorf(xtandard.ErrInvalidRequest, "segment format %q is not available for %s", s.SegmentFormat, s.Protocol)
		}
		if s.SegmentFormat != xtandard.DefaultSegmentFormat {
			out = append(out, xtandard.Opt("segment_format", string(s.SegmentFormat)))
		}
	}
	return out, container, nil
}

// NativeValue formats a query dialect value: booleans are true/false,
// numbers as [FormatNumber], strings verbatim.
func NativeValue(key string, v any) (string, error) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), nil
	case reflect.String:
		return rv.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return FormatArg(v, "", "query key "+key)
	default:
		return "", xtandard.Errorf(xtandard.ErrInvalidArgument, "query key %s: unsupported value type %T", key, v)
	}
}

// Deref returns *p, or nil (an empty argument) for a nil pointer.
func Deref[T any](p *T) xtandard.Arg {
	if p == nil {
		return nil
	}
	return *p
}

// Trim drops trailing empty arguments (nil or "") beyond the first min.
func Trim(args []xtandard.Arg, minLen int) []xtandard.Arg {
	n := len(args)
	for n > minLen && empty(args[n-1]) {
		n--
	}
	return args[:n]
}

func empty(a xtandard.Arg) bool {
	if a == nil {
		return true
	}
	v := reflect.ValueOf(a)
	return v.Kind() == reflect.String && v.Len() == 0
}

// ProcessingOptions converts processing options into Options, dropping nil ones.
func ProcessingOptions(opts []xtandard.ProcessingOption) []xtandard.Option {
	out := make([]xtandard.Option, 0, len(opts))
	for _, o := range opts {
		if o != nil {
			out = append(out, o.ProcessingOption())
		}
	}
	return out
}

// InfoOptions converts /info options into Options, dropping nil ones.
func InfoOptions(opts []xtandard.InfoOption) []xtandard.Option {
	out := make([]xtandard.Option, 0, len(opts))
	for _, o := range opts {
		if o != nil {
			out = append(out, o.InfoOption())
		}
	}
	return out
}

// VideoOptions converts /video options into Options, dropping nil ones.
func VideoOptions(opts []xtandard.VideoOption) []xtandard.Option {
	out := make([]xtandard.Option, 0, len(opts))
	for _, o := range opts {
		if o != nil {
			out = append(out, o.VideoOption())
		}
	}
	return out
}
