package vips

import "testing"

func TestFormatOfLoader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		loader, sub string
		want        Format
	}{
		{"jpegload_buffer", "", FormatJPEG},
		{"jpegload", "", FormatJPEG},
		{"uhdrload_buffer", "", FormatJPEG},
		{"pngload_buffer", "", FormatPNG},
		{"webpload_buffer", "", FormatWebP},
		{"gifload_buffer", "", FormatGIF},
		{"heifload_buffer", "av1", FormatAVIF},
		{"heifload_buffer", "hevc", FormatHEIF},
		{"heifload_buffer", "", FormatHEIF},
		{"jxlload_buffer", "", FormatJXL},
		{"tiffload_buffer", "", FormatTIFF},
		{"svgload_buffer", "", FormatSVG},
		{"jp2kload_buffer", "", FormatJP2K},
		{"ppmload_buffer", "", FormatUnknown},
		{"", "", FormatUnknown},
	}
	for _, tc := range tests {
		if got := formatOfLoader(tc.loader, tc.sub); got != tc.want {
			t.Errorf("formatOfLoader(%q, %q) = %v, want %v", tc.loader, tc.sub, got, tc.want)
		}
	}
}

func TestFormatString(t *testing.T) {
	t.Parallel()

	want := map[Format]string{
		FormatUnknown: "unknown", FormatJPEG: "jpeg", FormatPNG: "png", FormatWebP: "webp",
		FormatGIF: "gif", FormatAVIF: "avif", FormatHEIF: "heif", FormatJXL: "jxl",
		FormatTIFF: "tiff", FormatSVG: "svg", FormatJP2K: "jp2k", Format(-1): "unknown",
		Format(100): "unknown",
	}
	for f, s := range want {
		if got := f.String(); got != s {
			t.Errorf("Format(%d).String() = %q, want %q", int(f), got, s)
		}
	}
}

func TestDefaultSaveOptions(t *testing.T) {
	t.Parallel()

	o := DefaultSaveOptions()
	if o.Keep != KeepNone || o.JPEG.Quality != 75 || o.WebP.Quality != 75 || o.HEIF.Quality != 50 ||
		o.JXL.Quality != 75 || o.PNG.Compression != 6 || o.GIF.BitDepth != 8 ||
		o.TIFF.Predictor != TIFFPredictorHorizontal {
		t.Errorf("DefaultSaveOptions drifted from the libvips defaults: %+v", o)
	}
	if KeepAll != 63 {
		t.Errorf("KeepAll = %d", KeepAll)
	}
}

func TestArenaWithoutImages(t *testing.T) {
	t.Parallel()

	var a Arena
	if img, err := a.Add(nil, nil); img != nil || err != nil || a.Len() != 0 {
		t.Errorf("Add(nil, nil) = %v, %v; Len %d", img, err, a.Len())
	}
	a.Close()
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	if err := new(DefaultConfig()).Validate(); err != nil {
		t.Errorf("DefaultConfig invalid: %v", err)
	}
	bad := Config{Concurrency: 0, CacheMax: -1}
	if err := bad.Validate(); err == nil {
		t.Error("invalid config passed Validate")
	}
	if len(DefaultSavers()) == 0 || len(DefaultLoaders()) == 0 {
		t.Error("empty default allowlists")
	}
}
