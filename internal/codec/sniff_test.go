package codec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/container"
	"github.com/xantiagoma/xtandard-image/internal/testutil/golden"
)

// fixtureFormats is the expected format of every file in testdata/fixtures.
func fixtureFormats() map[string]codec.Format {
	return map[string]codec.Format{
		"avif/photo.avif":                 codec.FormatAVIF,
		"avif/thumbnail.avif":             codec.FormatAVIF,
		"exr/rgba-half.exr":               codec.FormatEXR,
		"exr/rgb-float.exr":               codec.FormatEXR,
		"hdr/scene.hdr":                   codec.FormatRadiance,
		"hdr/rgbe.hdr":                    codec.FormatRadiance,
		"pfm/scene.pfm":                   codec.FormatPFM,
		"ppm/photo.ppm":                   codec.FormatPPM,
		"ppm/photo-ascii.ppm":             codec.FormatPPM,
		"ppm/photo-ascii.pgm":             codec.FormatPPM,
		"ppm/graphic.pbm":                 codec.FormatPPM,
		"ppm/graphic-ascii.pbm":           codec.FormatPPM,
		"jxr/photo.jxr":                   codec.FormatJXR,
		"jxr/alpha.jxr":                   codec.FormatJXR,
		"jxr/rgb16.jxr":                   codec.FormatJXR,
		"jxr/gray.jxr":                    codec.FormatJXR,
		"jxr/hdr.jxr":                     codec.FormatJXR,
		"tga/alpha.tga":                   codec.FormatTGA,
		"tga/photo-rle.tga":               codec.FormatTGA,
		"pcx/photo.pcx":                   codec.FormatPCX,
		"dds/photo-dxt1.dds":              codec.FormatDDS,
		"dds/alpha-dxt5.dds":              codec.FormatDDS,
		"dds/bc1-dx10.dds":                codec.FormatDDS,
		"dds/bc4.dds":                     codec.FormatDDS,
		"dds/bc6h-scene.dds":              codec.FormatDDS,
		"dds/bc7-alpha.dds":               codec.FormatDDS,
		"jxl/cmyk.jxl":                    codec.FormatJXL,
		"jxl/cmyk-alpha.jxl":              codec.FormatJXL,
		"tiff/lab8.tif":                   codec.FormatTIFF,
		"tiff/lab8-alpha.tif":             codec.FormatTIFF,
		"tiff/lab8-icc.tif":               codec.FormatTIFF,
		"tiff/lab16.tif":                  codec.FormatTIFF,
		"qoi/alpha.qoi":                   codec.FormatQOI,
		"sgi/photo.sgi":                   codec.FormatSGI,
		"sgi/alpha.sgi":                   codec.FormatSGI,
		"sun/photo.ras":                   codec.FormatSunRaster,
		"xbm/graphic.xbm":                 codec.FormatXBM,
		"xpm/graphic.xpm":                 codec.FormatXPM,
		"pam/alpha.pam":                   codec.FormatPAM,
		"wbmp/graphic.wbmp":               codec.FormatWBMP,
		"icns/icon.icns":                  codec.FormatICNS,
		"eps/simple.eps":                  codec.FormatEPS,
		"eps/pages.ps":                    codec.FormatEPS,
		"eps/illustrator.ai":              codec.FormatEPS,
		"eps/preview.eps":                 codec.FormatEPS,
		"bmp/rgb24.bmp":                   codec.FormatBMP,
		"bmp/rle8.bmp":                    codec.FormatBMP,
		"gif/animated.gif":                codec.FormatGIF,
		"detect/apollo11-crew.jpg":        codec.FormatJPEG,
		"detect/astronaut-dogs.jpg":       codec.FormatJPEG,
		"detect/cat-laptop.jpg":           codec.FormatJPEG,
		"classify/bald-eagle.jpg":         codec.FormatJPEG,
		"classify/banana.jpg":             codec.FormatJPEG,
		"classify/golden-retriever.jpg":   codec.FormatJPEG,
		"classify/monarch.jpg":            codec.FormatJPEG,
		"classify/pizza.jpg":              codec.FormatJPEG,
		"classify/red-fox.jpg":            codec.FormatJPEG,
		"classify/space-shuttle.jpg":      codec.FormatJPEG,
		"dng/landscape.dng":               codec.FormatRAW,
		"dng/portrait-orientation-6.dng":  codec.FormatRAW,
		"dng/jxl-undecodable.dng":         codec.FormatRAW,
		"dng/jxl-landscape.dng":           codec.FormatRAW,
		"gpr/hero11-crop.gpr":             codec.FormatRAW,
		"dng/deflate-corrupt.dng":         codec.FormatRAW,
		"gif/static.gif":                  codec.FormatGIF,
		"heic/photo.heic":                 codec.FormatHEIC,
		"heic/thumbnail.heic":             codec.FormatHEIC,
		"ico/bmp-entry.ico":               codec.FormatICO,
		"ico/png-entry.ico":               codec.FormatICO,
		"jp2/photo.jp2":                   codec.FormatJP2,
		"jpeg/baseline.jpg":               codec.FormatJPEG,
		"jpeg/exif-orientation-6-icc.jpg": codec.FormatJPEG,
		"jpeg/metadata-p3.jpg":            codec.FormatJPEG,
		"jpeg/progressive.jpg":            codec.FormatJPEG,
		// High-precision JPEG (WP-106): 12-bit DCT and lossless.
		"jpeg/rgb12.jpg":              codec.FormatJPEG,
		"jpeg/rgb12-exif6-icc.jpg":    codec.FormatJPEG,
		"jpeg/grey12-progressive.jpg": codec.FormatJPEG,
		"jpeg/cmyk12.jpg":             codec.FormatJPEG,
		"jpeg/lossless16.jpg":         codec.FormatJPEG,
		"jpeg/lossless16-cmyk.jpg":    codec.FormatJPEG,
		"jpeg/lossless12-ycc.jpg":     codec.FormatJPEG,
		"jpeg/lossless10-grey.jpg":    codec.FormatJPEG,
		"jpeg/lossless8.jpg":          codec.FormatJPEG,
		"jpeg/lossless6.jpg":          codec.FormatJPEG,
		"jxl/photo.jxl":               codec.FormatJXL,
		"png/bordered.png":            codec.FormatPNG,
		"png/palette.png":             codec.FormatPNG,
		"png/rgb16.png":               codec.FormatPNG,
		"png/rgba8.png":               codec.FormatPNG,
		"pdf/pages.pdf":               codec.FormatPDF,
		"pdf/password.pdf":            codec.FormatPDF,
		"pdf/password-aes128.pdf":     codec.FormatPDF,
		"psd/layered.psd":             codec.FormatPSD,
		"psd/psb-layered.psb":         codec.FormatPSD,
		"psd/no-composite.psd":        codec.FormatPSD,
		"svg/logo.svg":                codec.FormatSVG,
		"tiff/multipage.tif":          codec.FormatTIFF,
		"uhdr/photo-ultrahdr.jpg":     codec.FormatUHDR,
		// WP-104: image sequences and gain maps.
		"avif/sequence.avif":                                          codec.FormatAVIF,
		"avif/sequence-alpha.avif":                                    codec.FormatAVIF,
		"avif/libavif-colors-animated-8bpc.avif":                      codec.FormatAVIF,
		"heic/sequence.heic":                                          codec.FormatHEIC,
		"gainmap/libavif-color_grid_gainmap_different_grid.avif":      codec.FormatAVIF,
		"gainmap/libavif-color_nogrid_alpha_nogrid_gainmap_grid.avif": codec.FormatAVIF,
		"gainmap/libavif-unsupported_gainmap_version.avif":            codec.FormatAVIF,
		"gainmap/libavif-seine_sdr_gainmap_srgb.avif":                 codec.FormatAVIF,
		// MPF and hdrgm XMP look like UltraHDR, but libultrahdr refuses it
		// (no ISO metadata or GContainer): jpegload decodes it.
		"gainmap/libavif-paris_exif_xmp_gainmap_bigendian.jpg": codec.FormatUHDR,
		"watermark/logo.png":           codec.FormatPNG,
		"watermark/badge.png":          codec.FormatPNG,
		"webp/animated.webp":           codec.FormatWebP,
		"webp/lossless.webp":           codec.FormatWebP,
		"webp/lossy.webp":              codec.FormatWebP,
		"video/bbb-h264-aac.mp4":       codec.FormatMP4,
		"video/clip-av1.mp4":           codec.FormatMP4,
		"video/clip-av1-rotate90.mp4":  codec.FormatMP4,
		"video/faces-av1-rotate90.mp4": codec.FormatMP4,
		"video/faces-vp9.webm":         codec.FormatWebM,
		"video/clip-png.mov":           codec.FormatMOV,
		"video/clip-raw.avi":           codec.FormatAVI,
		"video/clip-vp8-matroska.mkv":  codec.FormatMKV,
		"video/clip-vp8.webm":          codec.FormatWebM,
		"video/clip-vp9.webm":          codec.FormatWebM,
		"xcf/layers.xcf":               codec.FormatXCF,
		"xcf/gray.xcf":                 codec.FormatXCF,
		"xcf/alpha.xcf":                codec.FormatXCF,
		// WP-108: scientific and legacy long tail.
		"ktx2/bc1.ktx2":              codec.FormatKTX2,
		"ktx2/bc4.ktx2":              codec.FormatKTX2,
		"ktx2/bc6h.ktx2":             codec.FormatKTX2,
		"ktx2/bc7.ktx2":              codec.FormatKTX2,
		"ktx2/etc1s.ktx2":            codec.FormatKTX2,
		"ktx2/rgb8.ktx2":             codec.FormatKTX2,
		"ktx2/rgba8-zlib.ktx2":       codec.FormatKTX2,
		"ktx2/rgba8-zstd.ktx2":       codec.FormatKTX2,
		"ktx2/rgba8.ktx2":            codec.FormatKTX2,
		"ktx2/uastc-hdr.ktx2":        codec.FormatKTX2,
		"ktx2/uastc-zstd.ktx2":       codec.FormatKTX2,
		"fits/dither2-f32.fits.fz":   codec.FormatFITS,
		"fits/dither2-f32.ref.fits":  codec.FormatFITS,
		"fits/f32-nan.fits":          codec.FormatFITS,
		"fits/f64.fits":              codec.FormatFITS,
		"fits/gzip1-mef-i32.fits.fz": codec.FormatFITS,
		"fits/gzip2-f32.fits.fz":     codec.FormatFITS,
		"fits/gzip2-f32.ref.fits":    codec.FormatFITS,
		"fits/hcompress-u16.fits.fz": codec.FormatFITS,
		"fits/mef-i32.fits":          codec.FormatFITS,
		"fits/rgb16.fits":            codec.FormatFITS,
		"fits/rice-f32.fits.fz":      codec.FormatFITS,
		"fits/rice-f32.ref.fits":     codec.FormatFITS,
		"fits/rice-u16.fits.fz":      codec.FormatFITS,
		"fits/u16.fits":              codec.FormatFITS,
		"fits/u8.fits":               codec.FormatFITS,
		"dicom/ct-bigendian.dcm":     codec.FormatDICOM,
		"dicom/ct-j2k.dcm":           codec.FormatDICOM,
		"dicom/ct-rle.dcm":           codec.FormatDICOM,
		"dicom/ct-window.dcm":        codec.FormatDICOM,
		"dicom/grey-2frames.dcm":     codec.FormatDICOM,
		"dicom/jpeg-2frames-bot.dcm": codec.FormatDICOM,
		"dicom/jpeg-baseline.dcm":    codec.FormatDICOM,
		"dicom/mono1-implicit.dcm":   codec.FormatDICOM,
		"dicom/palette.dcm":          codec.FormatDICOM,
		"dicom/rgb-deflated.dcm":     codec.FormatDICOM,
		"dicom/rgb-planar.dcm":       codec.FormatDICOM,
		"dicom/rgb-rle.dcm":          codec.FormatDICOM,
		"pict/photo.pct":             codec.FormatPICT,
		// Designer containers are no codec format: the server unwraps them
		// (codec/container) before anything sniffs the source; nor are MNG
		// and JNG (WP-108).
		"ora/layers.ora":                codec.FormatUnknown,
		"kra/layers.kra":                codec.FormatUnknown,
		"procreate/thumbnail.procreate": codec.FormatUnknown,
		"sketch/preview.sketch":         codec.FormatUnknown,
		"svgz/logo.svgz":                codec.FormatUnknown,
		"pdn/thumbnail.pdn":             codec.FormatUnknown,
		"mng/frames.mng":                codec.FormatUnknown,
		"mng/jng.mng":                   codec.FormatUnknown,
		"mng/palette.mng":               codec.FormatUnknown,
		"mng/png.mng":                   codec.FormatUnknown,
		"jng/jng-idat.jng":              codec.FormatUnknown,
		"jng/jng-jdaa.jng":              codec.FormatUnknown,
		"jng/jng.jng":                   codec.FormatUnknown,
	}
}

