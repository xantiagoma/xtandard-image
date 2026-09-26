package encode

import (
	"bytes"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestResolveQuality is the precedence table of FORMATS §5 / imgproxy:
// quality > format_quality[f] > XI_FORMATS_<FMT>_QUALITY > XI_FORMATS_QUALITY.
func TestResolveQuality(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Quality = 70
	fq := map[transform.Format]int{transform.FormatWebP: 55, transform.FormatTIFF: 33}
	for _, tc := range []struct {
		name    string
		f       transform.Format
		quality int
		fq      map[transform.Format]int
		want    int
	}{
		{name: "explicit wins over everything", f: transform.FormatWebP, quality: 91, fq: fq, want: 91},
		{name: "format_quality over config", f: transform.FormatWebP, fq: fq, want: 55},
		{name: "format_quality for another format ignored", f: transform.FormatJPEG, fq: fq, want: 80},
		{name: "config per-format jpeg", f: transform.FormatJPEG, want: 80},
		{name: "config per-format webp", f: transform.FormatWebP, want: 80},
		{name: "config per-format avif", f: transform.FormatAVIF, want: 60},
		{name: "config per-format heic", f: transform.FormatHEIC, want: 60},
		{name: "config per-format jxl", f: transform.FormatJXL, want: 75},
		{name: "global for tiff", f: transform.FormatTIFF, want: 70},
		{name: "format_quality for tiff", f: transform.FormatTIFF, fq: fq, want: 33},
		{name: "zero format_quality falls through", f: transform.FormatAVIF, fq: map[transform.Format]int{transform.FormatAVIF: 0}, want: 60},
		{name: "explicit clamped", f: transform.FormatJPEG, quality: 400, want: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cfg.ResolveQuality(tc.f, tc.quality, tc.fq); got != tc.want {
				t.Errorf("ResolveQuality = %d, want %d", got, tc.want)
			}
		})
	}
	t.Run("global when per-format unset", func(t *testing.T) {
		t.Parallel()
		c := Config{Quality: 42}
		if got := c.ResolveQuality(transform.FormatJPEG, 0, nil); got != 42 {
			t.Errorf("ResolveQuality = %d, want 42", got)
		}
	})
}

