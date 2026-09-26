// Package raw is the camera RAW codec (FORMATS.md §2): it recognises RAW
// containers by magic bytes (Sniff), reads their header through LibRaw
// without unpacking anything (Probe, on top of vips.ProbeRaw), picks the
// embedded JPEG preview the pipeline may decode instead of demosaicing
// (Header.Preview, PreviewBytes), converts LibRaw's orientation to EXIF
// (Orientation) and renders the camera metadata as EXIF fields (EXIF).
//
// LibRaw itself is called only from internal/vips; this package is Go.
package raw

import (
	"context"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// Header describes a RAW file: the probed LibRaw header, its kind and the
// usable embedded preview.
type Header struct {
	vips.RawHeader
	Kind Kind
	// Orientation is the EXIF orientation (1..8) of the RAW, from LibRaw's
	// flip. It applies to the demosaiced image and to the preview alike:
	// both are stored the way the sensor was read.
	Orientation int
	// Preview is the largest embedded JPEG that decodes as a plain 8-bit
	// JPEG; HasPreview is false when there is none.
	Preview    Preview
	HasPreview bool
}

// Preview locates an embedded JPEG preview in the source buffer.
type Preview struct {
	Offset int64
	Length int
	// Width and Height are the JPEG frame size (stored orientation).
	Width, Height int
}

// Probe sniffs data's kind and reads the RAW header with LibRaw (header
// only: no unpacking, no decoding) and picks the preview. Errors are those
// of vips.ProbeRaw: KindUnsupported, KindCorrupt, KindLimitExceeded or a
// context kind. Like every vips call it must run inside vips.Run.
func Probe(ctx context.Context, buf *vips.Buffer) (Header, error) {
	rh, err := vips.ProbeRaw(ctx, buf)
	if err != nil {
		return Header{}, fmt.Errorf("raw: probe: %w", err)
	}
	if rh.Width <= 0 || rh.Height <= 0 {
		return Header{}, errs.New(errs.KindCorrupt, "raw: probe: no image size")
	}
	data := buf.Bytes()
	h := Header{RawHeader: rh, Orientation: Orientation(rh.Flip)}
	h.Kind, _ = Sniff(data)
	h.Preview, h.HasPreview = PickPreview(data, rh.Thumbnails)
	return h, nil
}

// PickPreview returns the largest thumbnail LibRaw classifies as JPEG whose
// bytes (clamped to data) parse as a decodable JPEG frame (JPEGSize).
func PickPreview(data []byte, thumbs []vips.RawThumbnail) (Preview, bool) {
	var best Preview
	found := false
	for _, t := range thumbs {
		if !t.JPEG || t.Offset <= 0 || t.Offset >= int64(len(data)) || t.Length < 64 {
			continue
		}
		n := int(min(int64(t.Length), int64(len(data))-t.Offset))
		w, h, _, ok := JPEGSize(data[t.Offset : t.Offset+int64(n)])
		if !ok {
			continue
		}
		if !found || int64(w)*int64(h) > int64(best.Width)*int64(best.Height) {
			best = Preview{Offset: t.Offset, Length: n, Width: w, Height: h}
			found = true
		}
	}
	return best, found
}

// PreviewBytes copies the preview p out of data into dst (which must hold
// p.Length bytes) with the SOI marker restored, as LibRaw's unpack_thumb
// does. It fails with KindCorrupt when p does not lie inside data.
func PreviewBytes(dst, data []byte, p Preview) error {
	if p.Offset <= 0 || p.Length < 2 || p.Offset+int64(p.Length) > int64(len(data)) || len(dst) != p.Length {
		return errs.New(errs.KindCorrupt, "raw: preview outside the file")
	}
	copy(dst, data[p.Offset:p.Offset+int64(p.Length)])
	dst[0], dst[1] = 0xFF, 0xD8
	return nil
}

// Orientation converts LibRaw's flip (dcraw's convention) to the EXIF
// orientation 1..8; unknown values are 1. It is the inverse of LibRaw's
// "50132467"[exif & 7] mapping of the TIFF Orientation tag.
func Orientation(flip int) int {
	switch flip {
	case 1:
		return 2
	case 2:
		return 4
	case 3:
		return 3
	case 4:
		return 5
	case 5:
		return 8
	case 6:
		return 6
	case 7:
		return 7
	default:
		return 1
	}
}
