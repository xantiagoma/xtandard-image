// Package imagemeta decodes image metadata for the /info endpoint in the
// shape of imgproxy's JSON response (docs.imgproxy.net usage/getting_info):
// EXIF values from the string fields libvips attaches, IPTC-IIM datasets and
// Photoshop resources (ParseIPTC) and XMP packets (ParseXMP).
//
// It is pure Go: the cgo side (codec.Inspect) reads the raw fields from
// libvips and hands them over here. Every parser takes attacker-controlled
// bytes, never panics and is fuzzed.
package imagemeta

import (
	"strconv"
	"strings"
)

// EXIFField splits a libvips EXIF field name "exif-ifd<n>-<Name>" into the
// IFD number (0 = IFD0, 1 = thumbnail IFD, 2 = EXIF, 3 = GPS,
// 4 = interoperability) and libexif's canonical tag name ("DateTimeOriginal").
func EXIFField(field string) (ifd int, name string, ok bool) {
	rest, ok := strings.CutPrefix(field, "exif-ifd")
	if !ok {
		return 0, "", false
	}
	num, name, ok := strings.Cut(rest, "-")
	if !ok || name == "" || num == "" || len(num) > 2 {
		return 0, "", false
	}
	ifd, err := strconv.Atoi(num)
	if err != nil || ifd < 0 {
		return 0, "", false
	}
	return ifd, name, true
}

// numericEXIFFormats are the libexif format names libvips renders as raw
// numbers before the parenthesis (vips_exif_to_s in libvips exif.c).
var numericEXIFFormats = map[string]bool{
	"Short": true, "SShort": true, "Long": true, "SLong": true,
	"Rational": true, "SRational": true,
}

// EXIFValue returns the human-readable value (libexif's
// exif_entry_get_value, what imgproxy reports) of a libvips EXIF string
// field. libvips renders each tag as
//
//	<raw> (<human-readable>, <format>, <n> components, <m> bytes)
//
// where <raw> is n numbers each followed by a space for numeric formats
// with fewer than 10 components, and "<human-readable> " otherwise. Values
// that do not have this shape are returned trimmed.
func EXIFValue(s string) string {
	body, ok := strings.CutSuffix(s, " bytes)")
	if !ok {
		return strings.TrimSpace(s)
	}
	body, _, ok1 := strings.CutLast(body, ", ") // "<m>" bytes
	body, comps, ok2 := strings.CutLast(body, ", ")
	body, format, ok3 := strings.CutLast(body, ", ")
	nstr, ok4 := strings.CutSuffix(comps, " components")
	n, err := strconv.Atoi(nstr)
	if !ok1 || !ok2 || !ok3 || !ok4 || err != nil || n < 0 {
		return strings.TrimSpace(s)
	}
	if numericEXIFFormats[format] && n > 0 && n < 10 {
		rest := body
		for range n {
			_, after, found := strings.Cut(rest, " ")
			if !found {
				return strings.TrimSpace(s)
			}
			rest = after
		}
		if text, ok := strings.CutPrefix(rest, "("); ok {
			return text
		}
		return strings.TrimSpace(s)
	}
	// body is text + " (" + text.
	if l := (len(body) - 2) / 2; len(body) >= 2 && len(body)%2 == 0 && body[l:l+2] == " (" && body[:l] == body[l+2:] {
		return body[l+2:]
	}
	return strings.TrimSpace(s)
}
