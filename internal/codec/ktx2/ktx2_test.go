package ktx2_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/codec/dds"
	"github.com/xantiagoma/xtandard-image/internal/codec/ktx2"
	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// fakeBackend stands in for the Basis Universal transcoder and Zstandard
// (internal/vips): it returns a fixed image and "decompresses" by copying.
type fakeBackend struct {
	info  ktx2.BasisInfo
	img   ktx2.BasisImage
	err   error
	calls int
}

func (f *fakeBackend) BasisInfo(context.Context, []byte) (ktx2.BasisInfo, error) {
	return f.info, f.err
}

func (f *fakeBackend) Basis(_ context.Context, _ []byte, _ int64) (ktx2.BasisImage, error) {
	f.calls++
	return f.img, f.err
}

func (f *fakeBackend) Zstd(_ context.Context, dst, src []byte) error {
	if f.err != nil {
		return f.err
	}
	copy(dst, src)
	return nil
}

// file builds a KTX2 file with one level: the stored level data and its
// uncompressed length.
type file struct {
	vk, scheme            uint32
	w, h, depth, layers   uint32
	faces, levels         uint32
	level                 []byte
	uncompressed          uint64
	levelOffset, levelLen uint64 // override the computed entry when non-zero
	truncateIndex         bool
}

func (f file) bytes() []byte {
	le := binary.LittleEndian
	if f.faces == 0 {
		f.faces = 1
	}
	if f.levels == 0 {
		f.levels = 1
	}
	b := []byte(ktx2.Identifier)
	for _, v := range []uint32{f.vk, 1, f.w, f.h, f.depth, f.layers, f.faces, f.levels, f.scheme} {
		b = le.AppendUint32(b, v)
	}
	b = append(b, make([]byte, 32)...) // DFD, KVD and SGD index: empty
	if f.truncateIndex {
		return b
	}
	off, n := uint64(80+24*int(f.levels)), uint64(len(f.level))
	if f.levelOffset != 0 {
		off = f.levelOffset
	}
	if f.levelLen != 0 {
		n = f.levelLen
	}
	for range f.levels {
		b = le.AppendUint64(b, off)
		b = le.AppendUint64(b, n)
		b = le.AppendUint64(b, f.uncompressed)
	}
	return append(b, f.level...)
}