func TestPlanFor(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	def := vips.DefaultSaveOptions()
	photo := imageInfo{pixels: 320 * 240}
	for _, tc := range []struct {
		name    string
		t       Target
		info    imageInfo
		format  vips.Format
		quality int
		check   func(t *testing.T, o *vips.SaveOptions)
	}{
		{
			name: "jpeg defaults", t: Target{Format: transform.FormatJPEG}, info: photo,
			format: vips.FormatJPEG, quality: 80,
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				want := vips.JPEGOptions{Quality: 80, Interlace: true, OptimizeCoding: true, Subsample: vips.SubsampleAuto}
				if diff := cmp.Diff(want, o.JPEG); diff != "" {
					t.Error(diff)
				}
			},
		},
		{
			name: "jpeg options", info: photo, format: vips.FormatJPEG, quality: 90,
			t: Target{Format: transform.FormatJPEG, Quality: 90, Options: Options{
				Progressive: new(true), ChromaSubsampling: transform.Subsampling420,
				JPEG: &transform.JPEGOptions{Progressive: new(false), NoSubsample: new(true)},
			}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if o.JPEG.Interlace || o.JPEG.Subsample != vips.SubsampleOff {
					t.Errorf("jpeg = %+v, want baseline 4:4:4", o.JPEG)
				}
			},
		},
		{
			name: "jpeg mozjpeg options", info: photo, format: vips.FormatJPEG, quality: 80,
			t: Target{Format: transform.FormatJPEG, Options: Options{JPEG: &transform.JPEGOptions{
				TrellisQuant: new(true), OvershootDeringing: new(true), OptimizeScans: new(true), QuantTable: new(3),
			}}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				want := vips.JPEGOptions{
					Quality: 80, Interlace: true, OptimizeCoding: true, Subsample: vips.SubsampleAuto,
					TrellisQuant: true, OvershootDeringing: true, OptimizeScans: true, QuantTable: 3,
				}
				if diff := cmp.Diff(want, o.JPEG); diff != "" {
					t.Error(diff)
				}
			},
		},
		{
			name: "png plain", t: Target{Format: transform.FormatPNG}, info: photo, format: vips.FormatPNG,
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				want := def.PNG
				want.Filter = vips.PNGFilterAll
				if diff := cmp.Diff(want, o.PNG); diff != "" {
					t.Error(diff)
				}
			},
		},
		{
			name: "png quantised by decision", info: photo, format: vips.FormatPNG,
			t: Target{Format: transform.FormatPNG, Options: Options{Quantize: true, Effort: 9, PNG: &transform.PNGOptions{
				QuantizationColors: 12, Interlaced: new(true), Compression: new(9),
			}}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				want := vips.PNGOptions{
					Compression: 9, Interlace: true, Filter: vips.PNGFilterNone, Palette: true,
					Quality: def.PNG.Quality, Dither: def.PNG.Dither, BitDepth: 4, Effort: 10,
				}
				if diff := cmp.Diff(want, o.PNG); diff != "" {
					t.Error(diff)
				}
			},
		},
		{
			name: "png quantise vetoed", info: photo, format: vips.FormatPNG,
			t: Target{Format: transform.FormatPNG, Options: Options{Quantize: true, PNG: &transform.PNGOptions{
				Quantize: new(false), AdaptiveFilter: new(false),
			}}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if o.PNG.Palette || o.PNG.Filter != vips.PNGFilterNone {
					t.Errorf("png = %+v, want truecolour without filters", o.PNG)
				}
			},
		},
		{
			name: "webp lossy", info: photo, format: vips.FormatWebP, quality: 80,
			t: Target{Format: transform.FormatWebP, Options: Options{WebP: &transform.WebPOptions{
				SmartSubsample: new(true), Preset: transform.WebPPresetPhoto,
			}}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				w := o.WebP
				if w.Lossless || w.NearLossless || !w.SmartSubsample || w.Preset != vips.WebPPresetPhoto || w.Effort != 4 || w.Quality != 80 {
					t.Errorf("webp = %+v", w)
				}
				if w.KMin != def.WebP.KMin || w.KMax != def.WebP.KMax || w.MinSize {
					t.Errorf("webp animation options changed: %+v", w)
				}
			},
		},
		{
			name: "webp lossless by decision", info: photo, format: vips.FormatWebP,
			t: Target{Format: transform.FormatWebP, Options: Options{PreferLossless: true, Effort: 1}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if !o.WebP.Lossless || o.WebP.Effort != 0 {
					t.Errorf("webp = %+v, want lossless effort 0", o.WebP)
				}
			},
		},
		{
			name: "webp lossy compression beats decision", info: photo, format: vips.FormatWebP, quality: 80,
			t: Target{Format: transform.FormatWebP, Options: Options{
				PreferLossless: true, WebP: &transform.WebPOptions{Compression: transform.WebPLossy},
			}},
		},
		{
			name: "webp near-lossless", info: photo, format: vips.FormatWebP, quality: 80,
			t: Target{Format: transform.FormatWebP, Options: Options{WebP: &transform.WebPOptions{Compression: transform.WebPNearLossless}}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if !o.WebP.NearLossless || o.WebP.Lossless {
					t.Errorf("webp = %+v, want near-lossless", o.WebP)
				}
			},
		},
		{
			name: "avif defaults", t: Target{Format: transform.FormatAVIF}, info: photo,
			format: vips.FormatAVIF, quality: 60,
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				want := vips.HEIFOptions{Quality: 60, Effort: 4, Subsample: vips.SubsampleAuto}
				if diff := cmp.Diff(want, o.HEIF); diff != "" {
					t.Error(diff)
				}
			},
		},
		{
			name: "avif large image caps effort", t: Target{Format: transform.FormatAVIF},
			info: imageInfo{pixels: 5_000_000}, format: vips.FormatAVIF, quality: 60,
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if o.HEIF.Effort != 2 {
					t.Errorf("effort = %d, want 2", o.HEIF.Effort)
				}
			},
		},
		{
			name: "avif explicit effort beats the cap", info: imageInfo{pixels: 5_000_000}, format: vips.FormatAVIF, quality: 60,
			t: Target{Format: transform.FormatAVIF, Options: Options{Effort: 9, ChromaSubsampling: transform.Subsampling444}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if o.HEIF.Effort != 9 || o.HEIF.Subsample != vips.SubsampleOff {
					t.Errorf("heif = %+v, want effort 9, 4:4:4", o.HEIF)
				}
			},
		},
		{
			name: "avif_options subsample wins", info: photo, format: vips.FormatAVIF, quality: 60,
			t: Target{Format: transform.FormatAVIF, Options: Options{
				ChromaSubsampling: transform.Subsampling444, AVIF: &transform.AVIFOptions{Subsample: transform.Subsampling420},
			}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if o.HEIF.Subsample != vips.SubsampleOn {
					t.Errorf("subsample = %v, want on", o.HEIF.Subsample)
				}
			},
		},
		{
			name: "avif lossless has no quality", info: photo, format: vips.FormatAVIF,
			t: Target{Format: transform.FormatAVIF, Options: Options{Lossless: new(true)}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if !o.HEIF.Lossless {
					t.Error("not lossless")
				}
			},
		},
		{
			name: "heic 8-bit", t: Target{Format: transform.FormatHEIC}, info: photo, format: vips.FormatHEIF, quality: 60,
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if o.HEIF.BitDepth != 8 || o.HEIF.Lossless {
					t.Errorf("heif = %+v", o.HEIF)
				}
			},
		},
		{
			name: "jxl defaults", t: Target{Format: transform.FormatJXL}, info: photo, format: vips.FormatJXL, quality: 75,
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				want := vips.JXLOptions{Quality: 75, Effort: 5}
				if diff := cmp.Diff(want, o.JXL); diff != "" {
					t.Error(diff)
				}
			},
		},
		{
			name: "jxl lossless effort 9", info: photo, format: vips.FormatJXL,
			t: Target{Format: transform.FormatJXL, Options: Options{Lossless: new(true), Effort: 9}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if !o.JXL.Lossless || o.JXL.Effort != 10 {
					t.Errorf("jxl = %+v", o.JXL)
				}
			},
		},
		{
			name: "gif", info: photo, format: vips.FormatGIF,
			t: Target{Format: transform.FormatGIF, Quality: 50, Options: Options{Progressive: new(true)}},
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				want := def.GIF
				want.Interlace = true
				if diff := cmp.Diff(want, o.GIF); diff != "" {
					t.Error(diff)
				}
			},
		},
		{
			name: "tiff photo is jpeg-compressed", t: Target{Format: transform.FormatTIFF}, info: photo,
			format: vips.FormatTIFF, quality: 80,
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if o.TIFF.Compression != vips.TIFFCompressionJPEG || o.TIFF.Quality != 80 {
					t.Errorf("tiff = %+v", o.TIFF)
				}
			},
		},
		{
			name: "tiff with alpha is deflate", t: Target{Format: transform.FormatTIFF},
			info: imageInfo{pixels: 100, hasAlpha: true}, format: vips.FormatTIFF,
			check: func(t *testing.T, o *vips.SaveOptions) {
				t.Helper()
				if o.TIFF.Compression != vips.TIFFCompressionDeflate || o.TIFF.Predictor != vips.TIFFPredictorHorizontal {
					t.Errorf("tiff = %+v", o.TIFF)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, ok := cfg.planFor(&tc.t, tc.info)
			if !ok {
				t.Fatal("planFor refused the format")
			}
			if p.format != tc.format || p.quality != tc.quality {
				t.Errorf("format, quality = %v, %d; want %v, %d", p.format, p.quality, tc.format, tc.quality)
			}
			if p.opts.Keep != vips.KeepNone {
				t.Errorf("keep = %v before the metadata policy", p.opts.Keep)
			}
			if tc.check != nil {
				tc.check(t, &p.opts)
			}
		})
	}
	for _, f := range []transform.Format{
		transform.FormatUnset, transform.FormatBMP, transform.FormatICO, transform.FormatSVG,
		transform.FormatMP4, transform.FormatWebM, transform.FormatJSON, transform.FormatBest,
	} {
		if _, ok := cfg.planFor(&Target{Format: f}, photo); ok {
			t.Errorf("planFor(%q) accepted a format libvips does not save", f)
		}
	}
}

