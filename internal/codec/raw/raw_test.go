package raw_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/raw"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(golden.Fixture(tb, name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// tiff builds a little-endian TIFF whose IFD0 holds the given SHORT/LONG
// entries and a Make string.
func tiff(maker string, entries ...[3]uint32) []byte {
	type ent struct{ tag, typ, count, val uint32 }
	es := make([]ent, 0, len(entries)+1)
	for _, e := range entries {
		es = append(es, ent{e[0], e[1], 1, e[2]})
	}
	n := len(es) + 1
	makeOff := uint32(8 + 2 + n*12 + 4)                              //nolint:gosec // small by construction
	es = append(es, ent{0x010F, 2, uint32(len(maker) + 1), makeOff}) //nolint:gosec // short test strings
	b := []byte("II*\x00\x08\x00\x00\x00")
	b = binary.LittleEndian.AppendUint16(b, uint16(n)) //nolint:gosec // small by construction
	for _, e := range es {
		b = binary.LittleEndian.AppendUint16(b, uint16(e.tag)) //nolint:gosec // tag ids
		b = binary.LittleEndian.AppendUint16(b, uint16(e.typ)) //nolint:gosec // type ids
		b = binary.LittleEndian.AppendUint32(b, e.count)
		if e.typ == 3 {
			b = binary.LittleEndian.AppendUint16(b, uint16(e.val)) //nolint:gosec // SHORT values
			b = append(b, 0, 0)
		} else {
			b = binary.LittleEndian.AppendUint32(b, e.val)
		}
	}
	b = binary.LittleEndian.AppendUint32(b, 0)
	return append(append(b, maker...), 0)
}

// leafTIFF is a big-endian IFD0 without a Make, like a Leaf Aptus back
// writes it: the compression and a LeafData (0x8606) block of 16 bytes that
// starts with magic.
func leafTIFF(compression uint16, magic string) []byte {
	be := binary.BigEndian
	b := []byte("MM\x00*\x00\x00\x00\x08")
	b = be.AppendUint16(b, 2)
	b = be.AppendUint16(b, 0x103) // Compression, SHORT
	b = be.AppendUint16(b, 3)
	b = be.AppendUint32(b, 1)
	b = be.AppendUint16(b, compression)
	b = append(b, 0, 0)
	b = be.AppendUint16(b, 0x8606) // LeafData, ASCII
	b = be.AppendUint16(b, 2)
	b = be.AppendUint32(b, 16)
	b = be.AppendUint32(b, 8+2+2*12+4)
	b = be.AppendUint32(b, 0)
	return append(b, (magic + "\x00\x00\x00\x01camera_pr")[:16]...)
}

func TestSniff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data []byte
		want raw.Kind
	}{
		{"synthetic dng", readFixture(t, "dng/landscape.dng"), raw.KindDNG},
		{"cr2", []byte("II*\x00\x10\x00\x00\x00CR\x02\x00"), raw.KindCR2},
		{"cr3", []byte("\x00\x00\x00\x18ftypcrx \x00\x00\x00\x01crx isom"), raw.KindCR3},
		{"raf", []byte("FUJIFILMCCD-RAW 0201FF383501"), raw.KindRAF},
		{"orf", []byte("IIRO\x08\x00\x00\x00"), raw.KindORF},
		{"rw2", []byte("IIU\x00\x08\x00\x00\x00"), raw.KindRW2},
		{"x3f", []byte("FOVb\x00\x00\x00\x00"), raw.KindX3F},
		{"mrw", []byte("\x00MRM\x00\x00\x00\x00"), raw.KindMRW},
		{"crw", []byte("II\x1a\x00\x00\x00HEAPCCDR"), raw.KindCRW},
		{"iiq", []byte("IIII\x01\x00\x00\x00"), raw.KindIIQ},
		{"iiq in tiff", []byte("II*\x00\x08\x00\x00\x00IIII\x01\x00\x00\x00"), raw.KindIIQ},
		{"nef: reduced-res ifd0", tiff("NIKON CORPORATION", [3]uint32{0xFE, 4, 1}), raw.KindNEF},
		{"arw: jpeg ifd0", tiff("SONY", [3]uint32{0x103, 3, 6}), raw.KindARW},
		{"pef: pentax compression", tiff("PENTAX Corporation", [3]uint32{0x103, 3, 65535}), raw.KindPEF},
		{"srw: subifds", tiff("SAMSUNG", [3]uint32{0x14A, 4, 100}), raw.KindSRW},
		{"3fr: cfa", tiff("Hasselblad", [3]uint32{0x106, 3, 32803}), raw.Kind3FR},
		{"erf", tiff("SEIKO EPSON CORP.", [3]uint32{0xFE, 4, 1}), raw.KindERF},
		{"mef", tiff("Mamiya-OP Co.,Ltd.", [3]uint32{0xFE, 4, 1}), raw.KindMEF},
		{"mos", tiff("Leaf", [3]uint32{0x14A, 4, 100}), raw.KindMOS},
		{"mos: leafdata, no make (Aptus 22)", leafTIFF(99, "PKTS"), raw.KindMOS},
		{"mos: leafdata beyond the head", leafTIFF(99, "PKTS")[:40], raw.KindMOS},
		{"dcr", tiff("Kodak", [3]uint32{0xFE, 4, 1}), raw.KindDCR},
		{"dng version tag", tiff("Anything", [3]uint32{0xC612, 1, 0x01040000}), raw.KindDNG},
		// Not RAW.
		{"camera tiff, plain rgb", tiff("NIKON CORPORATION", [3]uint32{0x103, 3, 1}, [3]uint32{0x106, 3, 2}), raw.KindUnknown},
		{"camera tiff, no compression tag", tiff("SONY"), raw.KindUnknown},
		{"scanner tiff", tiff("EPSON", [3]uint32{0xFE, 4, 1}), raw.KindUnknown},
		{"plain tiff fixture", readFixture(t, "tiff/multipage.tif"), raw.KindUnknown},
		{"jpeg", readFixture(t, "jpeg/baseline.jpg"), raw.KindUnknown},
		{"heic", readFixture(t, "heic/photo.heic"), raw.KindUnknown},
		{"short", []byte("II*"), raw.KindUnknown},
		{"bad ifd offset", []byte("II*\x00\xff\xff\xff\x7f"), raw.KindUnknown},
		{"make beyond the data", tiff("NIKON CORPORATION", [3]uint32{0xFE, 4, 1})[:40], raw.KindUnknown},
		{"tag 0x8606 without pkts", leafTIFF(99, "XXXX"), raw.KindUnknown},
		{"leafdata on a plain lzw tiff", leafTIFF(5, "PKTS"), raw.KindUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := raw.Sniff(tc.data)
			if got != tc.want || ok != (tc.want != raw.KindUnknown) {
				t.Errorf("Sniff = %v, %t; want %v", got, ok, tc.want)
			}
		})
	}
}

