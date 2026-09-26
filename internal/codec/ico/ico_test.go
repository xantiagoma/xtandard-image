package ico

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func readFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// rgba converts a Raw to straight RGBA samples (alpha 255 for 3 bands).
func rgba(r *raster.Raw) []byte {
	out := make([]byte, 0, r.Width*r.Height*4)
	for i := 0; i < len(r.Data); i += r.Bands {
		a := byte(0xFF)
		if r.Bands == 4 {
			a = r.Data[i+3]
		}
		out = append(out, r.Data[i], r.Data[i+1], r.Data[i+2], a)
	}
	return out
}

func decodePNG(tb testing.TB, b []byte) (w, h int, pix []byte) {
	tb.Helper()
	m, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		tb.Fatal(err)
	}
	bb := m.Bounds()
	for y := bb.Min.Y; y < bb.Max.Y; y++ {
		for x := bb.Min.X; x < bb.Max.X; x++ {
			c, _ := color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
			pix = append(pix, c.R, c.G, c.B, c.A)
		}
	}
	return bb.Dx(), bb.Dy(), pix
}

func diffPixels(w int, got, want []byte) string {
	if len(got) != len(want) {
		return fmt.Sprintf("got %d samples, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			p := i / 4
			return fmt.Sprintf("pixel (%d,%d) channel %d: got %d, want %d", p%w, p/w, i%4, got[i], want[i])
		}
	}
	return ""
}

func wantKind(t *testing.T, err error, k errs.Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %v", k)
	}
	if got := errs.KindOf(err); got != k {
		t.Fatalf("error kind %v, want %v (%v)", got, k, err)
	}
}

// build assembles an icon file of type typ (1 ICO, 2 CUR) from payloads. The
// directory's size bytes are left zero: the decoder measures payloads.
func build(typ uint16, payloads ...[]byte) []byte {
	n := dirHeaderLen + dirEntryLen*len(payloads)
	b := make([]byte, n, n+len(bytes.Join(payloads, nil)))
	binary.LittleEndian.PutUint16(b[2:], typ)
	binary.LittleEndian.PutUint16(b[4:], u16(len(payloads)))
	for i, p := range payloads {
		d := b[dirHeaderLen+dirEntryLen*i:]
		binary.LittleEndian.PutUint16(d[4:], 1)
		binary.LittleEndian.PutUint32(d[8:], u32(len(p)))
		binary.LittleEndian.PutUint32(d[12:], u32(len(b)))
		b = append(b, p...)
	}
	return b
}

// testRaw returns a deterministic w×h image; for 4 bands every fifth pixel is
// fully transparent so DIB entries get a non-empty AND mask.
func testRaw(w, h, bands int) *raster.Raw {
	r := &raster.Raw{Width: w, Height: h, Bands: bands, Data: make([]byte, w*h*bands)}
	var v, row byte // byte(i*37 + i/bands*11) without narrowing conversions
	for i := range r.Data {
		if i > 0 && i%bands == 0 {
			row += 11
		}
		r.Data[i] = v*37 + row
		v++
		if bands == 4 && i%4 == 3 && (i/4)%5 == 0 {
			r.Data[i] = 0
		}
	}
	return r
}

func pngBytes(tb testing.TB, r *raster.Raw) []byte {
	tb.Helper()
	m := image.NewNRGBA(image.Rect(0, 0, r.Width, r.Height))
	copy(m.Pix, rgba(r))
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

// dib4 is a 2x2 4-bit DIB entry (doubled height) whose AND mask makes the
// top-right and bottom-left pixels transparent.
func dib4() []byte {
	b := make([]byte, 40, 72)
	le := binary.LittleEndian
	le.PutUint32(b, 40)
	le.PutUint32(b[4:], 2)
	le.PutUint32(b[8:], 4)
	le.PutUint16(b[12:], 1)
	le.PutUint16(b[14:], 4)
	le.PutUint32(b[32:], 4)
	b = append(b,
		0, 0, 0, 0, // palette 0: black
		0, 0, 255, 0, // 1: red (BGR0)
		0, 255, 0, 0, // 2: green
		255, 0, 0, 0, // 3: blue
		0x12, 0, 0, 0, // XOR bottom row: 1 2
		0x30, 0, 0, 0, // XOR top row: 3 0
		0x80, 0, 0, 0, // AND bottom row: x0 transparent
		0x40, 0, 0, 0, // AND top row: x1 transparent
	)
	return b
}

// TestDecodeFixtures decodes every entry of the ImageMagick corpus
// (testdata/gen.sh) and compares it with ImageMagick's own decode.
func TestDecodeFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		file  string
		sizes []int // square entry sizes in directory order
		bands []int
	}{
		{file: "multi", sizes: []int{64, 48, 32, 16}, bands: []int{4, 4, 4, 4}},
		{file: "pal16", sizes: []int{32}, bands: []int{3}},
		{file: "mask1", sizes: []int{16}, bands: []int{4}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, tc.file+".ico")
			if !Sniff(data) {
				t.Fatal("Sniff = false")
			}
			entries, err := Entries(data)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(tc.sizes) {
				t.Fatalf("%d entries, want %d", len(entries), len(tc.sizes))
			}
			for i, size := range tc.sizes {
				e := entries[i]
				if e.Index != i || e.Width != size || e.Height != size || e.PNG {
					t.Errorf("entry %d = %+v, want %dx%d DIB", i, e, size, size)
				}
				d, err := Decode(data, Options{UsePage: true, Page: i})
				if err != nil {
					t.Fatalf("page %d: %v", i, err)
				}
				if d.Raw.Bands != tc.bands[i] {
					t.Errorf("page %d: bands = %d, want %d", i, d.Raw.Bands, tc.bands[i])
				}
				w, _, want := decodePNG(t, readFixture(t, fmt.Sprintf("%s-%d.ref.png", tc.file, i)))
				if s := diffPixels(w, rgba(d.Raw), want); s != "" {
					t.Errorf("page %d vs ImageMagick: %s", i, s)
				}
			}
		})
	}
}

