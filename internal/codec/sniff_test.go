package codec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// fixtureFormats is the expected format of every file in testdata/fixtures.
func fixtureFormats() map[string]codec.Format {
	return map[string]codec.Format{
		"avif/photo.avif":                 codec.FormatAVIF,
		"bmp/rgb24.bmp":                   codec.FormatBMP,
		"bmp/rle8.bmp":                    codec.FormatBMP,
		"gif/animated.gif":                codec.FormatGIF,
		"gif/static.gif":                  codec.FormatGIF,
		"heic/photo.heic":                 codec.FormatHEIC,
		"ico/bmp-entry.ico":               codec.FormatICO,
		"ico/png-entry.ico":               codec.FormatICO,
		"jp2/photo.jp2":                   codec.FormatJP2,
		"jpeg/baseline.jpg":               codec.FormatJPEG,
		"jpeg/exif-orientation-6-icc.jpg": codec.FormatJPEG,
		"jpeg/progressive.jpg":            codec.FormatJPEG,
		"jxl/photo.jxl":                   codec.FormatJXL,
		"png/palette.png":                 codec.FormatPNG,
		"png/rgb16.png":                   codec.FormatPNG,
		"png/rgba8.png":                   codec.FormatPNG,
		"svg/logo.svg":                    codec.FormatSVG,
		"tiff/multipage.tif":              codec.FormatTIFF,
		"uhdr/photo-ultrahdr.jpg":         codec.FormatUHDR,
		"webp/animated.webp":              codec.FormatWebP,
		"webp/lossless.webp":              codec.FormatWebP,
		"webp/lossy.webp":                 codec.FormatWebP,
	}
}

func TestSniffFixtures(t *testing.T) {
	t.Parallel()

	want := fixtureFormats()
	// Every fixture on disk must be in the table, so a new one cannot be
	// added without a sniff expectation.
	root := filepath.Dir(golden.Fixture(t, "jpeg/baseline.jpg"))
	root = filepath.Dir(root)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if _, ok := want[filepath.ToSlash(rel)]; !ok {
			t.Errorf("fixture %s has no expected format", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, f := range want {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data := readFixture(t, name)
			got, ok := codec.Sniff(data)
			if got != f || !ok {
				t.Errorf("Sniff = %v, %v; want %v, true", got, ok, f)
			}
			// UltraHDR needs the markers up to the start of scan; everything
			// else is recognised from SniffLen bytes.
			if f != codec.FormatUHDR {
				if got, _ := codec.Sniff(data[:min(len(data), codec.SniffLen)]); got != f {
					t.Errorf("Sniff(first SniffLen bytes) = %v, want %v", got, f)
				}
			}
		})
	}
}