func zlibBytes(tb testing.TB, p []byte) []byte {
	tb.Helper()
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(p); err != nil {
		tb.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	return z.Bytes()
}

func TestDecodeConfig(t *testing.T) {
	t.Parallel()
	ignore := cmpopts.IgnoreUnexported(ktx2.Config{})
	rgba := make([]byte, 4*3*4)
	for _, tc := range []struct {
		name string
		in   []byte
		be   ktx2.Backend
		want ktx2.Config
	}{
		{"rgba8 fixture", readFixture(t, "ktx2/rgba8.ktx2"), nil, ktx2.Config{
			Width: 64, Height: 48, VkFormat: 43, Format: "R8G8B8A8", Alpha: true, Levels: 1, Faces: 1,
		}},
		{"rgb8 fixture", readFixture(t, "ktx2/rgb8.ktx2"), nil, ktx2.Config{
			Width: 64, Height: 48, VkFormat: 23, Format: "R8G8B8", Levels: 1, Faces: 1,
		}},
		{"bc1 fixture (no alpha)", readFixture(t, "ktx2/bc1.ktx2"), nil, ktx2.Config{
			Width: 64, Height: 48, VkFormat: 131, Format: "BC1", Levels: 1, Faces: 1,
		}},
		{"bc4 fixture", readFixture(t, "ktx2/bc4.ktx2"), nil, ktx2.Config{
			Width: 64, Height: 48, VkFormat: 139, Format: "BC4", Grey: true, Levels: 1, Faces: 1,
		}},
		{"bc6h fixture", readFixture(t, "ktx2/bc6h.ktx2"), nil, ktx2.Config{
			Width: 64, Height: 48, VkFormat: 143, Format: "BC6H_UFLOAT", HDR: true, Levels: 1, Faces: 1,
		}},
		{"zlib fixture", readFixture(t, "ktx2/rgba8-zlib.ktx2"), nil, ktx2.Config{
			Width: 64, Height: 48, VkFormat: 43, Format: "R8G8B8A8", Scheme: ktx2.SupercompressionZlib,
			Alpha: true, Levels: 1, Faces: 1,
		}},
		{"etc1s fixture, no backend", readFixture(t, "ktx2/etc1s.ktx2"), nil, ktx2.Config{
			Width: 64, Height: 48, Format: "Basis Universal", Scheme: ktx2.SupercompressionBasisLZ, Basis: true,
			Levels: 1, Faces: 1,
		}},
		{"etc1s fixture, backend says alpha", readFixture(t, "ktx2/etc1s.ktx2"),
			&fakeBackend{info: ktx2.BasisInfo{Width: 64, Height: 48, Alpha: true}}, ktx2.Config{
				Width: 64, Height: 48, Format: "Basis Universal", Scheme: ktx2.SupercompressionBasisLZ, Basis: true,
				Alpha: true, Levels: 1, Faces: 1,
			}},
		{"astc goes to the transcoder", file{vk: 157, w: 4, h: 4, level: make([]byte, 16)}.bytes(), nil, ktx2.Config{
			Width: 4, Height: 4, VkFormat: 157, Format: "ASTC", Basis: true, Levels: 1, Faces: 1,
		}},
		{"1d texture", file{vk: 37, w: 3, h: 0, level: make([]byte, 12)}.bytes(), nil, ktx2.Config{
			Width: 3, Height: 1, VkFormat: 37, Format: "R8G8B8A8", Alpha: true, Levels: 1, Faces: 1,
		}},
		{"cube map", file{vk: 37, w: 3, h: 4, faces: 6, levels: 1, level: append(rgba, make([]byte, 5*len(rgba))...)}.bytes(), nil,
			ktx2.Config{Width: 3, Height: 4, VkFormat: 37, Format: "R8G8B8A8", Alpha: true, Levels: 1, Faces: 6}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ktx2.DecodeConfig(t.Context(), tc.in, tc.be)
			if err != nil {
				t.Fatalf("DecodeConfig: %v", err)
			}
			if diff := cmp.Diff(tc.want, got, ignore); diff != "" {
				t.Errorf("DecodeConfig mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDecodeConfigErrors(t *testing.T) {
	t.Parallel()
	good := file{vk: 37, w: 2, h: 2, level: make([]byte, 16)}
	with := func(fn func(f *file)) []byte {
		f := good
		fn(&f)
		return f.bytes()
	}
	for _, tc := range []struct {
		name string
		in   []byte
		kind errs.Kind
	}{
		{"not ktx2", []byte("\xabKTX 11\xbb\r\n\x1a\n" + string(make([]byte, 100))), errs.KindCorrupt},
		{"truncated header", []byte(ktx2.Identifier + "\x25\x00"), errs.KindCorrupt},
		{"truncated index", with(func(f *file) { f.truncateIndex = true }), errs.KindCorrupt},
		{"zero width", with(func(f *file) { f.w = 0 }), errs.KindCorrupt},
		{"huge width", with(func(f *file) { f.w = 1 << 20 }), errs.KindCorrupt},
		{"five faces", with(func(f *file) { f.faces = 5 }), errs.KindCorrupt},
		{"too many levels", with(func(f *file) { f.levels = 40 }), errs.KindCorrupt},
		{"level outside the file", with(func(f *file) { f.levelOffset = 1 << 40 }), errs.KindCorrupt},
		{"level longer than the file", with(func(f *file) { f.levelLen = 1 << 20 }), errs.KindCorrupt},
		{"level shorter than the image", with(func(f *file) { f.level = make([]byte, 15) }), errs.KindCorrupt},
		{"zlib without an uncompressed size", with(func(f *file) { f.scheme = 3 }), errs.KindCorrupt},
		{"etc2", with(func(f *file) { f.vk = 147 }), errs.KindUnsupported},
		{"pvrtc", with(func(f *file) { f.vk = 1000054000 }), errs.KindUnsupported},
		{"unknown vk format", with(func(f *file) { f.vk = 70 }), errs.KindUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ktx2.DecodeConfig(t.Context(), tc.in, nil)
			if errs.KindOf(err) != tc.kind {
				t.Fatalf("DecodeConfig error %v (kind %v), want kind %v", err, errs.KindOf(err), tc.kind)
			}
		})
	}
}

// TestDecodeFixtures decodes the Go-route fixtures and compares them with
// the same image decoded from the DDS fixtures of codec/dds where the
// payload is identical (the generator writes both from one encoder).
func TestDecodeFixtures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		bands int
		hdr   bool
	}{
		{"ktx2/bc1.ktx2", 3, false},
		{"ktx2/bc4.ktx2", 3, false},
		{"ktx2/bc7.ktx2", 4, false},
		{"ktx2/bc6h.ktx2", 3, true},
		{"ktx2/rgba8.ktx2", 4, false},
		{"ktx2/rgb8.ktx2", 3, false},
		{"ktx2/rgba8-zlib.ktx2", 4, false},
		{"ktx2/rgba8-zstd.ktx2", 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := ktx2.Decode(t.Context(), readFixture(t, tc.name), ktx2.Options{Backend: &zstdByHand{t: t}})
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			w, h, bands := 0, 0, 0
			switch {
			case tc.hdr && d.Float != nil:
				w, h, bands = d.Float.Width, d.Float.Height, d.Float.Bands
			case !tc.hdr && d.Raw != nil:
				w, h, bands = d.Raw.Width, d.Raw.Height, d.Raw.Bands
			default:
				t.Fatalf("Decode returned raw %v float %v", d.Raw != nil, d.Float != nil)
			}
			if w != 64 || h != 48 || bands != tc.bands {
				t.Errorf("decoded %dx%dx%d, want 64x48x%d", w, h, bands, tc.bands)
			}
		})
	}
}

// zstdByHand is a Backend whose Zstd reads the uncompressed reference the
// generator also wrote: the zstd fixture must decode to the same pixels as
// the raw RGBA8 one.
type zstdByHand struct {
	t *testing.T
}

func (*zstdByHand) BasisInfo(context.Context, []byte) (ktx2.BasisInfo, error) {
	return ktx2.BasisInfo{}, errs.New(errs.KindUnsupported, "no transcoder")
}

func (*zstdByHand) Basis(context.Context, []byte, int64) (ktx2.BasisImage, error) {
	return ktx2.BasisImage{}, errs.New(errs.KindUnsupported, "no transcoder")
}

func (z *zstdByHand) Zstd(_ context.Context, dst, _ []byte) error {
	ref, err := ktx2.Decode(z.t.Context(), readFixture(z.t, "ktx2/rgba8.ktx2"), ktx2.Options{})
	if err != nil {
		return err
	}
	copy(dst, ref.Raw.Data)
	return nil
}

func TestDecodeSameAsDDS(t *testing.T) {
	t.Parallel()
	// The raw RGBA8 fixture and its zlib twin decode to identical pixels.
	raw, err := ktx2.Decode(t.Context(), readFixture(t, "ktx2/rgba8.ktx2"), ktx2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	z, err := ktx2.Decode(t.Context(), readFixture(t, "ktx2/rgba8-zlib.ktx2"), ktx2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw.Raw.Data, z.Raw.Data) {
		t.Error("zlib level decodes to other pixels than the raw one")
	}
	// A KTX2 level of BC7 blocks decodes like the same blocks in a DDS file.
	k := readFixture(t, "ktx2/bc7.ktx2")
	c, err := ktx2.DecodeConfig(t.Context(), k, nil)
	if err != nil {
		t.Fatal(err)
	}
	blocks := k[len(k)-(c.Width+3)/4*((c.Height+3)/4)*16:]
	ddsFile := append(ddsHeader(c.Width, c.Height, 99), blocks...)
	want, err := dds.Decode(ddsFile, dds.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ktx2.Decode(t.Context(), k, ktx2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want.Raw.Data, got.Raw.Data) {
		t.Error("BC7 KTX2 decodes to other pixels than the same blocks in a DDS file")
	}
}

func ddsHeader(w, h int, dxgi uint32) []byte {
	le := binary.LittleEndian
	b := make([]byte, 148)
	copy(b, "DDS ")
	le.PutUint32(b[4:], 124)
	le.PutUint32(b[12:], uint32(h)) //nolint:gosec // test sizes
	le.PutUint32(b[16:], uint32(w)) //nolint:gosec // test sizes
	le.PutUint32(b[76:], 32)
	le.PutUint32(b[80:], 4)
	copy(b[84:], "DX10")
	le.PutUint32(b[128:], dxgi)
	le.PutUint32(b[132:], 3)
	le.PutUint32(b[140:], 1)
	return b
}

func TestDecodeBasis(t *testing.T) {
	t.Parallel()
	in := readFixture(t, "ktx2/etc1s.ktx2")
	px := func(n int, v byte) []byte { return bytes.Repeat([]byte{v}, n) }
	for _, tc := range []struct {
		name  string
		be    *fakeBackend
		check func(t *testing.T, d raster.Decoded)
		kind  errs.Kind
	}{
		{"opaque drops alpha", &fakeBackend{img: ktx2.BasisImage{
			BasisInfo: ktx2.BasisInfo{Width: 64, Height: 48}, RGBA8: px(64*48*4, 7),
		}}, func(t *testing.T, d raster.Decoded) {
			if d.Raw == nil || d.Raw.Bands != 3 || len(d.Raw.Data) != 64*48*3 || d.Raw.Data[0] != 7 {
				t.Errorf("raw %+v", d.Raw)
			}
		}, 0},
		{"alpha kept", &fakeBackend{img: ktx2.BasisImage{
			BasisInfo: ktx2.BasisInfo{Width: 64, Height: 48, Alpha: true}, RGBA8: px(64*48*4, 9),
		}}, func(t *testing.T, d raster.Decoded) {
			if d.Raw == nil || d.Raw.Bands != 4 {
				t.Errorf("raw %+v", d.Raw)
			}
		}, 0},
		{"hdr to float", &fakeBackend{img: ktx2.BasisImage{
			BasisInfo: ktx2.BasisInfo{Width: 64, Height: 48, HDR: true},
			RGBAHalf:  make([]uint16, 64*48*4),
		}}, func(t *testing.T, d raster.Decoded) {
			if d.Float == nil || d.Float.Bands != 3 {
				t.Errorf("float %+v", d.Float)
			}
		}, 0},
		{"size mismatch", &fakeBackend{img: ktx2.BasisImage{
			BasisInfo: ktx2.BasisInfo{Width: 32, Height: 48}, RGBA8: px(32*48*4, 0),
		}}, nil, errs.KindCorrupt},
		{"short pixels", &fakeBackend{img: ktx2.BasisImage{
			BasisInfo: ktx2.BasisInfo{Width: 64, Height: 48}, RGBA8: px(10, 0),
		}}, nil, errs.KindCorrupt},
		{"transcoder error kept", &fakeBackend{err: errs.New(errs.KindLimitExceeded, "too big")}, nil, errs.KindLimitExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := ktx2.Decode(t.Context(), in, ktx2.Options{Backend: tc.be})
			if tc.kind != 0 {
				if errs.KindOf(err) != tc.kind {
					t.Fatalf("Decode error %v, want kind %v", err, tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			tc.check(t, d)
		})
	}
	if _, err := ktx2.Decode(t.Context(), in, ktx2.Options{}); errs.KindOf(err) != errs.KindUnsupported {
		t.Errorf("Decode without a backend: %v, want unsupported", err)
	}
}

func TestDecodeLimits(t *testing.T) {
	t.Parallel()
	be := &fakeBackend{}
	_, err := ktx2.Decode(t.Context(), readFixture(t, "ktx2/etc1s.ktx2"), ktx2.Options{MaxPixels: 100, Backend: be})
	if errs.KindOf(err) != errs.KindLimitExceeded || be.calls != 0 {
		t.Errorf("Decode over the limit: %v after %d transcoder calls", err, be.calls)
	}
	_, err = ktx2.Decode(t.Context(), readFixture(t, "ktx2/rgba8.ktx2"), ktx2.Options{MaxPixels: 100})
	if errs.KindOf(err) != errs.KindLimitExceeded {
		t.Errorf("Decode over the limit: %v", err)
	}
}

func TestDecodeSupercompressionErrors(t *testing.T) {
	t.Parallel()
	img := make([]byte, 2*2*4)
	for _, tc := range []struct {
		name string
		f    file
		be   ktx2.Backend
		kind errs.Kind
	}{
		{"zlib garbage", file{vk: 37, w: 2, h: 2, scheme: 3, level: []byte("not zlib at all"), uncompressed: 16}, nil, errs.KindCorrupt},
		{"zlib too short", file{vk: 37, w: 2, h: 2, scheme: 3, level: zlibBytes(t, img[:8]), uncompressed: 16}, nil, errs.KindCorrupt},
		{"zlib declares less than an image", file{vk: 37, w: 2, h: 2, scheme: 3, level: zlibBytes(t, img), uncompressed: 8}, nil, errs.KindCorrupt},
		{"zlib declares more than the level", file{vk: 37, w: 2, h: 2, scheme: 3, level: zlibBytes(t, img), uncompressed: 64}, nil, errs.KindCorrupt},
		{"zstd without a decoder", file{vk: 37, w: 2, h: 2, scheme: 2, level: make([]byte, 16), uncompressed: 16}, nil, errs.KindUnsupported},
		{"zstd decoder error", file{vk: 37, w: 2, h: 2, scheme: 2, level: make([]byte, 16), uncompressed: 16},
			&fakeBackend{err: errs.New(errs.KindCorrupt, "bad frame")}, errs.KindCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ktx2.Decode(t.Context(), tc.f.bytes(), ktx2.Options{Backend: tc.be})
			if errs.KindOf(err) != tc.kind {
				t.Fatalf("Decode error %v (kind %v), want kind %v", err, errs.KindOf(err), tc.kind)
			}
		})
	}
	// A zlib level with its exact size decodes.
	good := file{vk: 37, w: 2, h: 2, scheme: 3, level: zlibBytes(t, img), uncompressed: 16}
	if _, err := ktx2.Decode(t.Context(), good.bytes(), ktx2.Options{}); err != nil {
		t.Errorf("Decode zlib: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	for _, name := range []string{"bc1", "bc4", "bc6h", "bc7", "rgba8", "rgb8", "rgba8-zlib", "rgba8-zstd", "etc1s"} {
		f.Add(readFixture(f, "ktx2/"+name+".ktx2"))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		be := &fakeBackend{err: errs.New(errs.KindCorrupt, "fake")}
		c, cerr := ktx2.DecodeConfig(t.Context(), b, nil)
		d, err := ktx2.Decode(t.Context(), b, ktx2.Options{MaxPixels: 1 << 20, Backend: be})
		if err != nil {
			return
		}
		if cerr != nil {
			t.Fatalf("Decode succeeded where DecodeConfig failed: %v", cerr)
		}
		switch {
		case d.Raw != nil:
			if err := d.Raw.Validate(); err != nil || d.Raw.Width != c.Width || d.Raw.Height != c.Height {
				t.Fatalf("raw %dx%d (%v), config %dx%d", d.Raw.Width, d.Raw.Height, err, c.Width, c.Height)
			}
		case d.Float != nil:
			if err := d.Float.Validate(); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("no pixels")
		}
	})
}