func encodeJPEG(tb testing.TB, w, h int) []byte {
	tb.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

func TestJPEGSize(t *testing.T) {
	t.Parallel()
	j := encodeJPEG(t, 33, 17)
	noSOI := append([]byte{0, 0}, j[2:]...)
	sof3 := bytes.Clone(j)
	if i := bytes.Index(sof3, []byte{0xFF, 0xC0}); i > 0 {
		sof3[i+1] = 0xC3
	}
	for _, tc := range []struct {
		name       string
		data       []byte
		w, h, c    int
		ok         bool
		wantReject bool
	}{
		{name: "baseline grey", data: j, w: 33, h: 17, c: 1, ok: true},
		{name: "soi missing (LibRaw patches it)", data: noSOI, w: 33, h: 17, c: 1, ok: true},
		{name: "fixture", data: readFixture(t, "jpeg/progressive.jpg"), w: 320, h: 240, c: 3, ok: true},
		{name: "lossless frame", data: sof3},
		{name: "truncated", data: j[:20]},
		{name: "h265 preview", data: []byte("\x00\x00\x00\x01CISZ\x00\x00\x00\x00\x00\x00")},
		{name: "empty", data: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w, h, c, ok := raw.JPEGSize(tc.data)
			if ok != tc.ok || w != tc.w || h != tc.h || c != tc.c {
				t.Errorf("JPEGSize = %d, %d, %d, %t; want %d, %d, %d, %t", w, h, c, ok, tc.w, tc.h, tc.c, tc.ok)
			}
		})
	}
}

func TestPickPreview(t *testing.T) {
	t.Parallel()
	small, big := encodeJPEG(t, 40, 30), encodeJPEG(t, 160, 120)
	data := append(append(append([]byte("II*\x00pad."), small...), big...), "junk-junk-junk-junk-junk-junk-junk-junk-junk-junk-junk-junk-junk"...)
	offSmall, offBig := int64(8), int64(8+len(small))
	junk := offBig + int64(len(big))
	for _, tc := range []struct {
		name   string
		thumbs []vips.RawThumbnail
		want   raw.Preview
		ok     bool
	}{
		{"largest wins", []vips.RawThumbnail{
			{JPEG: true, Offset: offSmall, Length: len(small)},
			{JPEG: true, Offset: offBig, Length: len(big), Width: 1, Height: 1}, // tags lie: the JPEG header decides
		}, raw.Preview{Offset: offBig, Length: len(big), Width: 160, Height: 120}, true},
		{"non-jpeg skipped", []vips.RawThumbnail{{JPEG: false, Offset: offBig, Length: len(big)}}, raw.Preview{}, false},
		{"not a jpeg", []vips.RawThumbnail{{JPEG: true, Offset: junk, Length: 64}}, raw.Preview{}, false},
		{
			"clamped to the file",
			[]vips.RawThumbnail{{JPEG: true, Offset: offBig, Length: 1 << 30}},
			raw.Preview{Offset: offBig, Length: len(data) - int(offBig), Width: 160, Height: 120},
			true,
		},
		{"outside the file", []vips.RawThumbnail{{JPEG: true, Offset: int64(len(data)) + 10, Length: 100}}, raw.Preview{}, false},
		{"none", nil, raw.Preview{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := raw.PickPreview(data, tc.thumbs)
			if ok != tc.ok || got != tc.want {
				t.Errorf("PickPreview = %+v, %t; want %+v, %t", got, ok, tc.want, tc.ok)
			}
		})
	}

	p := raw.Preview{Offset: offBig, Length: len(big)}
	dst := make([]byte, p.Length)
	data[offBig] = 0 // a preview stored without its SOI marker
	if err := raw.PreviewBytes(dst, data, p); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst, big) {
		t.Error("PreviewBytes did not restore the SOI marker")
	}
	if err := raw.PreviewBytes(dst, data[:10], p); err == nil {
		t.Error("PreviewBytes accepted a preview outside the data")
	}
}