func TestSniffSynthetic(t *testing.T) {
	t.Parallel()

	tiffHead := func(order string) []byte { return []byte(order + "\x08\x00\x00\x00") }
	tests := []struct {
		name string
		head []byte
		want codec.Format
	}{
		{"empty", nil, codec.FormatUnknown},
		{"one byte", []byte{0xFF}, codec.FormatUnknown},
		{"text", []byte("hello, world"), codec.FormatUnknown},
		{"html", []byte("<!doctype html><html><body></body></html>"), codec.FormatUnknown},
		{"jpeg soi only", []byte{0xFF, 0xD8, 0xFF}, codec.FormatJPEG},
		{"jpeg mpf without gain map", jpegWith(app(2, "MPF\x00MM\x00*")), codec.FormatJPEG},
		{"jpeg iso gain map without mpf", jpegWith(app(2, "urn:iso:std:iso:ts:21496:-1\x00\x00\x00")), codec.FormatJPEG},
		{"uhdr iso", jpegWith(app(2, "urn:iso:std:iso:ts:21496:-1\x00\x00\x00"), app(2, "MPF\x00MM\x00*")), codec.FormatUHDR},
		{"uhdr xmp", jpegWith(app(1, `http://ns.adobe.com/xap/1.0/`+"\x00"+`<x:xmpmeta hdrgm:Version="1.0"/>`), app(2, "MPF\x00II*\x00")), codec.FormatUHDR},
		{"uhdr markers after sos ignored", append(jpegWith(), append(app(2, "MPF\x00"), app(2, "urn:iso:std:iso:ts:21496:-1\x00")...)...), codec.FormatJPEG},
		{"jpeg bad segment length", []byte{0xFF, 0xD8, 0xFF, 0xE2, 0x00, 0x01, 'M', 'P', 'F', 0}, codec.FormatJPEG},
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"), codec.FormatPNG},
		{"png truncated signature", []byte("\x89PNG\r\n"), codec.FormatUnknown},
		{"gif87a", []byte("GIF87a\x01\x00\x01\x00"), codec.FormatGIF},
		{"gif89a", []byte("GIF89a"), codec.FormatGIF},
		{"webp", []byte("RIFF\x24\x00\x00\x00WEBPVP8 "), codec.FormatWebP},
		{"riff wave", []byte("RIFF\x24\x00\x00\x00WAVEfmt "), codec.FormatUnknown},
		{"jxl codestream", []byte{0xFF, 0x0A, 0xFA, 0x1F}, codec.FormatJXL},
		{"jxl container", []byte("\x00\x00\x00\x0cJXL \r\n\x87\n"), codec.FormatJXL},
		{"jp2 container", []byte("\x00\x00\x00\x0cjP  \r\n\x87\n"), codec.FormatJP2},
		{"j2k codestream", []byte{0xFF, 0x4F, 0xFF, 0x51, 0x00, 0x2F}, codec.FormatJP2},
		{"tiff le", tiffHead("II*\x00"), codec.FormatTIFF},
		{"tiff be", tiffHead("MM\x00*"), codec.FormatTIFF},
		{"bigtiff le", tiffHead("II+\x00"), codec.FormatTIFF},
		{"bigtiff be", tiffHead("MM\x00+"), codec.FormatTIFF},
		{"avif", isobmff("avif", "mif1", "miaf"), codec.FormatAVIF},
		{"avif sequence", isobmff("avis", "msf1", "avif"), codec.FormatAVIF},
		{"avif by compatible brand", isobmff("mif1", "miaf", "avif"), codec.FormatAVIF},
		{"heic", isobmff("heic", "mif1", "heic"), codec.FormatHEIC},
		{"heix", isobmff("heix", "mif1"), codec.FormatHEIC},
		{"heic sequence", isobmff("hevc", "msf1"), codec.FormatHEIC},
		{"heic by compatible brand", isobmff("mif1", "heic"), codec.FormatHEIC},
		{"generic heif", isobmff("mif1", "miaf"), codec.FormatHEIF},
		{"generic heif sequence", isobmff("msf1", "iso8"), codec.FormatHEIF},
		{"brands past the box ignored", append(isobmff("mif1"), "avif"...), codec.FormatHEIF},
		{"mp4", isobmff("isom", "iso2", "mp41"), codec.FormatUnknown},
		{"mov", isobmff("qt  "), codec.FormatUnknown},
		{"cr3", isobmff("crx ", "isom"), codec.FormatRAW},
		{"ftyp truncated", []byte("\x00\x00\x00\x18ftypav"), codec.FormatUnknown},
		{"bmp", []byte("BM\x46\x00\x00\x00\x00\x00\x00\x00\x36\x00\x00\x00\x28\x00\x00\x00"), codec.FormatBMP},
		{"bm text", []byte("BMW is a car, not a bitmap"), codec.FormatUnknown},
		{"ico", []byte("\x00\x00\x01\x00\x01\x00\x10\x10\x00\x00\x01\x00\x20\x00\x68\x04\x00\x00\x16\x00\x00\x00"), codec.FormatICO},
		{"cur", []byte("\x00\x00\x02\x00\x01\x00\x10\x10\x00\x00\x01\x00\x20\x00\x68\x04\x00\x00\x16\x00\x00\x00"), codec.FormatICO},
		{"svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`), codec.FormatSVG},
		{"svg bom", []byte("\xEF\xBB\xBF<svg/>"), codec.FormatSVG},
		{"xml not svg", []byte(`<?xml version="1.0"?><rss/>`), codec.FormatUnknown},
		{"pdf", []byte("%PDF-1.7\n"), codec.FormatPDF},
		{"psd", []byte("8BPS\x00\x01"), codec.FormatPSD},
		{"psb", []byte("8BPS\x00\x02"), codec.FormatPSD},
		{"psd bad version", []byte("8BPS\x00\x03"), codec.FormatUnknown},
		{"cr2", []byte("II*\x00\x10\x00\x00\x00CR\x02\x00"), codec.FormatRAW},
		{"crw", []byte("II\x1a\x00\x00\x00HEAPCCDR"), codec.FormatRAW},
		{"raf", []byte("FUJIFILMCCD-RAW 0201"), codec.FormatRAW},
		{"orf", []byte("IIRO\x08\x00\x00\x00"), codec.FormatRAW},
		{"rw2", []byte("IIU\x00\x18\x00\x00\x00"), codec.FormatRAW},
		{"x3f", []byte("FOVb\x00\x00\x02\x00"), codec.FormatRAW},
		{"mrw", []byte("\x00MRM\x00\x00\x00\x00"), codec.FormatRAW},
		{"gzip", []byte("\x1f\x8b\x08\x00"), codec.FormatUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := codec.Sniff(tc.head)
			if got != tc.want || ok != (tc.want != codec.FormatUnknown) {
				t.Errorf("Sniff(%q) = %v, %v; want %v", tc.head, got, ok, tc.want)
			}
		})
	}
}

