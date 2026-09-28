package dds_test

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/codec/dds"
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// header builds a DDS file header: w×h, the DDS_PIXELFORMAT flags, FourCC,
// bit count and R, G, B, A masks; with dxgi > 0 it is a DX10 file.
type header struct {
	w, h       uint32
	flags      uint32
	fourCC     string
	bpp        uint32
	masks      [4]uint32
	dxgi       uint32
	alphaMode  uint32
	dataLength int
}

func (h header) bytes() []byte {
	le := binary.LittleEndian
	b := make([]byte, 128, 128+20+h.dataLength)
	copy(b, "DDS ")
	le.PutUint32(b[4:], 124)
	le.PutUint32(b[12:], h.h)
	le.PutUint32(b[16:], h.w)
	le.PutUint32(b[76:], 32)
	le.PutUint32(b[80:], h.flags)
	copy(b[84:88], h.fourCC)
	le.PutUint32(b[88:], h.bpp)
	for i, m := range h.masks {
		le.PutUint32(b[92+4*i:], m)
	}
	if h.dxgi > 0 {
		copy(b[84:88], "DX10")
		le.PutUint32(b[80:], 0x4)
		x := make([]byte, 20)
		le.PutUint32(x, h.dxgi)
		le.PutUint32(x[4:], 3)
		le.PutUint32(x[12:], 1)
		le.PutUint32(x[16:], h.alphaMode)
		b = append(b, x...)
	}
	return append(b, make([]byte, h.dataLength)...)
}

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "dds", name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func TestDecodeConfig(t *testing.T) {
	t.Parallel()
	ignore := cmpopts.IgnoreUnexported(dds.Config{})
	for _, tc := range []struct {
		name string
		in   []byte
		want dds.Config
	}{
		{"legacy dxt1", header{w: 8, h: 4, flags: 0x4, fourCC: "DXT1"}.bytes(), dds.Config{Width: 8, Height: 4, Format: "BC1"}},
		{"legacy dxt1 with alpha flag", header{w: 8, h: 4, flags: 0x5, fourCC: "DXT1"}.bytes(), dds.Config{Width: 8, Height: 4, Format: "BC1", Alpha: true}},
		{"legacy dxt5", header{w: 8, h: 4, flags: 0x4, fourCC: "DXT5"}.bytes(), dds.Config{Width: 8, Height: 4, Format: "BC3", Alpha: true}},
		{"legacy ati1", header{w: 8, h: 4, flags: 0x4, fourCC: "ATI1"}.bytes(), dds.Config{Width: 8, Height: 4, Format: "BC4_UNORM", Grey: true}},
		{"legacy bc5s", header{w: 8, h: 4, flags: 0x4, fourCC: "BC5S"}.bytes(), dds.Config{Width: 8, Height: 4, Format: "BC5_SNORM"}},
		{"legacy half float", header{w: 2, h: 2, flags: 0x4, fourCC: "q\x00\x00\x00"}.bytes(), dds.Config{Width: 2, Height: 2, Format: "R16G16B16A16_FLOAT", Alpha: true, HDR: true}},
		{"dx10 bc7", header{w: 16, h: 16, dxgi: 98}.bytes(), dds.Config{Width: 16, Height: 16, Format: "BC7", Alpha: true}},
		{"dx10 bc7 opaque", header{w: 16, h: 16, dxgi: 99, alphaMode: 3}.bytes(), dds.Config{Width: 16, Height: 16, Format: "BC7"}},
		{"dx10 bc6h uf16", header{w: 4, h: 4, dxgi: 95}.bytes(), dds.Config{Width: 4, Height: 4, Format: "BC6H_UF16", HDR: true}},
		{"dx10 bc6h sf16", header{w: 4, h: 4, dxgi: 96}.bytes(), dds.Config{Width: 4, Height: 4, Format: "BC6H_SF16", HDR: true}},
		{"dx10 bc4 snorm", header{w: 4, h: 4, dxgi: 81}.bytes(), dds.Config{Width: 4, Height: 4, Format: "BC4_SNORM", Grey: true}},
		{"dx10 bgrx", header{w: 3, h: 1, dxgi: 88}.bytes(), dds.Config{Width: 3, Height: 1, Format: "B8G8R8X8"}},
		{"dx10 r32 float", header{w: 3, h: 1, dxgi: 41}.bytes(), dds.Config{Width: 3, Height: 1, Format: "R32_FLOAT", Grey: true, HDR: true}},
		{"rgb masks 24-bit", header{w: 2, h: 2, flags: 0x40, bpp: 24, masks: [4]uint32{0xFF0000, 0xFF00, 0xFF, 0}}.bytes(), dds.Config{Width: 2, Height: 2, Format: "RGB masks"}},
		{"argb masks", header{w: 2, h: 2, flags: 0x41, bpp: 32, masks: [4]uint32{0xFF0000, 0xFF00, 0xFF, 0xFF000000}}.bytes(), dds.Config{Width: 2, Height: 2, Format: "RGB masks", Alpha: true}},
		{"luminance", header{w: 2, h: 2, flags: 0x20000, bpp: 8, masks: [4]uint32{0xFF, 0, 0, 0}}.bytes(), dds.Config{Width: 2, Height: 2, Format: "luminance masks", Grey: true}},
		{"palette", header{w: 2, h: 2, flags: 0x20, bpp: 8}.bytes(), dds.Config{Width: 2, Height: 2, Format: "palette8"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := dds.DecodeConfig(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(tc.want, got, ignore); d != "" {
				t.Errorf("DecodeConfig (-want +got):\n%s", d)
			}
		})
	}
}

func TestDecodeConfigErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   []byte
		kind errs.Kind
	}{
		{"short", []byte("DDS \x7c\x00\x00\x00"), errs.KindCorrupt},
		{"bad magic", append([]byte("DDX "), header{w: 4, h: 4}.bytes()[4:]...), errs.KindCorrupt},
		{"zero width", header{w: 0, h: 4, flags: 0x4, fourCC: "DXT1"}.bytes(), errs.KindCorrupt},
		{"too wide", header{w: 1 << 17, h: 4, flags: 0x4, fourCC: "DXT1"}.bytes(), errs.KindCorrupt},
		{"truncated dx10", header{w: 4, h: 4, flags: 0x4, fourCC: "DX10"}.bytes(), errs.KindCorrupt},
		{"unknown fourcc", header{w: 4, h: 4, flags: 0x4, fourCC: "ETC2"}.bytes(), errs.KindUnsupported},
		{"unknown dxgi", header{w: 4, h: 4, dxgi: 200}.bytes(), errs.KindUnsupported},
		{"yuv", header{w: 4, h: 4, flags: 0x200, bpp: 16}.bytes(), errs.KindUnsupported},
		{"odd bit count", header{w: 4, h: 4, flags: 0x40, bpp: 12, masks: [4]uint32{0xF00, 0xF0, 0xF, 0}}.bytes(), errs.KindUnsupported},
		{"no masks", header{w: 4, h: 4, flags: 0x40, bpp: 32}.bytes(), errs.KindUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := dds.DecodeConfig(tc.in); errs.KindOf(err) != tc.kind {
				t.Errorf("DecodeConfig: %v (kind %v), want kind %v", err, errs.KindOf(err), tc.kind)
			}
		})
	}
}