func TestPick(t *testing.T) {
	t.Parallel()
	data := readFixture(t, "multi.ico")
	tests := []struct {
		name       string
		minW, minH int
		want       int // entry width
	}{
		{name: "largest", want: 64},
		{name: "smallest covering 20x20", minW: 20, minH: 20, want: 32},
		{name: "exact 48", minW: 48, minH: 48, want: 48},
		{name: "width only", minW: 10, want: 16},
		{name: "none covers: largest", minW: 100, minH: 100, want: 64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d, err := Decode(data, Options{MinWidth: tc.minW, MinHeight: tc.minH})
			if err != nil {
				t.Fatal(err)
			}
			if d.Raw.Width != tc.want {
				t.Errorf("picked %d px, want %d", d.Raw.Width, tc.want)
			}
		})
	}

	// Equal sizes: the deeper entry wins.
	e := Pick([]Entry{{Index: 0, Width: 16, Height: 16, BitCount: 8}, {Index: 1, Width: 16, Height: 16, BitCount: 32}}, 0, 0)
	if e.Index != 1 {
		t.Errorf("tie: picked %d, want 1", e.Index)
	}
}

func TestDecodeCrafted(t *testing.T) {
	t.Parallel()
	big := testRaw(300, 200, 4) // larger than a directory byte can say
	bigPNG := pngBytes(t, big)
	small := pngBytes(t, testRaw(8, 8, 3))

	t.Run("png entry", func(t *testing.T) {
		t.Parallel()
		data := build(typeIcon, dib4(), bigPNG, small)
		entries, err := Entries(data)
		if err != nil {
			t.Fatal(err)
		}
		want := []Entry{
			{Index: 0, Width: 2, Height: 2, BitCount: 4},
			{Index: 1, Width: 300, Height: 200, BitCount: 32, PNG: true},
			{Index: 2, Width: 8, Height: 8, BitCount: 24, PNG: true},
		}
		if diff := cmp.Diff(want, entries, cmp.Comparer(func(a, b Entry) bool {
			return a.Index == b.Index && a.Width == b.Width && a.Height == b.Height &&
				a.BitCount == b.BitCount && a.PNG == b.PNG
		})); diff != "" {
			t.Errorf("Entries (-want +got):\n%s", diff)
		}
		d, err := Decode(data, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if d.Raw != nil || !bytes.Equal(d.Embedded, bigPNG) {
			t.Errorf("best entry: Embedded %d bytes, want the %d-byte PNG", len(d.Embedded), len(bigPNG))
		}
	})

	t.Run("dib with AND mask", func(t *testing.T) {
		t.Parallel()
		d, err := Decode(build(typeIcon, dib4()), Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := []byte{
			0, 0, 255, 255, 0, 0, 0, 0, // top: blue, black masked
			255, 0, 0, 0, 0, 255, 0, 255, // bottom: red masked, green
		}
		if d.Raw.Bands != 4 {
			t.Errorf("bands = %d, want 4", d.Raw.Bands)
		}
		if s := diffPixels(2, rgba(d.Raw), want); s != "" {
			t.Error(s)
		}
	})

	t.Run("cursor", func(t *testing.T) {
		t.Parallel()
		data := build(typeCursor, dib4())
		if !Sniff(data) {
			t.Error("Sniff(CUR) = false")
		}
		if _, err := Decode(data, Options{}); err != nil {
			t.Error(err)
		}
	})

	t.Run("broken entries are skipped", func(t *testing.T) {
		t.Parallel()
		data := build(typeIcon, []byte("junk"), dib4())
		binary.LittleEndian.PutUint32(data[dirHeaderLen+8:], 1<<30) // entry 0 size past EOF
		entries, err := Entries(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Index != 1 {
			t.Fatalf("entries = %+v, want only index 1", entries)
		}
		_, err = Decode(data, Options{UsePage: true, Page: 0})
		wantKind(t, err, errs.KindCorrupt)
	})
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	valid := build(typeIcon, dib4())
	tests := []struct {
		name string
		data []byte
		opts Options
		kind errs.Kind
	}{
		{name: "empty"},
		{name: "reserved", data: append([]byte{1}, valid[1:]...)},
		{name: "type", data: append([]byte{0, 0, 3}, valid[3:]...)},
		{name: "no entries", data: []byte{0, 0, 1, 0, 0, 0}},
		{name: "truncated directory", data: valid[:dirHeaderLen+8]},
		{name: "only junk", data: build(typeIcon, []byte("not an image at all, not even close"))},
		{name: "truncated png header", data: build(typeIcon, pngSignature)},
		{name: "page not found", data: valid, opts: Options{UsePage: true, Page: 3}},
		{name: "page negative", data: valid, opts: Options{UsePage: true, Page: -1}},
		{name: "dib over limit", data: valid, opts: Options{MaxPixels: 3}, kind: errs.KindLimitExceeded},
		{
			name: "png over limit", kind: errs.KindLimitExceeded,
			data: build(typeIcon, pngBytes(t, testRaw(4, 4, 3))), opts: Options{MaxPixels: 15},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := tc.kind
			if want == errs.KindUnknown {
				want = errs.KindCorrupt
			}
			_, err := Decode(tc.data, tc.opts)
			wantKind(t, err, want)
		})
	}
}

func TestSniff(t *testing.T) {
	t.Parallel()
	valid := build(typeIcon, dib4())
	tests := []struct {
		name string
		head []byte
		want bool
	}{
		{name: "empty"},
		{name: "icon", head: valid, want: true},
		{name: "cursor", head: build(typeCursor, dib4()), want: true},
		{name: "directory only", head: valid[:6], want: true},
		{name: "fixture", head: readFixture(t, "multi.ico"), want: true},
		{name: "zero entries", head: []byte{0, 0, 1, 0, 0, 0}},
		{name: "type 3", head: []byte{0, 0, 3, 0, 1, 0}},
		{name: "offset inside directory", head: func() []byte {
			b := bytes.Clone(valid)
			binary.LittleEndian.PutUint32(b[dirHeaderLen+12:], 6)
			return b
		}()},
		{name: "reserved byte", head: func() []byte {
			b := bytes.Clone(valid)
			b[dirHeaderLen+3] = 1
			return b
		}()},
		{name: "bmp", head: []byte("BM\x00\x00")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Sniff(tc.head); got != tc.want {
				t.Errorf("Sniff = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	frames := []*raster.Raw{
		testRaw(16, 16, 3),   // 24-bit DIB
		testRaw(16, 16, 4),   // 32-bit DIB + AND mask
		testRaw(48, 48, 4),   // largest DIB
		testRaw(7, 5, 3),     // odd sizes, row padding
		testRaw(49, 30, 3),   // PNG, RGB
		testRaw(256, 256, 4), // PNG, stored as 0 in the directory
	}
	data, err := Encode(frames...)
	if err != nil {
		t.Fatal(err)
	}
	if !Sniff(data) {
		t.Fatal("Sniff(encoded) = false")
	}
	entries, err := Entries(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(frames) {
		t.Fatalf("%d entries, want %d", len(entries), len(frames))
	}
	for i, f := range frames {
		d, err := Decode(data, Options{UsePage: true, Page: i})
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		png := f.Width > MaxDIBSize || f.Height > MaxDIBSize
		if entries[i].PNG != png {
			t.Errorf("frame %d: PNG = %v, want %v", i, entries[i].PNG, png)
		}
		if png {
			w, h, got := decodePNG(t, d.Embedded)
			if w != f.Width || h != f.Height {
				t.Fatalf("frame %d: %dx%d, want %dx%d", i, w, h, f.Width, f.Height)
			}
			if s := diffPixels(w, got, rgba(f)); s != "" {
				t.Errorf("frame %d: %s", i, s)
			}
			continue
		}
		if diff := cmp.Diff(f, d.Raw); diff != "" {
			t.Errorf("frame %d round trip (-want +got):\n%s", i, diff)
		}
	}
}

func TestEncodeInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		frames []*raster.Raw
		kind   errs.Kind
	}{
		{name: "no frames", kind: errs.KindInternal},
		{name: "too wide", frames: []*raster.Raw{testRaw(257, 1, 3)}, kind: errs.KindLimitExceeded},
		{name: "invalid raw", frames: []*raster.Raw{{Width: 1, Height: 1, Bands: 3}}, kind: errs.KindInternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Encode(tc.frames...)
			wantKind(t, err, tc.kind)
		})
	}
}