func TestSniffSVGBeyondSniffLen(t *testing.T) {
	t.Parallel()
	// A root element that starts after SniffLen bytes of comments is not
	// recognised: detection is bounded.
	head := "<!--" + strings.Repeat("x", codec.SniffLen) + "--><svg/>"
	if got, ok := codec.Sniff([]byte(head)); ok {
		t.Errorf("Sniff = %v, want unknown", got)
	}
}

// FuzzSniff checks that Sniff never panics, is consistent with its ok
// result, and only looks at a bounded prefix for every format except
// UltraHDR (whose markers may lie anywhere before the start of scan).
func FuzzSniff(f *testing.F) {
	for name := range fixtureFormats() {
		data := readFixture(f, name)
		f.Add(data[:min(len(data), 2*codec.SniffLen)])
	}
	f.Add(jpegWith(app(2, "urn:iso:std:iso:ts:21496:-1\x00"), app(2, "MPF\x00")))
	f.Add(isobmff("mif1", "avif"))
	f.Add([]byte("\x00\x00\x00\x00ftyp"))
	f.Fuzz(func(t *testing.T, b []byte) {
		got, ok := codec.Sniff(b)
		if ok != (got != codec.FormatUnknown) {
			t.Fatalf("Sniff = %v, %v", got, ok)
		}
		if got == codec.FormatUHDR || got == codec.FormatJPEG {
			return
		}
		longer := append(append([]byte(nil), b...), make([]byte, 64)...)
		if len(b) >= codec.SniffLen {
			if again, _ := codec.Sniff(longer); again != got {
				t.Fatalf("Sniff changed from %v to %v with trailing bytes past SniffLen", got, again)
			}
		}
	})
}

func BenchmarkSniff(b *testing.B) {
	for _, name := range []string{"jpeg/baseline.jpg", "uhdr/photo-ultrahdr.jpg", "png/rgba8.png", "heic/photo.heic", "ico/png-entry.ico", "svg/logo.svg"} {
		data := readFixture(b, name)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := codec.Sniff(data); !ok {
					b.Fatal("not recognised")
				}
			}
		})
	}
	b.Run("unknown", func(b *testing.B) {
		data := make([]byte, codec.SniffLen)
		b.ReportAllocs()
		for b.Loop() {
			codec.Sniff(data)
		}
	})
}