func TestDecodeUncompressed(t *testing.T) {
	t.Parallel()
	le := binary.LittleEndian
	half := func(v ...uint16) []byte {
		var b []byte
		for _, x := range v {
			b = le.AppendUint16(b, x)
		}
		return b
	}
	f32 := func(v ...float32) []byte {
		var b []byte
		for _, x := range v {
			b = le.AppendUint32(b, math.Float32bits(x))
		}
		return b
	}
	with := func(h header, data []byte) []byte { return append(h.bytes(), data...) }
	// The palette (entry 7 is 10, 20, 30), then two indices.
	indices := make([]byte, 1026)
	copy(indices[4*7:], []byte{10, 20, 30, 0})
	indices[1024] = 7
	for _, tc := range []struct {
		name  string
		in    []byte
		raw   *raster.Raw
		float *raster.Float
	}{
		{
			"bgra8", with(header{w: 2, h: 1, dxgi: 87}, []byte{3, 2, 1, 255, 30, 20, 10, 128}),
			&raster.Raw{Width: 2, Height: 1, Bands: 4, Data: []byte{1, 2, 3, 255, 10, 20, 30, 128}}, nil,
		},
		{
			"rgb565 masks", with(header{w: 2, h: 1, flags: 0x40, bpp: 16, masks: [4]uint32{0xF800, 0x07E0, 0x001F, 0}}, half(0xF800, 0x07E0)),
			&raster.Raw{Width: 2, Height: 1, Bands: 3, Data: []byte{255, 0, 0, 0, 255, 0}}, nil,
		},
		{
			"luminance+alpha", with(header{w: 1, h: 1, flags: 0x20001, bpp: 16, masks: [4]uint32{0xFF, 0, 0, 0xFF00}}, []byte{77, 200}),
			&raster.Raw{Width: 1, Height: 1, Bands: 4, Data: []byte{77, 77, 77, 200}}, nil,
		},
		{
			"r8g8", with(header{w: 1, h: 1, dxgi: 49}, []byte{9, 8}),
			&raster.Raw{Width: 1, Height: 1, Bands: 3, Data: []byte{9, 8, 0}}, nil,
		},
		{
			"rgb10a2", with(header{w: 1, h: 1, dxgi: 24}, le.AppendUint32(nil, 1023|512<<10|3<<30)),
			&raster.Raw{Width: 1, Height: 1, Bands: 4, Data: []byte{255, 128, 0, 255}}, nil,
		},
		{
			"palette", with(header{w: 2, h: 1, flags: 0x20, bpp: 8}, indices),
			&raster.Raw{Width: 2, Height: 1, Bands: 3, Data: []byte{10, 20, 30, 0, 0, 0}}, nil,
		},
		{
			"rgba half", with(header{w: 1, h: 1, dxgi: 10}, half(0x4000, 0x3C00, 0x3800, 0x3C00)),
			nil, &raster.Float{Width: 1, Height: 1, Bands: 4, Data: []float32{2, 1, 0.5, 1}},
		},
		{
			"rgb float", with(header{w: 1, h: 1, dxgi: 6}, f32(40, 0.25, -1)),
			nil, &raster.Float{Width: 1, Height: 1, Bands: 3, Data: []float32{40, 0.25, -1}},
		},
		{
			"r float grey", with(header{w: 2, h: 1, dxgi: 41}, f32(3, 0.5)),
			nil, &raster.Float{Width: 2, Height: 1, Bands: 3, Data: []float32{3, 3, 3, 0.5, 0.5, 0.5}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := dds.Decode(tc.in, dds.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(raster.Decoded{Raw: tc.raw, Float: tc.float}, d); d != "" {
				t.Errorf("Decode (-want +got):\n%s", d)
			}
		})
	}
}

// TestDecodeBlocks decodes the block-compressed fixtures: size, band count
// and a sane first pixel (the photo's sky).
func TestDecodeBlocks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		w, h    int
		bands   int
		hdr     bool
	}{
		{"photo-dxt1.dds", 64, 48, 3, false},
		{"alpha-dxt5.dds", 64, 48, 4, false},
		{"bc1-dx10.dds", 64, 48, 4, false},
		{"bc4.dds", 64, 48, 3, false},
		{"bc7-alpha.dds", 64, 48, 4, false},
		{"bc6h-scene.dds", 48, 32, 3, true},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			d, err := dds.Decode(readFixture(t, tc.fixture), dds.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.hdr {
				f := d.Float
				if f == nil || f.Width != tc.w || f.Height != tc.h || f.Bands != tc.bands || f.Validate() != nil {
					t.Fatalf("Decode = %+v, want %dx%d float with %d bands", d, tc.w, tc.h, tc.bands)
				}
				var peak float32
				for _, v := range f.Data {
					peak = max(peak, v)
				}
				if peak < 20 || peak > 45 { // the sun is at 40
					t.Errorf("peak %g, want the sun (~40)", peak)
				}
				return
			}
			r := d.Raw
			if r == nil || r.Width != tc.w || r.Height != tc.h || r.Bands != tc.bands || r.Validate() != nil {
				t.Fatalf("Decode = %+v, want %dx%d with %d bands", d, tc.w, tc.h, tc.bands)
			}
		})
	}
}