func TestNativeEffort(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ e, low, high, want int }{
		{1, 0, 6, 0},
		{5, 0, 6, 3},
		{9, 0, 6, 6},
		{1, 0, 9, 0},
		{9, 0, 9, 9},
		{1, 1, 10, 1},
		{5, 1, 10, 6},
		{9, 1, 10, 10},
		{0, 1, 10, 1},
		{42, 1, 10, 10},
	} {
		if got := nativeEffort(tc.e, tc.low, tc.high); got != tc.want {
			t.Errorf("nativeEffort(%d, %d, %d) = %d, want %d", tc.e, tc.low, tc.high, got, tc.want)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	def := DefaultConfig()
	if err := def.Validate(); err != nil {
		t.Fatalf("default config: %v", err)
	}
	bad := def
	bad.Quality = 0
	bad.FormatQuality = map[transform.Format]int{transform.FormatJPEG: 101}
	bad.WebPEffort = 7
	bad.JXLEffort = 0
	bad.AVIFLargePixels = -1
	if _, err := New(&bad); err == nil {
		t.Fatal("New accepted an invalid config")
	}
}

func TestNewTarget(t *testing.T) {
	t.Parallel()
	out := &transform.Output{
		Format: transform.FormatUnset, Quality: 70,
		FormatQuality: map[transform.Format]int{transform.FormatAVIF: 50},
		MaxBytes:      1000, Lossless: new(false), Effort: 3,
		StripMetadata: new(true), KeepCopyright: new(false), StripColorProfile: new(false),
	}
	d := &negotiate.Decision{Format: transform.FormatWebP, Lossless: true, Quantize: true}
	got := NewTarget(out, d, true)
	want := Target{
		Format: transform.FormatWebP, Quality: 70, FormatQuality: out.FormatQuality,
		Options:  Options{Lossless: new(false), Effort: 3, PreferLossless: true, Quantize: true},
		Metadata: Metadata{Strip: true, StripColorProfile: false},
		Animated: true, MaxBytes: 1000,
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("NewTarget (-want +got):\n%s", diff)
	}
	if def := NewTarget(&transform.Output{}, d, false).Metadata; def != (Metadata{Strip: true, KeepCopyright: true, StripColorProfile: true}) {
		t.Errorf("default metadata = %+v", def)
	}
}

func TestStripXMPGPS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, want string
		changed        bool
	}{
		{
			name: "attributes",
			in:   `<rdf:Description rdf:about="" exif:GPSLatitude="52,31.0N" dc:format="image/jpeg" exif:GPSLongitude='13,24.0E'/>`,
			want: `<rdf:Description rdf:about="" dc:format="image/jpeg"/>`, changed: true,
		},
		{
			name: "elements",
			in:   "<rdf:Description>\n <exif:GPSLatitude>52,31.0N</exif:GPSLatitude>\n <dc:rights>(c) me</dc:rights>\n <exif:GPSAltitudeRef/>\n</rdf:Description>",
			want: "<rdf:Description>\n \n <dc:rights>(c) me</dc:rights>\n \n</rdf:Description>", changed: true,
		},
		{
			name: "nested element",
			in:   `<a><exif:GPSTimeStamp><rdf:Seq><rdf:li>1</rdf:li></rdf:Seq></exif:GPSTimeStamp><b/></a>`,
			want: `<a><b/></a>`, changed: true,
		},
		{name: "no gps", in: `<x exif:Make="Foo"/>`, want: `<x exif:Make="Foo"/>`},
		{name: "text mention", in: `<dc:description>about exif:GPS tags</dc:description>`, want: `<dc:description>about exif:GPS tags</dc:description>`},
		{name: "unterminated element", in: `<a><exif:GPSLatitude>1`, want: `<a><exif:GPSLatitude>1`, changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, changed := stripXMPGPS([]byte(tc.in))
			if string(got) != tc.want || changed != tc.changed {
				t.Errorf("stripXMPGPS = %q, %v; want %q, %v", got, changed, tc.want, tc.changed)
			}
		})
	}
}

func FuzzStripXMPGPS(f *testing.F) {
	f.Add([]byte(`<x exif:GPSLatitude="1" y="2"><exif:GPSAltitude>3</exif:GPSAltitude></x>`))
	f.Add([]byte(`exif:GPS`))
	f.Add([]byte(`<exif:GPS`))
	f.Add([]byte(` exif:GPSx=`))
	f.Fuzz(func(t *testing.T, in []byte) {
		out, changed := stripXMPGPS(in)
		if !changed && !bytes.Equal(out, in) {
			t.Fatalf("unchanged output differs: %q -> %q", in, out)
		}
		if len(out) > len(in) {
			t.Fatalf("output grew: %q -> %q", in, out)
		}
	})
}
