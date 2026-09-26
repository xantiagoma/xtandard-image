package codec_test

import (
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

func allFormats() []codec.Format {
	return []codec.Format{
		codec.FormatUnknown, codec.FormatJPEG, codec.FormatPNG, codec.FormatGIF, codec.FormatWebP,
		codec.FormatAVIF, codec.FormatHEIC, codec.FormatHEIF, codec.FormatJXL, codec.FormatTIFF,
		codec.FormatBMP, codec.FormatICO, codec.FormatSVG, codec.FormatJP2, codec.FormatUHDR,
		codec.FormatPDF, codec.FormatPSD, codec.FormatRAW,
	}
}

func TestFormatFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		f           codec.Format
		name, ext   string
		mime        string
		anim, alpha bool
		supported   bool
		out         transform.Format
	}{
		{codec.FormatUnknown, "unknown", "", "application/octet-stream", false, false, false, transform.FormatUnset},
		{codec.FormatJPEG, "jpeg", "jpg", "image/jpeg", false, false, true, transform.FormatJPEG},
		{codec.FormatPNG, "png", "png", "image/png", false, true, true, transform.FormatPNG},
		{codec.FormatGIF, "gif", "gif", "image/gif", true, true, true, transform.FormatGIF},
		{codec.FormatWebP, "webp", "webp", "image/webp", true, true, true, transform.FormatWebP},
		{codec.FormatAVIF, "avif", "avif", "image/avif", false, true, true, transform.FormatAVIF},
		{codec.FormatHEIC, "heic", "heic", "image/heic", false, true, true, transform.FormatHEIC},
		{codec.FormatHEIF, "heif", "heif", "image/heif", false, true, true, transform.FormatHEIC},
		{codec.FormatJXL, "jxl", "jxl", "image/jxl", true, true, true, transform.FormatJXL},
		{codec.FormatTIFF, "tiff", "tiff", "image/tiff", false, true, true, transform.FormatTIFF},
		{codec.FormatBMP, "bmp", "bmp", "image/bmp", false, true, true, transform.FormatBMP},
		{codec.FormatICO, "ico", "ico", "image/x-icon", false, true, true, transform.FormatICO},
		{codec.FormatSVG, "svg", "svg", "image/svg+xml", false, true, true, transform.FormatSVG},
		{codec.FormatJP2, "jp2", "jp2", "image/jp2", false, true, true, transform.FormatUnset},
		{codec.FormatUHDR, "uhdr", "jpg", "image/jpeg", false, false, true, transform.FormatJPEG},
		{codec.FormatPDF, "pdf", "pdf", "application/pdf", false, true, true, transform.FormatUnset},
		{codec.FormatPSD, "psd", "psd", "image/vnd.adobe.photoshop", false, true, true, transform.FormatUnset},
		{codec.FormatRAW, "raw", "", "application/octet-stream", false, false, false, transform.FormatUnset},
	}
	if len(tests) != len(allFormats()) {
		t.Fatalf("table covers %d formats, want %d", len(tests), len(allFormats()))
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := tc.f
			if f.String() != tc.name || f.Ext() != tc.ext || f.MIME() != tc.mime {
				t.Errorf("String/Ext/MIME = %q/%q/%q, want %q/%q/%q", f, f.Ext(), f.MIME(), tc.name, tc.ext, tc.mime)
			}
			if f.SupportsAnimation() != tc.anim || f.SupportsAlpha() != tc.alpha || f.Supported() != tc.supported {
				t.Errorf("anim/alpha/supported = %v/%v/%v, want %v/%v/%v",
					f.SupportsAnimation(), f.SupportsAlpha(), f.Supported(), tc.anim, tc.alpha, tc.supported)
			}
			out, ok := f.Output()
			if out != tc.out || ok != (tc.out != transform.FormatUnset) {
				t.Errorf("Output = %v, %v; want %v", out, ok, tc.out)
			}
			if f == codec.FormatUnknown {
				return
			}
			got, err := codec.ParseFormat(tc.name)
			if err != nil || got != f {
				t.Errorf("ParseFormat(%q) = %v, %v", tc.name, got, err)
			}
			text, err := f.MarshalText()
			if err != nil || string(text) != tc.name {
				t.Errorf("MarshalText = %q, %v", text, err)
			}
		})
	}
}

func TestParseFormatAliases(t *testing.T) {
	t.Parallel()

	tests := map[string]codec.Format{
		"JPG": codec.FormatJPEG, "jpe": codec.FormatJPEG, "tif": codec.FormatTIFF, "cur": codec.FormatICO,
		"j2k": codec.FormatJP2, "jpx": codec.FormatJP2, "psb": codec.FormatPSD, "hif": codec.FormatHEIF,
		"apng": codec.FormatPNG, "dib": codec.FormatBMP, "ultrahdr": codec.FormatUHDR,
	}
	for in, want := range tests {
		if got, err := codec.ParseFormat(in); err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "unknown", "mp4", "exe"} {
		if _, err := codec.ParseFormat(bad); errs.KindOf(err) != errs.KindUnsupported {
			t.Errorf("ParseFormat(%q) error = %v, want Unsupported", bad, err)
		}
	}
	if _, err := codec.Format(200).MarshalText(); err == nil {
		t.Error("MarshalText of an invalid format succeeded")
	}
}

func TestMatchesContentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		f    codec.Format
		ct   string
		want bool
	}{
		{codec.FormatJPEG, "", true},
		{codec.FormatJPEG, "image/jpeg", true},
		{codec.FormatJPEG, "IMAGE/JPEG; charset=binary", true},
		{codec.FormatJPEG, "image/jpg", true},
		{codec.FormatJPEG, "image/png", false},
		{codec.FormatJPEG, "application/octet-stream", true},
		{codec.FormatJPEG, "binary/octet-stream", true},
		{codec.FormatJPEG, ";;;", true},
		{codec.FormatJPEG, "text/html", false},
		{codec.FormatUHDR, "image/jpeg", true},
		{codec.FormatPNG, "image/x-png", true},
		{codec.FormatHEIC, "image/heif", true},
		{codec.FormatHEIF, "image/heic-sequence", true},
		{codec.FormatAVIF, "image/heic", false},
		{codec.FormatBMP, "image/x-ms-bmp", true},
		{codec.FormatICO, "image/vnd.microsoft.icon", true},
		{codec.FormatSVG, "image/svg+xml", true},
		{codec.FormatSVG, "text/xml", false},
		{codec.FormatJP2, "image/jpx", true},
		{codec.FormatRAW, "image/x-canon-cr2", true},
		{codec.FormatGIF, "image/webp", false},
	}
	for _, tc := range tests {
		if got := tc.f.MatchesContentType(tc.ct); got != tc.want {
			t.Errorf("%v.MatchesContentType(%q) = %v, want %v", tc.f, tc.ct, got, tc.want)
		}
	}
}

func TestLimitsCheck(t *testing.T) {
	t.Parallel()

	still := func(w, h int) codec.ImageMeta {
		return codec.ImageMeta{Format: codec.FormatPNG, Width: w, Height: h, Frames: 1, Pages: 1}
	}
	anim := func(w, h, frames int) codec.ImageMeta {
		return codec.ImageMeta{Format: codec.FormatGIF, Width: w, Height: h, Frames: frames, Pages: frames, IsAnimated: true}
	}
	tests := []struct {
		name string
		lim  codec.Limits
		meta codec.ImageMeta
		want errs.Kind
	}{
		{"small", codec.Limits{}, still(100, 100), errs.KindUnknown},
		{"at the default limit", codec.Limits{}, still(10_000, 5_000), errs.KindUnknown},
		{"over the default limit", codec.Limits{}, still(10_000, 5_001), errs.KindLimitExceeded},
		{"100k squared", codec.Limits{}, still(100_000, 100_000), errs.KindLimitExceeded},
		{"custom pixel limit", codec.Limits{MaxSrcPixels: 100}, still(11, 10), errs.KindLimitExceeded},
		{"zero width", codec.Limits{}, still(0, 10), errs.KindCorrupt},
		{"frames at limit", codec.Limits{}, anim(10, 10, 256), errs.KindUnknown},
		{"too many frames", codec.Limits{}, anim(1, 1, 257), errs.KindLimitExceeded},
		{"custom frame limit", codec.Limits{MaxAnimationFrames: 2}, anim(1, 1, 3), errs.KindLimitExceeded},
		{"frame too large", codec.Limits{}, anim(5_000, 4_000, 2), errs.KindLimitExceeded},
		{"frames times pixels over total", codec.Limits{}, anim(4_000, 4_000, 4), errs.KindLimitExceeded},
		{"multi-page still counts one page", codec.Limits{}, codec.ImageMeta{Width: 7_000, Height: 7_000, Frames: 1, Pages: 50}, errs.KindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.lim.Check(&tc.meta)
			if got := errs.KindOf(err); got != tc.want {
				t.Errorf("Check = %v (kind %v), want kind %v", err, got, tc.want)
			}
		})
	}
}

func TestDefaultLimits(t *testing.T) {
	t.Parallel()
	d := codec.DefaultLimits()
	if d.MaxSrcPixels != codec.DefaultMaxPixels {
		t.Errorf("MaxSrcPixels = %d, want DefaultMaxPixels %d", d.MaxSrcPixels, codec.DefaultMaxPixels)
	}
	if d.MaxAnimationFrames != 256 || d.MaxAnimationFramePixels != 16_000_000 || d.MaxSVGBytes != 5<<20 {
		t.Errorf("DefaultLimits = %+v", d)
	}
}