func TestDecodeLimits(t *testing.T) {
	t.Parallel()
	big := header{w: 4096, h: 4096, dxgi: 98}.bytes() // no pixel data
	if _, err := dds.Decode(big, dds.Options{MaxPixels: 1 << 20}); errs.KindOf(err) != errs.KindLimitExceeded {
		t.Errorf("over MaxPixels: %v, want KindLimitExceeded", err)
	}
	if _, err := dds.Decode(big, dds.Options{}); errs.KindOf(err) != errs.KindCorrupt {
		t.Errorf("missing data: %v, want KindCorrupt (checked before allocating)", err)
	}
	full := readFixture(t, "bc7-alpha.dds")
	if _, err := dds.Decode(full[:len(full)-1], dds.Options{}); errs.KindOf(err) != errs.KindCorrupt {
		t.Errorf("truncated: %v, want KindCorrupt", err)
	}
}

// FuzzDecode feeds arbitrary files to the decoder: it must not panic, must
// return exactly one kind of result, and never allocate more than the
// input justifies: the pixel data must be present, so the output is at most
// 48 bytes per input byte (BC6H: 16 bytes become 16 RGB float pixels).
func FuzzDecode(f *testing.F) {
	for _, name := range []string{"photo-dxt1.dds", "alpha-dxt5.dds", "bc1-dx10.dds", "bc4.dds", "bc7-alpha.dds", "bc6h-scene.dds"} {
		f.Add(readFixture(f, name))
	}
	f.Add(header{w: 4, h: 4, dxgi: 96, dataLength: 16}.bytes())
	f.Add(header{w: 4, h: 4, flags: 0x41, bpp: 32, masks: [4]uint32{0xFF0000, 0xFF00, 0xFF, 0xFF000000}, dataLength: 64}.bytes())
	f.Add(header{w: 3, h: 2, flags: 0x20, bpp: 8, dataLength: 1024 + 6}.bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		c, cerr := dds.DecodeConfig(b)
		d, err := dds.Decode(b, dds.Options{MaxPixels: 1 << 22})
		if err != nil {
			switch errs.KindOf(err) {
			case errs.KindCorrupt, errs.KindUnsupported, errs.KindLimitExceeded:
			default:
				t.Fatalf("Decode: unexpected kind %v: %v", errs.KindOf(err), err)
			}
			return
		}
		if cerr != nil {
			t.Fatalf("Decode succeeded where DecodeConfig failed: %v", cerr)
		}
		var outBytes int
		switch {
		case d.Raw != nil && d.Float == nil:
			if err := d.Raw.Validate(); err != nil {
				t.Fatal(err)
			}
			outBytes = len(d.Raw.Data)
		case d.Float != nil && d.Raw == nil:
			if err := d.Float.Validate(); err != nil {
				t.Fatal(err)
			}
			outBytes = 4 * len(d.Float.Data)
		default:
			t.Fatalf("Decode returned %+v", d)
		}
		if outBytes > 48*len(b) {
			t.Fatalf("%dx%d decoded to %d bytes from %d input bytes", c.Width, c.Height, outBytes, len(b))
		}
	})
}
