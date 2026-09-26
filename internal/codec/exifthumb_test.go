package codec

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
)

// miniJPEG is a JPEG header with a w×h SOF0 and no scan data: enough for
// FindEXIFThumbnail, which never decodes.
func miniJPEG(w, h uint16) []byte {
	b := []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x11, 8}
	b = binary.BigEndian.AppendUint16(b, h)
	b = binary.BigEndian.AppendUint16(b, w)
	b = append(b, 3, 1, 0x22, 0, 2, 0x11, 1, 3, 0x11, 1)
	return append(b, 0xFF, 0xD9)
}

type exifEntry struct {
	tag, typ uint16
	val      uint32
}

// exifAPP1 builds an Exif APP1 segment: IFD0 with an orientation entry,
// IFD1 with entries, then thumb. thumbOff < 0 places the thumbnail right
// after IFD1 and fills the 0x0201/0x0202 values in.
func exifAPP1(bo binary.AppendByteOrder, ifd1 []exifEntry, thumb []byte) []byte {
	var t []byte
	if bo == binary.AppendByteOrder(binary.LittleEndian) {
		t = []byte("II*\x00")
	} else {
		t = []byte("MM\x00*")
	}
	t = bo.AppendUint32(t, 8)
	// IFD0: one entry, next IFD right after it.
	t = bo.AppendUint16(t, 1)
	t = append(t, entry(bo, exifEntry{0x0112, 3, 6})...)
	ifd1Off := uint32(8 + 2 + 12 + 4)
	if ifd1 == nil {
		ifd1Off = 0
	}
	t = bo.AppendUint32(t, ifd1Off)
	if ifd1 != nil {
		thumbOff := ifd1Off + 2 + 12*uint32(len(ifd1)) + 4 //nolint:gosec // G115: test values are small
		t = bo.AppendUint16(t, uint16(len(ifd1)))          //nolint:gosec // G115: test values are small
		for _, e := range ifd1 {
			switch {
			case e.tag == 0x0201 && e.val == 0:
				e.val = thumbOff
			case e.tag == 0x0202 && e.val == 0:
				e.val = uint32(len(thumb)) //nolint:gosec // G115: test values are small
			}
			t = append(t, entry(bo, e)...)
		}
		t = bo.AppendUint32(t, 0)
		t = append(t, thumb...)
	}
	seg := append([]byte("Exif\x00\x00"), t...)
	out := []byte{0xFF, 0xE1}
	out = binary.BigEndian.AppendUint16(out, uint16(len(seg)+2)) //nolint:gosec // G115: test values are small
	return append(out, seg...)
}

func entry(bo binary.AppendByteOrder, e exifEntry) []byte {
	b := bo.AppendUint16(nil, e.tag)
	b = bo.AppendUint16(b, e.typ)
	b = bo.AppendUint32(b, 1)
	if e.typ == 3 {
		b = bo.AppendUint16(b, uint16(e.val)) //nolint:gosec // G115: test values are small
		return append(b, 0, 0)
	}
	return bo.AppendUint32(b, e.val)
}

// withEXIF puts app1 after the SOI of a mini JPEG main image.
func withEXIF(app1 []byte) []byte {
	main := miniJPEG(640, 480)
	return slices.Concat(main[:2], []byte{0xFF, 0xE0, 0x00, 0x04, 0, 0}, app1, main[2:])
}

func TestFindEXIFThumbnail(t *testing.T) {
	t.Parallel()
	thumb := miniJPEG(160, 120)
	jpegIFD1 := []exifEntry{{0x0103, 3, 6}, {0x0201, 4, 0}, {0x0202, 4, 0}}
	for _, tc := range []struct {
		name string
		data []byte
		ok   bool
	}{
		{"little endian", withEXIF(exifAPP1(binary.LittleEndian, jpegIFD1, thumb)), true},
		{"big endian", withEXIF(exifAPP1(binary.BigEndian, jpegIFD1, thumb)), true},
		{"no compression tag", withEXIF(exifAPP1(binary.BigEndian, jpegIFD1[1:], thumb)), true},
		{"uncompressed thumbnail", withEXIF(exifAPP1(binary.LittleEndian, []exifEntry{{0x0103, 3, 1}, {0x0201, 4, 0}, {0x0202, 4, 0}}, thumb)), false},
		{"no ifd1", withEXIF(exifAPP1(binary.LittleEndian, nil, nil)), false},
		{"no offset", withEXIF(exifAPP1(binary.LittleEndian, []exifEntry{{0x0202, 4, 0}}, thumb)), false},
		{"offset out of bounds", withEXIF(exifAPP1(binary.LittleEndian, []exifEntry{{0x0201, 4, 1 << 20}, {0x0202, 4, 0}}, thumb)), false},
		{"length out of bounds", withEXIF(exifAPP1(binary.LittleEndian, []exifEntry{{0x0201, 4, 0}, {0x0202, 4, 1 << 20}}, thumb)), false},
		{"thumbnail not a jpeg", withEXIF(exifAPP1(binary.LittleEndian, jpegIFD1, []byte("not a jpeg at all"))), false},
		{"no exif", miniJPEG(640, 480), false},
		{"not a jpeg", []byte("\x89PNG\r\n\x1a\n"), false},
		{"truncated segment", withEXIF(exifAPP1(binary.LittleEndian, jpegIFD1, thumb))[:40], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := FindEXIFThumbnail(tc.data)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if got.Width != 160 || got.Height != 120 || !bytes.Equal(got.Data, thumb) {
				t.Errorf("thumbnail %dx%d (%d bytes), want 160x120 (%d bytes)", got.Width, got.Height, len(got.Data), len(thumb))
			}
		})
	}
}

func FuzzFindEXIFThumbnail(f *testing.F) {
	thumb := miniJPEG(16, 12)
	f.Add(withEXIF(exifAPP1(binary.LittleEndian, []exifEntry{{0x0201, 4, 0}, {0x0202, 4, 0}}, thumb)))
	f.Add(withEXIF(exifAPP1(binary.BigEndian, []exifEntry{{0x0103, 3, 6}, {0x0201, 4, 0}, {0x0202, 4, 0}}, thumb)))
	f.Add(miniJPEG(1, 1))
	f.Fuzz(func(t *testing.T, b []byte) {
		th, ok := FindEXIFThumbnail(b)
		if !ok {
			return
		}
		if th.Width <= 0 || th.Height <= 0 || len(th.Data) < 4 {
			t.Fatalf("ok with %dx%d, %d bytes", th.Width, th.Height, len(th.Data))
		}
	})
}