func TestOrientation(t *testing.T) {
	t.Parallel()
	// LibRaw's TIFF mapping flip = "50132467"[exif & 7] - '0', inverted.
	for exif := 1; exif <= 8; exif++ {
		flip := int("50132467"[exif&7] - '0')
		if got := raw.Orientation(flip); got != exif {
			t.Errorf("Orientation(%d) = %d, want %d", flip, got, exif)
		}
	}
	if got := raw.Orientation(-1); got != 1 {
		t.Errorf("Orientation(-1) = %d, want 1", got)
	}
}

func TestEXIF(t *testing.T) {
	t.Parallel()
	h := vips.RawHeader{
		Make: "Canon", Model: "EOS R6\x01", Lens: "RF70-200mm F2.8 L IS USM",
		ISO: 100, ShutterSeconds: 0.004, Aperture: 5.6, FocalLength: 200,
		Captured: time.Date(2021, 5, 12, 12, 21, 21, 0, time.UTC),
	}
	type field struct{ Name, Value string }
	got := make([]field, 0, 8)
	for _, tag := range raw.EXIF(&h) {
		f := tag.Field()
		got = append(got, field{string(f.Field), f.Value})
	}
	want := []field{
		{"exif-ifd0-Make", "Canon (Canon, ASCII, 6 components, 6 bytes)"},
		{"exif-ifd0-Model", "EOS R6 (EOS R6, ASCII, 7 components, 7 bytes)"},
		{"exif-ifd2-ExposureTime", "1/250 (1/250 sec., Rational, 1 components, 8 bytes)"},
		{"exif-ifd2-FNumber", "56/10 (f/5.6, Rational, 1 components, 8 bytes)"},
		{"exif-ifd2-ISOSpeedRatings", "100 (100, Short, 1 components, 2 bytes)"},
		{"exif-ifd2-FocalLength", "2000/10 (200.0 mm, Rational, 1 components, 8 bytes)"},
		{"exif-ifd2-DateTimeOriginal", "2021:05:12 12:21:21 (2021:05:12 12:21:21, ASCII, 20 components, 20 bytes)"},
		{"exif-ifd2-LensModel", "RF70-200mm F2.8 L IS USM (RF70-200mm F2.8 L IS USM, ASCII, 25 components, 25 bytes)"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("EXIF fields (-want +got):\n%s", diff)
	}
	long := raw.EXIF(&vips.RawHeader{ShutterSeconds: 2.5})
	if len(long) != 1 || long[0].Value != "2 sec." && long[0].Value != "3 sec." {
		t.Errorf("2.5 s exposure = %+v", long)
	}
	if len(raw.EXIF(&vips.RawHeader{})) != 0 {
		t.Error("empty header produced tags")
	}
}

func FuzzSniff(f *testing.F) {
	f.Add(readFixture(f, "dng/landscape.dng")[:512])
	f.Add(tiff("NIKON CORPORATION", [3]uint32{0xFE, 4, 1}))
	f.Add([]byte("\x00\x00\x00\x18ftypcrx \x00\x00\x00\x01crx isom"))
	f.Add([]byte("FUJIFILMCCD-RAW 0201"))
	f.Fuzz(func(t *testing.T, b []byte) {
		k, ok := raw.Sniff(b)
		if ok != (k != raw.KindUnknown) || k.String() == "" {
			t.Fatalf("Sniff = %v, %t", k, ok)
		}
	})
}

func FuzzJPEGSize(f *testing.F) {
	f.Add(encodeJPEG(f, 8, 8))
	f.Add(readFixture(f, "jpeg/progressive.jpg")[:600])
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x11, 0x08})
	f.Fuzz(func(t *testing.T, b []byte) {
		w, h, c, ok := raw.JPEGSize(b)
		if ok && (w <= 0 || h <= 0 || (c != 1 && c != 3)) {
			t.Fatalf("JPEGSize = %d, %d, %d, true", w, h, c)
		}
		if !ok && (w != 0 || h != 0 || c != 0) {
			t.Fatalf("JPEGSize failed with %d, %d, %d", w, h, c)
		}
	})
}
