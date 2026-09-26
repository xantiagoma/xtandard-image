package imageinfo

import (
	"encoding/hex"
	"encoding/json"
	"image"
	"image/draw"
	"image/png"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// loadPNG decodes testdata/<path> to non-premultiplied RGBA.
func loadPNG(tb testing.TB, path string) *Image {
	tb.Helper()
	f, err := os.Open(filepath.Join("testdata", path))
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		tb.Fatal(err)
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return &Image{Width: b.Dx(), Height: b.Dy(), Pix: dst.Pix}
}

// solid is a w×h image of one colour.
func solid(w, h int, c Color) *Image {
	m := &Image{Width: w, Height: h, Pix: make([]byte, w*h*4)}
	for i := 0; i < len(m.Pix); i += 4 {
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return m
}

// halves is a w×h image whose left `left` columns are a and the rest b.
func halves(w, h, left int, a, b Color) *Image {
	m := solid(w, h, b)
	for y := range h {
		for x := range left {
			i := (y*w + x) * 4
			m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = a.R, a.G, a.B, a.A
		}
	}
	return m
}

var (
	red    = Color{255, 0, 0, 255}
	blue   = Color{0, 0, 255, 255}
	white  = Color{255, 255, 255, 255}
	black  = Color{0, 0, 0, 255}
	clear0 = Color{10, 20, 30, 0}
)

type vectorFile[V any] struct {
	Vectors []V `json:"vectors"`
}

func readVectors[V any](t *testing.T, path string) []V {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", path))
	if err != nil {
		t.Fatal(err)
	}
	var f vectorFile[V]
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	return f.Vectors
}

// TestBlurhashReference compares with the reference C encoder on its own
// test images (testdata/blurhash/vectors.json).
func TestBlurhashReference(t *testing.T) {
	t.Parallel()
	type vector struct {
		Image string `json:"image"`
		X     int    `json:"x"`
		Y     int    `json:"y"`
		Hash  string `json:"hash"`
	}
	for _, v := range readVectors[vector](t, "blurhash/vectors.json") {
		t.Run(v.Image+"/"+strconv.Itoa(v.X)+"x"+strconv.Itoa(v.Y), func(t *testing.T) {
			t.Parallel()
			got, err := Blurhash(loadPNG(t, "blurhash/"+v.Image), v.X, v.Y)
			if err != nil {
				t.Fatal(err)
			}
			if got != v.Hash {
				t.Errorf("Blurhash = %q, want %q", got, v.Hash)
			}
		})
	}
}

// TestThumbhashReference compares with the reference JavaScript encoder
// (testdata/thumbhash/vectors.json).
func TestThumbhashReference(t *testing.T) {
	t.Parallel()
	type vector struct {
		Image string `json:"image"`
		Hash  string `json:"hash"`
	}
	for _, v := range readVectors[vector](t, "thumbhash/vectors.json") {
		t.Run(v.Image, func(t *testing.T) {
			t.Parallel()
			got, err := Thumbhash(loadPNG(t, "thumbhash/"+v.Image))
			if err != nil {
				t.Fatal(err)
			}
			if h := strings.ToUpper(hex.EncodeToString(got)); h != v.Hash {
				t.Errorf("Thumbhash = %s, want %s", h, v.Hash)
			}
		})
	}
}

func TestBlurhashSolid(t *testing.T) {
	t.Parallel()
	// Header: size flag 3x2 → 2+1*9 = 11 = "B", then (after the AC
	// maximum, which the reference's x/w basis makes non-zero even for a
	// flat image) the DC 0xffffff = "TSUA"; 5 AC pairs.
	got, err := Blurhash(solid(20, 10, white), 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2+4+5*2 || got[0] != 'B' || got[2:6] != "TSUA" {
		t.Errorf("Blurhash = %q, want B?TSUA + 10 characters", got)
	}
	for _, tc := range [][2]int{{0, 1}, {1, 0}, {10, 1}, {1, 10}} {
		if _, err := Blurhash(solid(2, 2, red), tc[0], tc[1]); err == nil {
			t.Errorf("Blurhash(%v) accepted", tc)
		}
	}
}

func TestThumbhashLimits(t *testing.T) {
	t.Parallel()
	if _, err := Thumbhash(solid(101, 10, red)); err == nil {
		t.Error("Thumbhash accepted 101x10")
	}
	if _, err := Thumbhash(&Image{Width: 2, Height: 2}); err == nil {
		t.Error("Thumbhash accepted an image without pixels")
	}
}

func TestAverage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		img    *Image
		ignore bool
		want   Color
	}{
		{"solid", solid(7, 5, Color{12, 34, 56, 255}), true, Color{12, 34, 56, 255}},
		{"two colours", halves(4, 2, 2, red, blue), true, Color{128, 0, 128, 255}},         // 127.5 rounds up
		{"three quarters", halves(4, 1, 3, white, black), true, Color{191, 191, 191, 255}}, // 191.25
		{"transparent ignored", halves(4, 1, 2, red, clear0), true, red},
		{"transparent counted", halves(4, 1, 2, red, clear0), false, Color{133, 10, 15, 128}},
		{"all transparent", solid(3, 3, clear0), true, Color{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Average(tc.img, tc.ignore)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Average = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPalette(t *testing.T) {
	t.Parallel()
	gradient := &Image{Width: 256, Height: 1, Pix: make([]byte, 256*4)}
	for x := range 256 {
		gradient.Pix[x*4], gradient.Pix[x*4+3] = byte(x), 255
	}
	for _, tc := range []struct {
		name string
		img  *Image
		n    int
		want []Color
	}{
		{"solid", solid(9, 9, Color{1, 2, 3, 255}), 4, []Color{{1, 2, 3, 255}}},
		{"two colours, most common first", halves(10, 3, 3, red, blue), 2, []Color{blue, red}},
		{"two colours, room for more", halves(10, 3, 7, red, blue), 16, []Color{red, blue}},
		{"transparent is one colour", halves(4, 4, 1, clear0, white), 2, []Color{white, {}}},
		{"gradient halves", gradient, 2, []Color{{64, 0, 0, 255}, {192, 0, 0, 255}}}, // means of 0..127 and 128..255
		{"gradient quarters", gradient, 4, []Color{{32, 0, 0, 255}, {96, 0, 0, 255}, {160, 0, 0, 255}, {224, 0, 0, 255}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Palette(tc.img, tc.n)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Palette (-want +got):\n%s", diff)
			}
		})
	}
	for _, n := range []int{-1, 0, 1, 257} {
		if _, err := Palette(solid(1, 1, red), n); err == nil {
			t.Errorf("Palette(%d) accepted", n)
		}
	}
}

func TestPaletteBounded(t *testing.T) {
	t.Parallel()
	m := loadPNG(t, "thumbhash/flower.png")
	for _, n := range []int{2, 5, 64, 256} {
		got, err := Palette(m, n)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != n {
			t.Errorf("Palette(%d) has %d colours", n, len(got))
		}
	}
}

func rgb(r, g, b uint8) *RGB { return &RGB{r, g, b} }

func TestDominant(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		img    *Image
		build  bool
		want   DominantColors
		exacts bool
	}{
		{
			// Pure red: s=1, l=0.5 is Vibrant and nothing else.
			name: "red", img: solid(8, 8, red),
			want: DominantColors{Vibrant: rgb(255, 0, 0)},
		},
		{
			// Red (Vibrant) and a dark desaturated blue (DarkMuted:
			// 40,40,60 is s=0.2, l≈0.196).
			name: "red and dark blue", img: halves(8, 8, 3, red, Color{40, 40, 60, 255}),
			want: DominantColors{Vibrant: rgb(255, 0, 0), DarkMuted: rgb(40, 40, 60)},
		},
		{
			// White is LightMuted (s=0, l=1); transparent pixels are
			// ignored.
			name: "white on transparent", img: halves(8, 8, 4, white, clear0),
			want: DominantColors{LightMuted: rgb(255, 255, 255)},
		},
		{
			name: "all transparent", img: solid(4, 4, clear0),
			build: true,
		},
		{
			// Built from Vibrant red (h=0): vibrant tones keep s=1 with the
			// target lumas 0.74 and 0.26; muted ones take s=0.3.
			name: "red, build missed", img: solid(8, 8, red), build: true,
			want: DominantColors{
				Vibrant:      rgb(255, 0, 0),
				LightVibrant: rgb(255, 122, 122), // hue 0, saturation 1, lightness 0.74
				DarkVibrant:  rgb(133, 0, 0),     // hue 0, saturation 1, lightness 0.26
				Muted:        rgb(166, 89, 89),   // hue 0, saturation 0.3, lightness 0.5
				LightMuted:   rgb(209, 169, 169), // hue 0, saturation 0.3, lightness 0.74
				DarkMuted:    rgb(86, 46, 46),    // hue 0, saturation 0.3, lightness 0.26
			},
		},
		{
			// An achromatic source builds greys only.
			name: "white, build missed", img: solid(8, 8, white), build: true,
			want: DominantColors{
				Vibrant: rgb(128, 128, 128), LightVibrant: rgb(189, 189, 189), DarkVibrant: rgb(66, 66, 66),
				Muted: rgb(128, 128, 128), LightMuted: rgb(255, 255, 255), DarkMuted: rgb(66, 66, 66),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Dominant(tc.img, tc.build)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Dominant (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHSLRoundTrip(t *testing.T) {
	t.Parallel()
	for _, c := range []RGB{{0, 0, 0}, {255, 255, 255}, {255, 0, 0}, {12, 200, 99}, {40, 40, 60}, {250, 10, 128}} {
		h, s, l := rgbToHSL(c.R, c.G, c.B)
		if got := hslToRGB(h, s, l); got != c {
			t.Errorf("hsl round trip of %v = %v", c, got)
		}
	}
}

func TestPerceptualHash(t *testing.T) {
	t.Parallel()
	flower := loadPNG(t, "thumbhash/flower.png")
	base, err := PerceptualHash(flower)
	if err != nil {
		t.Fatal(err)
	}
	// Same picture at another size: nearly the same hash.
	small, err := Downscale(flower, 40)
	if err != nil {
		t.Fatal(err)
	}
	scaled, err := PerceptualHash(small)
	if err != nil {
		t.Fatal(err)
	}
	if d := bits.OnesCount64(base ^ scaled); d > 6 {
		t.Errorf("flower vs downscaled flower: distance %d (%s vs %s)", d, FormatPerceptualHash(base), FormatPerceptualHash(scaled))
	}
	// A different picture: far away.
	other, err := PerceptualHash(loadPNG(t, "thumbhash/pic1-100.png"))
	if err != nil {
		t.Fatal(err)
	}
	if d := bits.OnesCount64(base ^ other); d < 16 {
		t.Errorf("flower vs pic1: distance %d, want ≥ 16", d)
	}
	// A left/right split has its energy in the horizontal frequencies.
	h, err := PerceptualHash(halves(64, 64, 32, white, black))
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatPerceptualHash(h); len(got) != 16 {
		t.Errorf("FormatPerceptualHash = %q, want 16 digits", got)
	}
	if FormatPerceptualHash(0x1f) != "000000000000001f" {
		t.Errorf("FormatPerceptualHash(0x1f) = %q", FormatPerceptualHash(0x1f))
	}
}

func TestDownscale(t *testing.T) {
	t.Parallel()
	m := halves(400, 100, 200, red, clear0)
	got, err := Downscale(m, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 100 || got.Height != 25 {
		t.Fatalf("Downscale = %dx%d, want 100x25", got.Width, got.Height)
	}
	// Left half red and opaque, right half transparent; no dark fringe.
	if c := got.Pix[:4]; c[0] != 255 || c[3] != 255 {
		t.Errorf("left pixel = %v", c)
	}
	if c := got.Pix[99*4 : 100*4]; c[3] != 0 {
		t.Errorf("right pixel = %v", c)
	}
	same, err := Downscale(m, 400)
	if err != nil || same != m {
		t.Errorf("Downscale within bounds copied the image (%v)", err)
	}
	// Mixed pixel: alpha-weighted colour stays red.
	mixed := resample(halves(2, 1, 1, red, clear0), 1, 1)
	if diff := cmp.Diff([]byte{255, 0, 0, 128}, mixed.Pix); diff != "" {
		t.Errorf("mixed pixel (-want +got):\n%s", diff)
	}
}

func TestHashsum(t *testing.T) {
	t.Parallel()
	for alg, want := range map[string]string{
		"md5":    "900150983cd24fb0d6963f7d28e17f72",
		"sha1":   "a9993e364706816aba3e25717850c26c9cd0d89d",
		"sha256": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"sha512": "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
	} {
		if got, ok := Hashsum(alg, []byte("abc")); !ok || got != want {
			t.Errorf("Hashsum(%s) = %q %v, want %q", alg, got, ok, want)
		}
	}
	if _, ok := Hashsum("crc32", nil); ok {
		t.Error("Hashsum accepted crc32")
	}
}

func TestInvalidImage(t *testing.T) {
	t.Parallel()
	bad := &Image{Width: 2, Height: 2, Pix: make([]byte, 15)}
	if _, err := Average(bad, true); err == nil {
		t.Error("Average accepted a short Pix")
	}
	if _, err := Palette(bad, 2); err == nil {
		t.Error("Palette accepted a short Pix")
	}
	if _, err := Dominant(bad, false); err == nil {
		t.Error("Dominant accepted a short Pix")
	}
	if _, err := PerceptualHash(nil); err == nil {
		t.Error("PerceptualHash accepted nil")
	}
	if _, err := Downscale(bad, 1); err == nil {
		t.Error("Downscale accepted a short Pix")
	}
}
