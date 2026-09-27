package translate

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// emitter is the input of one dialect emitter.
type emitter struct {
	r   *transform.Request
	src src
	// mountSource is the named source the target's mount binds (keys on
	// it are written relative); "" when not mounted or unbound.
	mountSource string
	// mountBase is the base_url of mountSource when it is an http source
	// with one (never shown: the playground does not reveal base URLs).
	mountBase string
	sep       string
}

// pl is the first pipeline (a zero one when the request has none).
func (e *emitter) pl() *transform.Pipeline {
	if len(e.r.Pipelines) == 0 {
		return &transform.Pipeline{}
	}
	return &e.r.Pipelines[0]
}

// errSource is returned when a dialect cannot address the source.
func errSource(msg string) error { return errors.New(msg) }

// errNeedsKey is why a dialect that addresses images by path on its
// mount's source (ImageKit's URL endpoint, Bunny's pull zone, AWS SIH's
// bucket) cannot write the source: it is not a key on that source, nor an
// http(s) URL under the source's base_url.
func (e *emitter) errNeedsKey(dialect string) error {
	if e.mountSource == "" {
		return fmt.Errorf("%s addresses images by path on its mount's source, and no mount here binds it to one: "+
			"mount it with a source (a local or keyed source, or an http source with base_url)", dialect)
	}
	if e.mountBase != "" {
		return fmt.Errorf("%s addresses images by path on its mount's source %q, and this source is neither a key on it "+
			"nor a URL under its base_url: pick a key on it (xi://%s/<key>)", dialect, e.mountSource, e.mountSource)
	}
	return fmt.Errorf("%s addresses images by path on its mount's source %q, and this source is not a key on it: "+
		"pick a key on it (xi://%s/<key>, a local or keyed source), or bind the mount to an http source with base_url "+
		"so URLs under it translate", dialect, e.mountSource, e.mountSource)
}

// num formats a float without trailing zeros ("0.5", "300").
func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// round4 rounds to 4 decimals, hiding binary noise of conversions.
func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

func itoa(n int) string { return strconv.Itoa(n) }

// hexRGB is "rrggbb" or "rrggbbaa" when not opaque.
func hexRGB(c transform.Color) string {
	if c.A == 255 {
		return fmt.Sprintf("%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
}

// hexARGB is "rrggbb" or "aarrggbb" (alpha first, imgix and wsrv).
func hexARGB(c transform.Color) string {
	if c.A == 255 {
		return fmt.Sprintf("%02x%02x%02x", c.R, c.G, c.B)
	}
	return fmt.Sprintf("%02x%02x%02x%02x", c.A, c.R, c.G, c.B)
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// dim is a Dim as "300" or "0.5"; ok is false for auto.
func dim(d transform.Dim) (string, bool) {
	switch {
	case d.Px > 0:
		return itoa(d.Px), true
	case d.Rel > 0:
		return num(d.Rel), true
	}
	return "", false
}

// sameSides reports four equal values (a uniform padding, border or
// radius).
func sameSides(a, b, c, d int) bool { return a == b && b == c && c == d }

// plainText strips Pango markup the dialects that take plain text cannot
// carry, reporting whether s had none (only the five XML escapes).
func plainText(s string) (string, bool) {
	if strings.ContainsAny(s, "<>") {
		return "", false
	}
	r := strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&#39;", "'", "&amp;", "&")
	return r.Replace(s), true
}

// escapePlain escapes a source for imgproxy's plain/ form and thumbor's
// image segment: everything but unreserved characters and ":/" is
// percent-encoded, so "?", "#", "@" and "%" never end the source early.
func escapePlain(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			c == '-', c == '.', c == '_', c == '~', c == ':', c == '/':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// compassNames maps compass gravities to a dialect's words.
type compassNames map[transform.GravityType]string