// containerFixtures is the number of fixtures fixtureFormats lists as
// FormatUnknown.
const containerFixtures = 13

func TestSniffFixtures(t *testing.T) {
	t.Parallel()

	want := fixtureFormats()
	// Every fixture on disk must be in the table, so a new one cannot be
	// added without a sniff expectation.
	root := filepath.Dir(golden.Fixture(t, "jpeg/baseline.jpg"))
	root = filepath.Dir(root)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) == ".md" {
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
			if f == codec.FormatUnknown {
				if _, isContainer := container.Detect(data); !isContainer || ok {
					t.Errorf("container: Detect %v, Sniff = %v, %v", isContainer, got, ok)
				}
				return
			}
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
		{"mp4", isobmff("isom", "iso2", "mp41"), codec.FormatMP4},
		{"mp4 av1", isobmff("isom", "av01", "iso2", "mp41"), codec.FormatMP4},
		{"3gp", isobmff("3gp5", "3gp5", "isom"), codec.FormatMP4},
		{"mov", isobmff("qt  "), codec.FormatMOV},
		{"mov without ftyp", []byte("\x00\x00\x00\x08wide\x00\x00\xdf\xa4mdat"), codec.FormatMOV},
		{"mov moov first", []byte("\x00\x00\x01\x00moov\x00\x00\x00\x6cmvhd"), codec.FormatMOV},
		{"mov bad atom size", []byte("\x00\x00\x00\x04moov"), codec.FormatUnknown},
		{"webm", []byte("\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01\x42\xf7\x81\x01\x42\xf2\x81\x04\x42\xf3\x81\x08\x42\x82\x84webm\x42\x87\x81\x04"), codec.FormatWebM},
		{"mkv", []byte("\x1a\x45\xdf\xa3\xa3\x42\x86\x81\x01\x42\xf7\x81\x01\x42\xf2\x81\x04\x42\xf3\x81\x08\x42\x82\x88matroska"), codec.FormatMKV},
		{"ebml without doctype", []byte("\x1a\x45\xdf\xa3\x80"), codec.FormatMKV},
		{"avi", []byte("RIFF\x24\x00\x00\x00AVI LIST"), codec.FormatAVI},
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
		{"exr", []byte("v/1\x01\x02\x00\x00\x00"), codec.FormatEXR},
		{"radiance", []byte("#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n"), codec.FormatRadiance},
		{"rgbe", []byte("#?RGBE\r\n"), codec.FormatRadiance},
		{"radiance prefix only", []byte("#?RADIANCEX\n"), codec.FormatUnknown},
		{"shell script", []byte("#!/bin/sh\n"), codec.FormatUnknown},
		{"pgm", []byte("P5\n2 2\n255\n"), codec.FormatPPM},
		{"ppm", []byte("P6 2 2 65535\n"), codec.FormatPPM},
		{"pfm", []byte("PF\n2 2\n-1.0\n"), codec.FormatPFM},
		{"pfm grey", []byte("Pf\n2 2\n1.0\n"), codec.FormatPFM},
		{"ascii ppm", []byte("P3\n2 2\n255\n"), codec.FormatPPM},
		{"ascii pbm", []byte("P1\n2 2\n0 1 1 0\n"), codec.FormatPPM},
		{"pbm", []byte("P4\n8 1\n"), codec.FormatPPM},
		{"pam", []byte("P7\nWIDTH 1\n"), codec.FormatPAM},
		{"p8", []byte("P8\n"), codec.FormatUnknown},
		{"jxr", []byte("II\xbc\x01\x08\x00\x00\x00"), codec.FormatJXR},
		{"hd photo", []byte("II\xbc\x00\x08\x00\x00\x00"), codec.FormatJXR},
		{"jxr bad version", []byte("II\xbc\x02\x08\x00\x00\x00"), codec.FormatUnknown},
		{"dds", append([]byte("DDS \x7c\x00\x00\x00"), make([]byte, 120)...), codec.FormatDDS},
		{"dds bad header size", append([]byte("DDS \x7d\x00\x00\x00"), make([]byte, 120)...), codec.FormatUnknown},
		{"qoi", []byte("qoif\x00\x00\x00\x01\x00\x00\x00\x01\x04\x00"), codec.FormatQOI},
		{"sun", append([]byte("\x59\xa6\x6a\x95"), make([]byte, 28)...), codec.FormatSunRaster},
		{"sgi", append([]byte("\x01\xda\x01\x01\x00\x03"), make([]byte, 506)...), codec.FormatSGI},
		{"sgi bad dimension", append([]byte("\x01\xda\x01\x01\x00\x04"), make([]byte, 506)...), codec.FormatUnknown},
		{"pcx", append([]byte("\x0a\x05\x01\x08"), make([]byte, 124)...), codec.FormatPCX},
		{"pcx version 3", append([]byte("\x0a\x03\x01\x08"), make([]byte, 124)...), codec.FormatUnknown},
		{"xbm", []byte("#define logo_width 16\n#define logo_height 16\n"), codec.FormatXBM},
		{"c header", []byte("#define VERSION 3\n"), codec.FormatUnknown},
		{"xpm", []byte("/* XPM */\nstatic char *x[] = {\n"), codec.FormatXPM},
		{"icns", []byte("icns\x00\x00\x00\x10is32\x00\x00\x00\x08"), codec.FormatICNS},
		{"eps", []byte("%!PS-Adobe-3.0 EPSF-3.0\n"), codec.FormatEPS},
		{"postscript after ctrl-d", []byte("\x04%!PS-Adobe-2.0\n"), codec.FormatEPS},
		{"bare postscript", []byte("%!\n/Times-Roman findfont\n"), codec.FormatEPS},
		{"dos eps", append([]byte("\xc5\xd0\xd3\xc6"), make([]byte, 26)...), codec.FormatEPS},
		{"dos eps short", []byte("\xc5\xd0\xd3\xc6\x1e\x00"), codec.FormatUnknown},
		{"percent bang text", []byte("%!foo"), codec.FormatUnknown},
		{"tga", tga(2, 0, 24, 0), codec.FormatTGA},
		{"tga grey rle", tga(11, 0, 8, 0), codec.FormatTGA},
		{"tga colour-mapped", tgaMapped(), codec.FormatTGA},
		{"tga with interleave bits", tga(2, 0, 24, 0x40), codec.FormatUnknown},
		{"tga bad depth", tga(2, 0, 12, 0), codec.FormatUnknown},
		{"tga zero width", append(tga(2, 0, 24, 0)[:12], make([]byte, 6)...), codec.FormatUnknown},
		{"wbmp", []byte{0, 0, 10, 2, 0xAA, 0x80, 0xFF, 0xC0}, codec.FormatWBMP},
		{"wbmp with a trailing byte", []byte{0, 0, 10, 2, 0xAA, 0x80, 0xFF, 0xC0, 0}, codec.FormatUnknown},
		{"p6 text", []byte("P6x"), codec.FormatUnknown},
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

// tga returns an 18-byte TGA header of a 4×2 image of the given type,
// pixel depth and descriptor, and one byte of pixels.
func tga(imageType, cmap, depth, desc byte) []byte {
	return []byte{0, cmap, imageType, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 0, 2, 0, depth, desc, 0}
}

// tgaMapped is a colour-mapped TGA header: 16 entries of 24 bits, 8-bit
// indices.
func tgaMapped() []byte {
	return []byte{0, 1, 1, 0, 0, 16, 0, 24, 0, 0, 0, 0, 4, 0, 2, 0, 8, 0, 0}
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
		// UltraHDR markers may lie anywhere before the start of scan; a
		// WBMP is only one when its length matches its header.
		if got == codec.FormatUHDR || got == codec.FormatJPEG || got == codec.FormatWBMP {
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
