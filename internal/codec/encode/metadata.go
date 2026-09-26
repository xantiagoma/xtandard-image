package encode

import (
	"bytes"
	"context"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// EXIF field names libvips derives from the EXIF block ("exif-ifdN-Tag",
// libexif IFD numbering: 0 = IFD0, 1 = thumbnail, 2 = Exif, 3 = GPS,
// 4 = interoperability).
const (
	exifFieldPrefix   = "exif-"
	exifIFDPrefix     = "exif-ifd"
	exifGPSPrefix     = "exif-ifd3-"
	exifCopyright     = "exif-ifd0-Copyright"
	exifArtist        = "exif-ifd0-Artist"
	exifThumbnailData = "jpeg-thumbnail-data"
)

// prepareMetadata applies the metadata policy m to img: it returns the image
// to save (img itself when nothing changes, else a copy tracked by a) and the
// saver keep flags.
//
// libvips' keep has one bit for all of EXIF, and savers rebuild the EXIF
// block from the "exif-ifdN-Tag" fields on the image (exif.c
// vips__exif_update: block entries without a field are dropped, fields are
// written back), so single tags are removed by removing their fields.
func prepareMetadata(ctx context.Context, a *vips.Arena, img *vips.Image, m Metadata) (*vips.Image, vips.Keep, error) {
	keep := vips.KeepNone
	if !m.StripColorProfile {
		keep |= vips.KeepICC
	}
	var remove []vips.MetaField
	var xmp []byte
	setXMP := false
	switch {
	case !m.Strip:
		// Keep everything but GPS (FORMATS §4: GPS never leaves the server).
		keep |= vips.KeepAll &^ vips.KeepICC
		for _, f := range img.Fields() {
			if isGPSField(string(f)) {
				remove = append(remove, f)
			}
		}
		if x, ok := img.Blob(vips.FieldXMP); ok {
			if stripped, changed := stripXMPGPS(x); changed {
				xmp, setXMP = stripped, true
			}
		}
	case m.KeepCopyright:
		// imgproxy vips_strip: only IFD0 Copyright and Artist survive; the
		// rest of EXIF (GPS included), XMP, IPTC and the thumbnail go.
		copyright := false
		for _, f := range img.Fields() {
			s := string(f)
			switch {
			case s == exifCopyright || s == exifArtist:
				copyright = true
			case strings.HasPrefix(s, exifIFDPrefix) || s == exifThumbnailData:
				remove = append(remove, f)
			default:
			}
		}
		if copyright {
			keep |= vips.KeepEXIF
		}
	default:
		// KeepNone strips every EXIF field in the saver.
	}
	if len(remove) == 0 && !setXMP {
		return img, keep, nil
	}
	out := img
	var err error
	if len(remove) > 0 {
		if out, err = a.Add(out.RemoveMetadata(ctx, remove...)); err != nil {
			return nil, 0, err
		}
	}
	if setXMP {
		if len(xmp) == 0 {
			out, err = a.Add(out.RemoveMetadata(ctx, vips.FieldXMP))
		} else {
			out, err = a.Add(out.SetBlob(ctx, vips.FieldXMP, xmp))
		}
		if err != nil {
			return nil, 0, err
		}
	}
	return out, keep, nil
}

// isGPSField reports whether a metadata field is an EXIF GPS tag.
func isGPSField(f string) bool {
	if strings.HasPrefix(f, exifGPSPrefix) {
		return true
	}
	return strings.HasPrefix(f, exifIFDPrefix) && strings.Contains(f, "-GPS")
}

// stripXMPGPS removes the exif:GPS* properties from an XMP packet, both as
// attributes (exif:GPSLatitude="…") and as elements
// (<exif:GPSLatitude>…</exif:GPSLatitude>, or self-closing). It is a textual
// filter over well-formed RDF/XML, which is what XMP writers produce; changed
// is false when nothing matched.
func stripXMPGPS(x []byte) (out []byte, changed bool) {
	const prefix = "exif:GPS"
	if !bytes.Contains(x, []byte(prefix)) {
		return x, false
	}
	var b bytes.Buffer
	b.Grow(len(x))
	for {
		i := bytes.Index(x, []byte(prefix))
		if i < 0 {
			b.Write(x)
			break
		}
		name := i + len(prefix)
		for name < len(x) && isNameByte(x[name]) {
			name++
		}
		qname := x[i:name]
		switch {
		case i > 0 && x[i-1] == '<':
			// Element: drop it through its end tag (or "/>").
			end := elementEnd(x, name, qname)
			if end < 0 {
				b.Write(x)
				return b.Bytes(), true
			}
			b.Write(x[:i-1])
			x = x[end:]
			changed = true
		case i > 0 && isSpace(x[i-1]):
			// Attribute: drop ` exif:GPSx="…"`.
			end := attributeEnd(x, name)
			if end < 0 {
				b.Write(x[:name])
				x = x[name:]
				continue
			}
			start := i
			for start > 0 && isSpace(x[start-1]) {
				start--
			}
			b.Write(x[:start])
			x = x[end:]
			changed = true
		default:
			// End tag or text: keep and move on.
			b.Write(x[:name])
			x = x[name:]
		}
	}
	return b.Bytes(), changed
}

// elementEnd returns the index just past the element whose name ends at
// name (qname is its qualified name), or -1.
func elementEnd(x []byte, name int, qname []byte) int {
	gt := bytes.IndexByte(x[name:], '>')
	if gt < 0 {
		return -1
	}
	gt += name
	if x[gt-1] == '/' {
		return gt + 1
	}
	closing := append(append([]byte("</"), qname...), '>')
	j := bytes.Index(x[gt:], closing)
	if j < 0 {
		return -1
	}
	return gt + j + len(closing)
}

// attributeEnd returns the index just past the quoted value of the attribute
// whose name ends at name, or -1.
func attributeEnd(x []byte, name int) int {
	i := name
	for i < len(x) && isSpace(x[i]) {
		i++
	}
	if i >= len(x) || x[i] != '=' {
		return -1
	}
	i++
	for i < len(x) && isSpace(x[i]) {
		i++
	}
	if i >= len(x) || (x[i] != '"' && x[i] != '\'') {
		return -1
	}
	q := x[i]
	j := bytes.IndexByte(x[i+1:], q)
	if j < 0 {
		return -1
	}
	return i + 1 + j + 1
}

func isNameByte(c byte) bool {
	return c == '_' || c == '-' || c == '.' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
