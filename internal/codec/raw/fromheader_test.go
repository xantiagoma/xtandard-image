package raw_test

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// withPreview returns a NEF-shaped file (a Nikon IFD0 with a reduced-size
// subfile, which Sniff takes for a NEF) or, without a maker, plain bytes,
// followed by a w×h baseline JPEG; thumb lists it like LibRaw does.
func withPreview(t *testing.T, nikon bool, w, h int) ([]byte, vips.RawThumbnail) {
	t.Helper()
	data := []byte("not a raw container at all")
	if nikon {
		data = tiff("NIKON CORPORATION", [3]uint32{0xFE, 4, 1})
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	thumb := vips.RawThumbnail{JPEG: true, Width: w, Height: h, Offset: int64(len(data)), Length: jb.Len()}
	return append(data, jb.Bytes()...), thumb
}

// TestFromHeaderPreviewOnly: a RAW LibRaw cannot demosaic becomes its
// preview, or a 422 naming the reason when it has none (WP-101).
func TestFromHeaderPreviewOnly(t *testing.T) {
	t.Parallel()
	nef, thumb := withPreview(t, true, 64, 48)
	plain, plainThumb := withPreview(t, false, 64, 48)
	he := vips.RawHeader{
		Undecodable: vips.RawNikonHE, Width: 5600, Height: 3728, Flip: 6, Make: "Nikon", Model: "Z50_2",
		Thumbnails: []vips.RawThumbnail{thumb},
	}
	for _, tc := range []struct {
		name   string
		rh     vips.RawHeader
		data   []byte
		w, h   int
		only   bool
		public string // client message of a KindUnsupported error
	}{
		{"decodable keeps the demosaic size", vips.RawHeader{Width: 5600, Height: 3728, Thumbnails: []vips.RawThumbnail{thumb}}, nef, 5600, 3728, false, ""},
		{"nikon he: the preview is the image", he, nef, 64, 48, true, ""},
		{
			"nikon he without a preview",
			vips.RawHeader{Undecodable: vips.RawNikonHE, Width: 5600, Height: 3728, Make: "Nikon", Model: "Z 8"},
			nef, 0, 0, false,
			"camera RAW not supported (Nikon Z 8: Nikon High Efficiency compression) and the file has no usable embedded preview",
		},
		{
			"placeholder decoder",
			vips.RawHeader{Undecodable: vips.RawNoDecoder, Width: 192, Height: 128, Thumbnails: []vips.RawThumbnail{thumb}},
			nef, 64, 48, true, "",
		},
		{
			"unknown camera in a raw container",
			vips.RawHeader{Undecodable: vips.RawUnknownCamera, Make: "Sony", Model: "ILCE-7M5", Thumbnails: []vips.RawThumbnail{thumb}},
			nef, 64, 48, true, "",
		},
		{
			"unknown camera outside a raw container",
			vips.RawHeader{Undecodable: vips.RawUnknownCamera, Make: "Sony", Model: "ILCE-7M5", Thumbnails: []vips.RawThumbnail{plainThumb}},
			plain, 0, 0, false,
			"camera RAW not supported (Sony ILCE-7M5: camera not supported by LibRaw) and the file has no usable embedded preview",
		},
		{
			"preview that is not a jpeg",
			vips.RawHeader{Undecodable: vips.RawNikonHE, Make: "Nikon", Model: "Z f", Thumbnails: []vips.RawThumbnail{{JPEG: false, Offset: thumb.Offset, Length: thumb.Length}}},
			nef, 0, 0, false,
			"camera RAW not supported (Nikon Z f: Nikon High Efficiency compression) and the file has no usable embedded preview",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, err := raw.FromHeader(&tc.rh, tc.data)
			if tc.public != "" {
				e, ok := errors.AsType[*errs.Error](err)
				if errs.KindOf(err) != errs.KindUnsupported || !ok || e.PublicMessage() != tc.public {
					t.Fatalf("err = %v, want unsupported %q", err, tc.public)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if h.Width != tc.w || h.Height != tc.h || h.PreviewOnly != tc.only || !h.HasPreview {
				t.Errorf("%dx%d preview-only %v (preview %v), want %dx%d %v", h.Width, h.Height, h.PreviewOnly, h.HasPreview, tc.w, tc.h, tc.only)
			}
			if h.Orientation != raw.Orientation(tc.rh.Flip) {
				t.Errorf("orientation %d, want %d", h.Orientation, raw.Orientation(tc.rh.Flip))
			}
		})
	}
	// A decodable header without a size is corrupt.
	if _, err := raw.FromHeader(&vips.RawHeader{}, nef); errs.KindOf(err) != errs.KindCorrupt {
		t.Errorf("no size: %v, want corrupt", err)
	}
}
