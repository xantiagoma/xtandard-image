package dicom_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/codec/dicom"
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// marker is written into every patient-identifying element of the
// fixtures (testdata/gen/dicom.go).
const marker = "XIPRIVACYMARKER"

var fixtures = []string{
	"ct-bigendian.dcm", "ct-j2k.dcm", "ct-rle.dcm", "ct-window.dcm", "grey-2frames.dcm",
	"jpeg-2frames-bot.dcm", "jpeg-baseline.dcm", "mono1-implicit.dcm", "palette.dcm",
	"rgb-deflated.dcm", "rgb-planar.dcm", "rgb-rle.dcm",
}

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "dicom", name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func TestSniff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		b    []byte
		want bool
	}{
		{"fixture", readFixture(t, "ct-window.dcm"), true},
		{"short", append(make([]byte, 128), "DIC"...), false},
		{"no magic", make([]byte, 200), false},
		{"magic at 0", []byte("DICM" + strings.Repeat("\x00", 200)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := dicom.Sniff(tc.b); got != tc.want {
				t.Errorf("Sniff = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseHeader(t *testing.T) {
	t.Parallel()
	ignore := cmpopts.IgnoreUnexported(dicom.Header{})
	grey := func(ts string, enc dicom.Encoding, bits, stored int, signed bool) dicom.Header {
		return dicom.Header{
			Width: 64, Height: 48, Frames: 1, Samples: 1, BitsAllocated: bits, BitsStored: stored,
			HighBit: stored - 1, Signed: signed, Photometric: "MONOCHROME2", TransferSyntax: ts, Encoding: enc,
			RescaleSlope: 1, VOILUTFunction: "LINEAR",
		}
	}
	ct := func(ts string, enc dicom.Encoding, stored int) dicom.Header {
		h := grey(ts, enc, 16, stored, false)
		h.RescaleIntercept, h.WindowCenter, h.WindowWidth, h.HasWindow = -1024, 40, 400, true
		return h
	}
	rgb := func(ts string, enc dicom.Encoding, photometric string, planar bool, frames int) dicom.Header {
		return dicom.Header{
			Width: 64, Height: 48, Frames: frames, Samples: 3, BitsAllocated: 8, BitsStored: 8, HighBit: 7,
			Photometric: photometric, Planar: planar, TransferSyntax: ts, Encoding: enc, RescaleSlope: 1,
			VOILUTFunction: "LINEAR",
		}
	}
	mono1 := grey("1.2.840.10008.1.2", dicom.EncodingNative, 8, 8, false)
	mono1.Photometric = "MONOCHROME1"
	twoFrames := grey("1.2.840.10008.1.2.1", dicom.EncodingNative, 8, 8, false)
	twoFrames.Frames = 2
	pal := grey("1.2.840.10008.1.2.1", dicom.EncodingNative, 8, 8, false)
	pal.Photometric = "PALETTE COLOR"
	for _, tc := range []struct {
		file string
		want dicom.Header
	}{
		{"ct-window.dcm", ct("1.2.840.10008.1.2.1", dicom.EncodingNative, 12)},
		{"ct-rle.dcm", ct("1.2.840.10008.1.2.5", dicom.EncodingRLE, 16)},
		{"ct-j2k.dcm", ct("1.2.840.10008.1.2.4.90", dicom.EncodingJPEG2000, 16)},
		{"ct-bigendian.dcm", grey("1.2.840.10008.1.2.2", dicom.EncodingNative, 16, 16, true)},
		{"mono1-implicit.dcm", mono1},
		{"grey-2frames.dcm", twoFrames},
		{"palette.dcm", pal},
		{"rgb-planar.dcm", rgb("1.2.840.10008.1.2.1", dicom.EncodingNative, "RGB", true, 1)},
		{"rgb-deflated.dcm", rgb("1.2.840.10008.1.2.1.99", dicom.EncodingDeflated, "RGB", false, 1)},
		{"rgb-rle.dcm", rgb("1.2.840.10008.1.2.5", dicom.EncodingRLE, "RGB", true, 1)},
		{"jpeg-baseline.dcm", rgb("1.2.840.10008.1.2.4.50", dicom.EncodingJPEGBaseline, "YBR_FULL_422", false, 1)},
		{"jpeg-2frames-bot.dcm", rgb("1.2.840.10008.1.2.4.50", dicom.EncodingJPEGBaseline, "YBR_FULL_422", false, 2)},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			got, err := dicom.ParseHeader(readFixture(t, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got, ignore); diff != "" {
				t.Errorf("ParseHeader (-want +got):\n%s", diff)
			}
		})
	}
}

// px returns pixel (x, y) of r.
func px(r *raster.Raw, x, y int) [3]byte {
	i := (y*r.Width + x) * 3
	return [3]byte{r.Data[i], r.Data[i+1], r.Data[i+2]}
}

func grey(v byte) [3]byte { return [3]byte{v, v, v} }

func TestDecode(t *testing.T) {
	t.Parallel()
	// Pixel (20, 10) of the photo is RGB 158 183 235 and grey 181
	// (pydicom's pixel_array of the same files); (32, 24) is the phantom's
	// centre, 40 HU; (0, 0) is air, -1000 HU; (32, 3) the 700 HU bone ring.
	type spot struct {
		x, y int
		want [3]byte
	}
	for _, tc := range []struct {
		file  string
		spots []spot
	}{
		// LINEAR window 40/400: ((40 - 39.5) / 399 + 0.5) * 255 = 127.8.
		{"ct-window.dcm", []spot{{32, 24, grey(128)}, {0, 0, grey(0)}, {32, 3, grey(255)}}},
		{"ct-rle.dcm", []spot{{32, 24, grey(128)}, {0, 0, grey(0)}, {32, 3, grey(255)}}},
		// No window: the frame's range, -1000..700; 40 HU → 1040 / 1700 × 255.
		{"ct-bigendian.dcm", []spot{{32, 24, grey(156)}, {0, 0, grey(0)}, {32, 3, grey(255)}}},
		// MONOCHROME1, 8-bit, no window: identity, then inverted.
		{"mono1-implicit.dcm", []spot{{20, 10, grey(255 - 181)}}},
		{"grey-2frames.dcm", []spot{{20, 10, grey(181)}}},
		// Heat map: 3 × 181 = 543 → R 255, G 255, B 33.
		{"palette.dcm", []spot{{20, 10, [3]byte{255, 255, 33}}}},
		{"rgb-planar.dcm", []spot{{20, 10, [3]byte{158, 183, 235}}}},
		{"rgb-deflated.dcm", []spot{{20, 10, [3]byte{158, 183, 235}}}},
		{"rgb-rle.dcm", []spot{{20, 10, [3]byte{158, 183, 235}}}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			r, err := dicom.Decode(readFixture(t, tc.file), dicom.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Validate(); err != nil {
				t.Fatal(err)
			}
			if r.Width != 64 || r.Height != 48 || r.Bands != 3 {
				t.Fatalf("decoded %dx%dx%d", r.Width, r.Height, r.Bands)
			}
			for _, s := range tc.spots {
				if got := px(r, s.x, s.y); got != s.want {
					t.Errorf("pixel (%d, %d) = %v, want %v", s.x, s.y, got, s.want)
				}
			}
		})
	}
}

func TestDecodeSameImage(t *testing.T) {
	t.Parallel()
	for _, group := range [][]string{
		{"rgb-planar.dcm", "rgb-deflated.dcm", "rgb-rle.dcm"},
		{"ct-window.dcm", "ct-rle.dcm"},
	} {
		want, err := dicom.Decode(readFixture(t, group[0]), dicom.Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range group[1:] {
			got, err := dicom.Decode(readFixture(t, name), dicom.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Data, want.Data) {
				t.Errorf("%s decodes differently from %s", name, group[0])
			}
		}
	}
}

func TestDecodeCodecFrames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"jpeg-baseline.dcm", "ct-j2k.dcm"} {
		_, err := dicom.Decode(readFixture(t, name), dicom.Options{})
		if errs.KindOf(err) != errs.KindUnsupported {
			t.Errorf("%s: Decode error %v, want unsupported", name, err)
		}
	}
}

func TestFirstFrame(t *testing.T) {
	t.Parallel()
	frame := func(name string) ([]byte, dicom.Header) {
		t.Helper()
		b := readFixture(t, name)
		h, err := dicom.ParseHeader(b)
		if err != nil {
			t.Fatal(err)
		}
		f, err := dicom.FirstFrame(b, &h)
		if err != nil {
			t.Fatal(err)
		}
		return f, h
	}
	single, h := frame("jpeg-baseline.dcm")
	img, err := jpeg.Decode(bytes.NewReader(single))
	if err != nil {
		t.Fatalf("first frame (two fragments) is not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 48 {
		t.Fatalf("JPEG is %v", b)
	}
	multi, _ := frame("jpeg-2frames-bot.dcm")
	if !bytes.Equal(bytes.TrimRight(multi, "\x00"), bytes.TrimRight(single, "\x00")) {
		t.Error("frame 0 of the two-frame file (Basic Offset Table) differs from the single frame")
	}
	j2k, _ := frame("ct-j2k.dcm")
	if !bytes.HasPrefix(j2k, []byte{0xFF, 0x4F, 0xFF, 0x51}) || !bytes.HasSuffix(bytes.TrimRight(j2k, "\x00"), []byte{0xFF, 0xD9}) {
		t.Errorf("JPEG 2000 codestream % x ... % x", j2k[:4], j2k[len(j2k)-2:])
	}

	// Render what the codec decoded: RGB (the JPEG decoder converted YCbCr).
	s := dicom.Samples{Width: 64, Height: 48, Bands: 3, Decoded: true, Data: make([]int32, 64*48*3)}
	for y := range 48 {
		for x := range 64 {
			r, g, b, _ := img.At(x, y).RGBA()
			i := (y*64 + x) * 3
			s.Data[i], s.Data[i+1], s.Data[i+2] = int32(r>>8), int32(g>>8), int32(b>>8)
		}
	}
	r, err := h.Render(s)
	if err != nil {
		t.Fatal(err)
	}
	got := px(r, 20, 10)
	for c, want := range [3]int{162, 183, 238} { // pydicom (Pillow) decode of the same frame
		if d := int(got[c]) - want; d < -3 || d > 3 {
			t.Errorf("rendered JPEG pixel %v, want about %v", got, [3]int{162, 183, 238})
			break
		}
	}

	if _, err := dicom.FirstFrame(readFixture(t, "ct-window.dcm"), &dicom.Header{Encoding: dicom.EncodingNative}); err == nil {
		t.Error("FirstFrame of native pixel data succeeded")
	}
}

func TestRenderErrors(t *testing.T) {
	t.Parallel()
	h, err := dicom.ParseHeader(readFixture(t, "ct-j2k.dcm"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []dicom.Samples{
		{Width: 2, Height: 2, Bands: 1, Data: make([]int32, 3)},
		{Width: 0, Height: 2, Bands: 1},
		{Width: 1, Height: 1, Bands: 2, Data: make([]int32, 2)},
		{Width: 1, Height: 1, Bands: 3, Data: make([]int32, 3)}, // header says 1 sample
	} {
		if _, err := h.Render(s); err == nil {
			t.Errorf("Render(%+v) succeeded", s)
		}
	}
}

// build writes a minimal explicit VR little endian file whose dataset is
// ds (already encoded) after the meta group with transfer syntax ts.
func build(ts string, ds []byte) []byte {
	var b bytes.Buffer
	b.Write(make([]byte, 128))
	b.WriteString("DICM")
	if len(ts)%2 == 1 {
		ts += "\x00"
	}
	b.Write(el(0x00020010, "UI", []byte(ts)))
	b.Write(ds)
	return b.Bytes()
}

// el encodes one explicit VR little endian element (items: vr "").
func el(tag uint32, vr string, v []byte) []byte {
	le := binary.LittleEndian
	out := le.AppendUint16(nil, uint16(tag>>16))
	out = le.AppendUint16(out, uint16(tag)) //nolint:gosec // G115: the element half of the tag
	switch vr {
	case "":
		out = le.AppendUint32(out, uint32(len(v))) //nolint:gosec // G115: test values are small
	case "OB", "OW", "SQ", "UN", "UT":
		out = append(out, vr...)
		out = append(out, 0, 0)
		out = le.AppendUint32(out, uint32(len(v))) //nolint:gosec // G115: test values are small
	default:
		out = append(out, vr...)
		out = le.AppendUint16(out, uint16(len(v))) //nolint:gosec // G115: test values are small
	}
	return append(out, v...)
}

func us(tag uint32, v uint16) []byte {
	return el(tag, "US", binary.LittleEndian.AppendUint16(nil, v))
}

// image is an 8-bit MONOCHROME2 4×h image module plus pixel data of n
// bytes.
func image(h uint16, n int) []byte {
	const w = 4
	var b []byte
	b = append(b, us(0x00280002, 1)...)
	b = append(b, el(0x00280004, "CS", []byte("MONOCHROME2 "))...)
	b = append(b, us(0x00280010, h)...)
	b = append(b, us(0x00280011, w)...)
	b = append(b, us(0x00280100, 8)...)
	b = append(b, us(0x00280101, 8)...)
	b = append(b, us(0x00280102, 7)...)
	b = append(b, us(0x00280103, 0)...)
	return append(b, el(0x7FE00010, "OB", make([]byte, n))...)
}

func TestErrors(t *testing.T) {
	t.Parallel()
	const le = "1.2.840.10008.1.2.1"
	// An undefined-length sequence nested n deep.
	nested := func(n int) []byte {
		b := make([]byte, 0, 20*n)
		for range n {
			// An undefined-length SQ holding an undefined-length item.
			b = append(b, 0x08, 0x00, 0x15, 0x11, 'S', 'Q', 0, 0, 0xFF, 0xFF, 0xFF, 0xFF,
				0xFE, 0xFF, 0x00, 0xE0, 0xFF, 0xFF, 0xFF, 0xFF)
		}
		return b
	}
	valid := build(le, image(4, 16))
	for _, tc := range []struct {
		name string
		b    []byte
		opts dicom.Options
		want errs.Kind
	}{
		{"not dicom", []byte("hello"), dicom.Options{}, errs.KindCorrupt},
		{"no transfer syntax", append(append(make([]byte, 128), "DICM"...), image(4, 16)...), dicom.Options{}, errs.KindCorrupt},
		{"unknown transfer syntax", build("1.2.3.4", image(4, 16)), dicom.Options{}, errs.KindUnsupported},
		{"no pixel data", build(le, image(4, 16)[:len(image(4, 16))-28]), dicom.Options{}, errs.KindUnsupported},
		{"short pixel data", build(le, image(4, 15)), dicom.Options{}, errs.KindCorrupt},
		{"truncated", valid[:len(valid)-20], dicom.Options{}, errs.KindCorrupt},
		{"zero rows", build(le, image(0, 16)), dicom.Options{}, errs.KindCorrupt},
		{"too many pixels", valid, dicom.Options{MaxPixels: 15}, errs.KindLimitExceeded},
		{"deep nesting", build(le, append(nested(40), image(4, 16)...)), dicom.Options{}, errs.KindCorrupt},
		{"bad VR", build(le, append([]byte{0x08, 0, 0x20, 0, 'd', 'a', 2, 0, '1', '2'}, image(4, 16)...)), dicom.Options{}, errs.KindCorrupt},
		{"native in RLE syntax", build("1.2.840.10008.1.2.5", image(4, 16)), dicom.Options{}, errs.KindCorrupt},
		{"bad deflate", append(build("1.2.840.10008.1.2.1.99", nil), 0xFF, 0xFF, 0xFF), dicom.Options{}, errs.KindCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := dicom.Decode(tc.b, tc.opts)
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("Decode error %v (%v), want %v", got, err, tc.want)
			}
		})
	}
	if _, err := dicom.Decode(valid, dicom.Options{}); err != nil {
		t.Errorf("valid minimal file: %v", err)
	}
}

// TestPrivacy checks that no value of a patient element reaches the
// header, its formatting or an error message, for intact and damaged files.
func TestPrivacy(t *testing.T) {
	t.Parallel()
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := readFixture(t, name)
			if !bytes.Contains(b, []byte(marker)) && name != "rgb-deflated.dcm" {
				t.Fatal("fixture without the privacy marker")
			}
			check := func(what string, v any) {
				if s := fmt.Sprintf("%+v %v %#v", v, v, v); strings.Contains(s, marker) {
					t.Fatalf("%s leaks a patient value: %s", what, s)
				}
			}
			for cut := 132; cut <= len(b); cut += len(b)/150 + 1 {
				c := b[:cut]
				h, err := dicom.ParseHeader(c)
				check("header", h)
				check("ParseHeader error", err)
				r, err := dicom.Decode(c, dicom.Options{})
				check("Decode error", err)
				_ = r
				if err == nil || h.Encoding.Codec() {
					f, err := dicom.FirstFrame(c, &h)
					check("FirstFrame error", err)
					if bytes.Contains(f, []byte(marker)) {
						t.Fatal("FirstFrame returned patient data")
					}
				}
				// Flip a byte in the dataset to reach other error paths.
				m := bytes.Clone(b)
				m[cut-1] ^= 0xA5
				_, err = dicom.ParseHeader(m)
				check("ParseHeader error (mutated)", err)
				_, err = dicom.Decode(m, dicom.Options{})
				check("Decode error (mutated)", err)
			}
		})
	}
}

func TestEncodingString(t *testing.T) {
	t.Parallel()
	for e := dicom.EncodingUnknown; e <= dicom.EncodingHTJ2K; e++ {
		if e.String() == "" || (e != dicom.EncodingUnknown && e.String() == "unknown") {
			t.Errorf("Encoding(%d).String() = %q", e, e.String())
		}
	}
}
