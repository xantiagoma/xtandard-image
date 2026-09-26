package raw

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Tag is one camera metadata tag of a RAW file, as EXIF.
type Tag struct {
	// IFD is the libexif IFD: 0 = IFD0, 2 = Exif.
	IFD int
	// Name is libexif's tag name ("ExposureTime").
	Name string
	// Value is the human-readable value, formatted like libexif's
	// exif_entry_get_value ("1/250 sec.", "f/5.6", "35.0 mm").
	Value string
	// raw is the value libvips parses back when it writes EXIF (numbers
	// for numeric tags, the text for ASCII tags).
	raw    string
	format string // libexif format name
	comps  int
	bytes  int
}

// Field returns the tag as the libvips metadata field "exif-ifdN-Name" in
// the string form libvips' own EXIF parser produces, which its savers turn
// back into an EXIF entry (vips__exif_update).
func (t *Tag) Field() vips.TextField {
	return vips.TextField{
		Field: vips.MetaField(fmt.Sprintf("exif-ifd%d-%s", t.IFD, t.Name)),
		Value: fmt.Sprintf("%s (%s, %s, %d components, %d bytes)", t.raw, t.Value, t.format, t.comps, t.bytes),
	}
}

// EXIF returns the camera metadata of h as EXIF tags: Make, Model and
// Software in IFD0; ExposureTime, FNumber, ISOSpeedRatings, FocalLength,
// DateTimeOriginal and LensModel in the Exif IFD. Unknown values are left
// out. GPS is never exported.
func EXIF(h *vips.RawHeader) []Tag {
	var out []Tag
	ascii := func(ifd int, name, v string) {
		v = asciiText(v)
		if v == "" {
			return
		}
		out = append(out, Tag{IFD: ifd, Name: name, Value: v, raw: v, format: "ASCII", comps: len(v) + 1, bytes: len(v) + 1})
	}
	rational := func(name string, num, den uint32, human string) {
		out = append(out, Tag{IFD: 2, Name: name, Value: human, raw: fmt.Sprintf("%d/%d", num, den), format: "Rational", comps: 1, bytes: 8})
	}
	ascii(0, "Make", h.Make)
	ascii(0, "Model", h.Model)
	ascii(0, "Software", h.Software)
	if s := h.ShutterSeconds; s > 0 && s < 1e5 {
		var num, den uint32
		switch {
		case s < 1:
			num, den = 1, uint32(math.Round(1/s))
		default:
			num, den = uint32(math.Round(s*10)), 10
		}
		if den > 0 && num > 0 {
			d := float64(num) / float64(den)
			human := fmt.Sprintf("%.0f sec.", d)
			if d < 1 {
				human = fmt.Sprintf("1/%.0f sec.", 1/d)
			}
			rational("ExposureTime", num, den, human)
		}
	}
	if a := h.Aperture; a > 0 && a < 1e4 {
		num := uint32(math.Round(a * 10))
		rational("FNumber", num, 10, fmt.Sprintf("f/%.1f", float64(num)/10))
	}
	if iso := math.Round(h.ISO); iso > 0 && iso <= math.MaxUint16 {
		v := int(iso)
		out = append(out, Tag{IFD: 2, Name: "ISOSpeedRatings", Value: strconv.Itoa(v), raw: strconv.Itoa(v), format: "Short", comps: 1, bytes: 2})
	}
	if f := h.FocalLength; f > 0 && f < 1e5 {
		num := uint32(math.Round(f * 10))
		rational("FocalLength", num, 10, fmt.Sprintf("%.1f mm", float64(num)/10))
	}
	if !h.Captured.IsZero() {
		ascii(2, "DateTimeOriginal", h.Captured.Format("2006:01:02 15:04:05"))
	}
	ascii(2, "LensModel", h.Lens)
	return out
}

// asciiText keeps the printable ASCII of s (EXIF ASCII tags, and what
// libvips writes anyway), trimmed.
func asciiText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7E {
			return -1
		}
		return r
	}, s))
}
