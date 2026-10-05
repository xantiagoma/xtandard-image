package transform

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var update = flag.Bool("update", false, "rewrite testdata/canonical_keys.json")

const goldenKeysFile = "testdata/canonical_keys.json"

type goldenCase struct {
	name  string
	build func() Request
}

func src(u string) SourceSpec { return SourceSpec{Raw: u} }

func one(p *Pipeline) []Pipeline { return []Pipeline{*p} }

// goldenCases are realistic requests covering every area of the model. Their
// keys are pinned in testdata/canonical_keys.json: a change to any key means
// every cached result and ETag in production is invalidated, so it must be a
// deliberate decision (bump canonicalKeyVersion and run with -update).
func goldenCases() []goldenCase {
	const img = "https://example.com/images/a.jpg"
	return []goldenCase{
		{"empty", func() Request { return Request{Source: src(img)} }},
		{"width_only", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Resize: Resize{Width: Dim{Px: 300}}})}
		}},
		{"fill_300x400", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Resize: Resize{Type: ResizeFill, Width: Dim{Px: 300}, Height: Dim{Px: 400}}})}
		}},
		{"fill_smart_webp", func() Request {
			return Request{
				Source:    src(img),
				Pipelines: one(&Pipeline{Resize: Resize{Type: ResizeFill, Width: Dim{Px: 300}, Height: Dim{Px: 400}}, Gravity: Gravity{Type: GravitySmart}}),
				Output:    Output{Format: FormatWebP},
			}
		}},
		{"fill_down_enlarge_dpr2", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Resize: Resize{Type: ResizeFillDown, Width: Dim{Px: 200}, Height: Dim{Px: 200}, DPR: 2, Enlarge: true}})}
		}},
		{"force_nearest", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Resize: Resize{Type: ResizeForce, Width: Dim{Px: 64}, Height: Dim{Px: 64}, Algorithm: KernelNearest}})}
		}},
		{"auto_min_dims_zoom", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Resize: Resize{Type: ResizeAuto, MinWidth: 100, MinHeight: 50, ZoomX: 1.5, ZoomY: 0.5}})}
		}},
		{"outside_relative_width", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Resize: Resize{Type: ResizeOutside, Width: Dim{Rel: 0.5}, Height: Dim{Rel: 0.25}}})}
		}},
		{"imgix_max_and_aspect", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Resize: Resize{Width: Dim{Px: 800}, MaxWidth: 600, MaxHeight: 400, AspectRatio: 16.0 / 9}})}
		}},
		{"focus_point_zoom", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Resize:  Resize{Type: ResizeFill, Width: Dim{Px: 500}, Height: Dim{Px: 500}},
				Gravity: Gravity{Type: GravityFocusPoint, FocusX: 0.25, FocusY: 0.75, FocusZoom: 2},
			})}
		}},
		// WP-86 added Gravity.FocusPx. A nil pointer is a zero field and is
		// omitted from the encoding, so every key above is unchanged (the
		// golden file only gained this entry); a pixel focus gets its own
		// key, distinct from the fractional one at the same place.
		{"focus_point_pixels", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Resize:  Resize{Type: ResizeFill, Width: Dim{Px: 500}, Height: Dim{Px: 500}},
				Gravity: Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{X1: 146, Y1: 206, X2: 279, Y2: 360}},
			})}
		}},
		{"compass_offsets", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Resize:  Resize{Type: ResizeFill, Width: Dim{Px: 100}, Height: Dim{Px: 100}},
				Gravity: Gravity{Type: GravityNorthWest, XOffset: 10, YOffset: 0.1},
			})}
		}},
		{"objects_weighted", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Resize: Resize{Type: ResizeFill, Width: Dim{Px: 400}, Height: Dim{Px: 300}},
				Gravity: Gravity{
					Type: GravityObjectWeighted, Classes: []string{"face", "cat"}, Weights: []float64{2, 1},
					ObjectsPosition: &ObjectsPosition{Type: GravityFocusPoint, X: 0.5, Y: 0.3},
				},
			})}
		}},
		{"crop_then_resize", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Crop:   &Crop{Width: Dim{Px: 1000}, Height: Dim{Px: 800}, Gravity: Gravity{Type: GravityNorthWest, XOffset: 20, YOffset: 30}},
				Resize: Resize{Width: Dim{Px: 250}},
			})}
		}},
		{"crop_aspect_ratio", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Crop: &Crop{AspectRatio: 1.5, AspectEnlarge: true}})}
		}},
		{"trim_color_equal", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Trim: &Trim{Threshold: 10, Color: &Color{255, 255, 255, 255}, EqualHor: true}})}
		}},
		{"rotate_flip_no_autorotate", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Rotate: 90, Flip: Flip{H: true}, AutoRotate: new(false)})}
		}},
		{"rotate_any_background", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{RotateAny: 12.5, Background: &Color{0, 0, 0, 255}})}
		}},
		{"padding_extend_background", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Resize:     Resize{Width: Dim{Px: 300}, Height: Dim{Px: 300}},
				Padding:    &Padding{Top: 10, Right: 20, Bottom: 10, Left: 20},
				Extend:     Extend{Enabled: true, Gravity: Gravity{Type: GravitySouth}},
				Background: &Color{255, 0, 0, 128},
			})}
		}},
		{"extend_ar_copy_edges", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Resize:   Resize{Width: Dim{Px: 300}, Height: Dim{Px: 100}},
				ExtendAR: Extend{Enabled: true, Mode: ExtendCopy},
			})}
		}},
		{"border_radius_mask", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Border: &Border{Top: 4, Right: 4, Bottom: 4, Left: 4, Color: Color{0, 0, 0, 255}},
				Radius: &Radius{Max: true},
				Mask:   &Mask{Shape: MaskHeart, Trim: true},
			})}
		}},
		{"adjust_filters", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Adjust:    Adjust{Brightness: new(-20.0), Contrast: new(1.2), Saturation: new(0.8), Hue: new(45.0), Opacity: new(0.9)},
				Grayscale: true,
				Sepia:     true,
				Negate:    true,
			})}
		}},
		{"monochrome_duotone_colorize", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Monochrome: &Monochrome{Intensity: 0.5, Color: Color{0xb3, 0xb3, 0xb3, 255}},
				Duotone:    &Duotone{Intensity: 1, Shadow: Color{0, 0, 0, 255}, Highlight: Color{255, 255, 255, 255}},
				Colorize:   &Colorize{Opacity: 0.3, Color: Color{255, 0, 0, 255}, KeepAlpha: true},
				Tint:       &Color{0, 128, 255, 255},
			})}
		}},
		{"gradient_angle", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Gradient: &Gradient{Opacity: 0.7, Color: Color{0, 0, 0, 255}, Direction: DirectionAngle, Angle: 30, Start: 0.2, Stop: 0.9},
			})}
		}},
		{"blur_sharpen_pixelate", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Blur: 2.5, Sharpen: 0.7, Pixelate: 8})}
		}},
		{"unsharp_blur_areas_progressive", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				Unsharp:         &Unsharp{Mode: UnsharpAlways, Weight: 1, Divider: 24},
				BlurAreas:       &BlurAreas{Sigma: 10, Areas: []Area{{0.1, 0.1, 0.2, 0.2}, {0.5, 0.5, 0.3, 0.1}}},
				ProgressiveBlur: &ProgressiveBlur{Sigma: 5, Direction: DirectionUp, Start: 0.5, Stop: 1},
			})}
		}},
		{"detections", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{
				CropObjects:    &CropObjects{Scale: 1.2, Classes: []string{"person"}},
				BlurDetections: &DetectionFilter{Sigma: 8, Classes: []string{"face"}},
				DrawDetections: &DetectionFilter{Classes: []string{"car", "dog"}},
			})}
		}},
		{"watermarks", func() Request {
			return Request{Source: src(img), Pipelines: one(&Pipeline{Watermarks: []Watermark{
				{Opacity: 0.5, Position: Gravity{Type: GravitySouthEast, XOffset: 10, YOffset: 10}, Scale: 0.2},
				{Opacity: 0.3, Tile: TileRepeat, Source: &SourceSpec{Raw: "s3://bucket/logo.png"}, Width: 100, Rotate: 45, Shadow: 2},
				{Opacity: 1, Text: "<b>hello</b>", Font: "sans 12", Color: &Color{255, 255, 255, 255}},
			}})}
		}},
		{"chained_pipelines", func() Request {
			return Request{Source: src("s3://bucket/key.jpg"), Pipelines: []Pipeline{
				{Resize: Resize{Width: Dim{Px: 500}, Height: Dim{Px: 500}}, Watermarks: []Watermark{{Opacity: 0.5, Position: Gravity{Type: GravityNorthWest}}}},
				{Crop: &Crop{Width: Dim{Px: 200}, Height: Dim{Px: 200}}, Watermarks: []Watermark{{Opacity: 0.7, Position: Gravity{Type: GravitySouthEast}}}},
			}}
		}},
		{"svg_style_pages_animation", func() Request {
			return Request{Source: src("https://example.com/a.gif"), Pipelines: one(&Pipeline{
				SVGStyle: "path{fill:red}", Page: new(2), Pages: new(-1), DisableAnimation: true,
			})}
		}},
		{"video_tile", func() Request {
			return Request{Source: src("https://example.com/v.mp4"), Pipelines: one(&Pipeline{Video: &VideoOptions{
				Second: new(3.5), Keyframes: new(true),
				Tile: &VideoTile{Step: 1, Columns: 3, Rows: 2, TileWidth: 160, TileHeight: 90, Fill: true, FocusX: 0.5, FocusY: 0.5},
			}})}
		}},
		{"output_quality_and_format_quality", func() Request {
			return Request{Source: src(img), Output: Output{
				Format: FormatAVIF, Quality: 55,
				FormatQuality: map[Format]int{FormatJPEG: 82, FormatWebP: 75, FormatAVIF: 50},
			}}
		}},
		{"output_encoder_options", func() Request {
			return Request{Source: src(img), Output: Output{
				Format:            FormatJPEG,
				Progressive:       new(false),
				ChromaSubsampling: Subsampling444,
				Effort:            6,
				JPEG:              &JPEGOptions{TrellisQuant: new(true), QuantTable: new(3)},
				PNG:               &PNGOptions{Quantize: new(true), QuantizationColors: 128, Compression: new(9)},
				WebP:              &WebPOptions{Compression: WebPNearLossless, Preset: WebPPresetPhoto, SmartSubsample: new(true)},
				AVIF:              &AVIFOptions{Subsample: Subsampling444},
			}}
		}},
		{"output_auto_quality_max_bytes", func() Request {
			return Request{Source: src(img), Output: Output{
				AutoQuality: &AutoQuality{Method: AutoQualityDSSIM, Target: 0.02, Min: 60, Max: 85, AllowedError: 0.001},
				MaxBytes:    100_000,
				Lossless:    new(false),
			}}
		}},
		{"output_metadata_color", func() Request {
			return Request{Source: src(img), Output: Output{
				StripMetadata: new(false), KeepCopyright: new(false), StripColorProfile: new(false),
				ColorProfile: "cmyk", PreserveHDR: new(true), DPI: 300, EnforceThumbnail: new(true),
			}}
		}},
		{"delivery_options", func() Request {
			return Request{Source: src(img), Delivery: Delivery{
				Filename: "photo.jpg", Attachment: new(true), CacheBuster: "v2",
				Expires: time.Unix(1_900_000_000, 0).UTC(), TTL: 24 * time.Hour, Immutable: true,
				Fallback: &SourceSpec{Raw: "local:///fallback.png"}, OnErrorRedirect: true,
				SkipProcessing: []Format{FormatSVG, FormatGIF}, CacheTags: []string{"a", "b"},
				Encoding: ResponseDataURL,
			}}
		}},
		{"source_encrypted_hashsum_mount", func() Request {
			return Request{Source: SourceSpec{
				Raw: "Zm9vYmFy", Encoding: SourceEncrypted, Mount: "assets",
				Hashsum: &Hashsum{Algorithm: HashSHA256, Digest: strings.Repeat("ab", 32)},
			}}
		}},
		{"limits_and_hints", func() Request {
			return Request{
				Source:      src(img),
				ClientHints: ClientHints{Width: true, DPR: true, WidthStep: 100},
				Limits:      LimitOverrides{MaxSrcResolution: 25, MaxSrcFileSize: 10 << 20, MaxAnimationFrames: 50, MaxResultDimension: 4096},
			}
		}},
		{"kind_raw", func() Request { return Request{Kind: KindRaw, Source: src(img)} }},
		{"kind_info", func() Request {
			return Request{Kind: KindInfo, Source: src(img), Info: &InfoOptions{
				Size: true, Format: true, Dimensions: true, EXIF: true,
				Blurhash: &InfoBlurhash{XComponents: 4, YComponents: 3}, Palette: 16,
				CalcHashsums: []HashAlgorithm{HashMD5, HashSHA256},
			}}
		}},
		{"kind_video", func() Request {
			return Request{Kind: KindVideo, Source: src(img), Video: &VideoOp{
				Start: 1500 * time.Millisecond, Duration: 3 * time.Second, Width: 640, Height: 360,
				Fit: VideoFitPad, Background: Color{R: 255, A: 255}, Rotate: 90, FlipH: true, FPS: 24,
				Container: VideoContainerWebM, VideoCodec: VideoCodecVP9, AudioCodec: AudioCodecOpus,
				Quality: 70, MaxBitrate: 1_000_000, AudioBitrate: 96_000, Crop: &VideoCrop{Width: 100, Height: 100},
			}}
		}},
		{"kind_video_overlays", func() Request {
			return Request{Kind: KindVideo, Source: src(img), Video: &VideoOp{Overlays: []VideoOverlay{
				{
					Opacity: 0.5, Source: &SourceSpec{Raw: "logo.png"}, Scale: 0.2,
					Position: Gravity{Type: GravitySouthEast, XOffset: 10, YOffset: 10},
					Start:    time.Second, End: 3 * time.Second, FadeIn: 500 * time.Millisecond,
				},
				{Opacity: 1, Text: "<b>hi</b>"},
				{Opacity: 1, Timecode: VideoTimecodeHMS, TimecodeSize: 32},
			}}}
		}},
		{"kind_info_thumbor", func() Request {
			return Request{Kind: KindInfo, Source: src(img), Info: &InfoOptions{Schema: InfoSchemaThumbor}}
		}},
	}
}

func readGoldenKeys(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(goldenKeysFile)
	if err != nil {
		t.Fatalf("read golden keys: %v (run go test -run TestCanonicalKeyGolden -update)", err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s: %v", goldenKeysFile, err)
	}
	return m
}

// TestCanonicalKeyGolden pins CanonicalKey. If it fails, a key changed: every
// result-cache entry and ETag would be invalidated. Only regenerate with
// -update when that is intended (and bump canonicalKeyVersion).
func TestCanonicalKeyGolden(t *testing.T) {
	cases := goldenCases()
	if len(cases) < 30 {
		t.Fatalf("only %d golden cases; the WP-03 contract requires ≥ 30", len(cases))
	}
	got := make(map[string]string, len(cases))
	byKey := map[string]string{}
	for _, tc := range cases {
		r := tc.build()
		r.ApplyDefaults(StandardDefaults())
		if err := r.Validate(); err != nil {
			t.Errorf("%s: golden request does not validate: %v", tc.name, err)
		}
		k := r.CanonicalKey()
		if prev, dup := byKey[k]; dup {
			t.Errorf("%s and %s share key %s", tc.name, prev, k)
		}
		byKey[k] = tc.name
		got[tc.name] = k
	}

	if *update {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(goldenKeysFile), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenKeysFile, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if diff := cmp.Diff(readGoldenKeys(t), got); diff != "" {
		t.Fatalf("CanonicalKey changed (-golden +got):\n%s", diff)
	}
}

func TestCanonicalKeyFormat(t *testing.T) {
	t.Parallel()
	r := Request{Source: src("x")}
	r.ApplyDefaults(StandardDefaults())
	k := r.CanonicalKey()
	if len(k) != 64 || strings.Trim(k, "0123456789abcdef") != "" {
		t.Fatalf("CanonicalKey = %q, want 64 lowercase hex digits", k)
	}
	if k != r.CanonicalKey() {
		t.Fatal("CanonicalKey is not deterministic")
	}
}

// defaultedKey returns the key of a defaulted copy of r.
func defaultedKey(r *Request) string {
	c := *r
	c.ApplyDefaults(StandardDefaults())
	return c.CanonicalKey()
}

func caseKey(tc goldenCase) string {
	r := tc.build()
	return defaultedKey(&r)
}

func TestCanonicalKeyExcludedFields(t *testing.T) {
	t.Parallel()
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := caseKey(tc)

			r := tc.build()
			r.Dialect = "cloudinary"
			r.Ignored = []Ignored{{Option: "gif_options", Reason: "removed upstream"}, {Option: "e_art", Reason: "unsupported"}}
			r.Delivery.Presets = []string{"default", "thumb"}
			r.Delivery.BypassCache = true
			if got := defaultedKey(&r); got != base {
				t.Fatalf("excluded fields changed the key: %s → %s", base, got)
			}
		})
	}
}

func TestCanonicalKeyEquivalentRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b Request
	}{
		{
			"explicit defaults equal implicit",
			Request{},
			Request{
				Pipelines: []Pipeline{{
					Resize:     Resize{Type: ResizeFit, ZoomX: 1, ZoomY: 1, DPR: 1, Algorithm: KernelLanczos3},
					Gravity:    Gravity{Type: GravityCenter},
					AutoRotate: new(true),
				}},
				Output: Output{
					StripMetadata: new(true), KeepCopyright: new(true), StripColorProfile: new(true),
					PreserveHDR: new(false), EnforceThumbnail: new(false),
				},
				Delivery: Delivery{Attachment: new(false)},
			},
		},
		{
			"rotation normalisation",
			Request{Pipelines: one(&Pipeline{Rotate: -90})},
			Request{Pipelines: one(&Pipeline{RotateAny: 270})},
		},
		{
			"identity adjustment",
			Request{Pipelines: one(&Pipeline{Adjust: Adjust{Contrast: new(1.0), Brightness: new(0.0)}})},
			Request{},
		},
		{
			"negative zero",
			Request{Pipelines: one(&Pipeline{Gravity: Gravity{XOffset: negZero()}})},
			Request{},
		},
		{
			"empty vs nil slices and maps",
			Request{Output: Output{FormatQuality: map[Format]int{}}, Delivery: Delivery{CacheTags: []string{}}},
			Request{},
		},
		{
			"crop gravity inherited equals explicit",
			Request{Pipelines: one(&Pipeline{Gravity: Gravity{Type: GravitySmart}, Crop: &Crop{Width: Dim{Px: 10}}})},
			Request{Pipelines: one(&Pipeline{Gravity: Gravity{Type: GravitySmart}, Crop: &Crop{Width: Dim{Px: 10}, Gravity: Gravity{Type: GravitySmart}}})},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if ka, kb := defaultedKey(&tc.a), defaultedKey(&tc.b); ka != kb {
				t.Fatalf("keys differ: %s vs %s", ka, kb)
			}
		})
	}
}

func negZero() float64 {
	z := 0.0
	return -z
}

func TestCanonicalKeyDistinguishes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b Request
	}{
		{"nil vs pointer to false", Request{}, Request{Output: Output{Lossless: new(false)}}},
		{"px vs rel", Request{Pipelines: one(&Pipeline{Resize: Resize{Width: Dim{Px: 1}}})}, Request{Pipelines: one(&Pipeline{Resize: Resize{Width: Dim{Rel: 1}}})}},
		{"width vs height", Request{Pipelines: one(&Pipeline{Resize: Resize{Width: Dim{Px: 100}}})}, Request{Pipelines: one(&Pipeline{Resize: Resize{Height: Dim{Px: 100}}})}},
		{"pipeline order", Request{Pipelines: []Pipeline{{Blur: 1}, {Sharpen: 1}}}, Request{Pipelines: []Pipeline{{Sharpen: 1}, {Blur: 1}}}},
		{"one vs two pipelines", Request{Pipelines: []Pipeline{{Blur: 1}}}, Request{Pipelines: []Pipeline{{Blur: 1}, {}}}},
		{"string boundaries", Request{Source: src("ab"), Delivery: Delivery{CacheBuster: "c"}}, Request{Source: src("a"), Delivery: Delivery{CacheBuster: "bc"}}},
		{"tags vs single tag", Request{Delivery: Delivery{CacheTags: []string{"a", "b"}}}, Request{Delivery: Delivery{CacheTags: []string{"ab"}}}},
		{"expires", Request{Delivery: Delivery{Expires: time.Unix(1, 0)}}, Request{Delivery: Delivery{Expires: time.Unix(2, 0)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if ka, kb := defaultedKey(&tc.a), defaultedKey(&tc.b); ka == kb {
				t.Fatalf("different requests share key %s", ka)
			}
		})
	}
}

func TestCanonicalKeyMapOrderIndependent(t *testing.T) {
	t.Parallel()
	fq := func(order []Format) map[Format]int {
		m := make(map[Format]int, len(order))
		for _, f := range order {
			m[f] = 50 + int(f)
		}
		return m
	}
	a := Request{Output: Output{FormatQuality: fq([]Format{FormatJPEG, FormatWebP, FormatAVIF, FormatPNG, FormatJXL})}}
	b := Request{Output: Output{FormatQuality: fq([]Format{FormatJXL, FormatPNG, FormatAVIF, FormatWebP, FormatJPEG})}}
	want := defaultedKey(&a)
	for range 50 { // Go randomises map iteration; repeat to exercise it
		if got := defaultedKey(&b); got != want {
			t.Fatalf("map order changed the key: %s vs %s", want, got)
		}
	}
}

// TestCanonicalKeyCoversEveryField walks the Request type and proves that
// changing any single field changes the key, except the excluded ones, which
// must not. A new field is therefore covered automatically, and a field that
// the encoder cannot represent fails here.
func TestCanonicalKeyCoversEveryField(t *testing.T) {
	t.Parallel()
	newBase := func() *Request {
		r := &Request{Source: src("https://example.com/a.jpg")}
		r.ApplyDefaults(StandardDefaults())
		return r
	}
	baseKey := newBase().CanonicalKey()

	type step func(reflect.Value) reflect.Value
	navigate := func(r *Request, steps []step) reflect.Value {
		v := reflect.ValueOf(r).Elem()
		for _, s := range steps {
			v = s(v)
		}
		return v
	}
	field := func(i int) step { return func(v reflect.Value) reflect.Value { return v.Field(i) } }
	deref := func(v reflect.Value) reflect.Value {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return v.Elem()
	}
	index0 := func(v reflect.Value) reflect.Value {
		if v.Kind() == reflect.Slice && v.Len() == 0 {
			v.Set(reflect.Append(v, reflect.Zero(v.Type().Elem())))
		}
		return v.Index(0)
	}

	// check applies mutate at path and compares against the same request
	// navigated but not mutated.
	check := func(path string, excluded bool, steps []step, mutate func(reflect.Value)) {
		before := newBase()
		navigate(before, steps)
		after := newBase()
		mutate(navigate(after, steps))
		kb, ka := before.CanonicalKey(), after.CanonicalKey()
		// Navigating may allocate a nil pointer on the way (a watermark's
		// or the fallback's SourceSpec, whose Password is excluded): the
		// navigated request then differs from the base, so an excluded field
		// is only held to "mutating it changes nothing".
		switch {
		case excluded && (ka != kb || (ka != baseKey && !strings.Contains(path, "*"))):
			t.Errorf("%s: excluded field changed the key", path)
		case !excluded && ka == kb:
			t.Errorf("%s: changing the field does not change the key", path)
		}
	}

	var visit func(typ reflect.Type, path string, excluded bool, steps []step)
	visit = func(typ reflect.Type, path string, excluded bool, steps []step) {
		steps = slices.Clip(steps) // copy-on-append
		switch typ.Kind() {
		case reflect.Struct:
			if typ == reflect.TypeFor[time.Time]() {
				check(path, excluded, steps, func(v reflect.Value) { v.Set(reflect.ValueOf(time.Unix(12345, 678))) })
				return
			}
			for i := range typ.NumField() {
				f := typ.Field(i)
				if !f.IsExported() {
					t.Errorf("%s.%s: unexported fields are invisible to CanonicalKey", path, f.Name)
					continue
				}
				visit(f.Type, path+"."+f.Name, excluded || f.Tag.Get("key") == "-", append(steps, field(i)))
			}
		case reflect.Pointer:
			check(path, excluded, steps, func(v reflect.Value) {
				if v.IsNil() {
					v.Set(reflect.New(typ.Elem()))
				} else {
					v.Set(reflect.Zero(typ))
				}
			})
			visit(typ.Elem(), path+"*", excluded, append(steps, deref))
		case reflect.Slice:
			check(path, excluded, steps, func(v reflect.Value) {
				v.Set(reflect.Append(v, reflect.Zero(typ.Elem())))
			})
			visit(typ.Elem(), path+"[0]", excluded, append(steps, index0))
		case reflect.Map:
			check(path, excluded, steps, func(v reflect.Value) {
				if v.IsNil() {
					v.Set(reflect.MakeMap(typ))
				}
				v.SetMapIndex(reflect.ValueOf(FormatPNG), reflect.ValueOf(42))
			})
		case reflect.Bool:
			check(path, excluded, steps, func(v reflect.Value) { v.SetBool(!v.Bool()) })
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			check(path, excluded, steps, func(v reflect.Value) { v.SetInt(v.Int() + 1) })
		case reflect.Uint8:
			check(path, excluded, steps, func(v reflect.Value) { v.SetUint(v.Uint() + 1) })
		case reflect.Float64:
			check(path, excluded, steps, func(v reflect.Value) { v.SetFloat(v.Float() + 0.5) })
		case reflect.String:
			check(path, excluded, steps, func(v reflect.Value) { v.SetString(v.String() + "x") })
		default:
			t.Errorf("%s: kind %s is not supported by CanonicalKey", path, typ.Kind())
		}
	}
	visit(reflect.TypeFor[Request](), "Request", false, nil)
}

func BenchmarkCanonicalKey(b *testing.B) {
	cases := goldenCases()
	reqs := make([]Request, len(cases))
	for i, tc := range cases {
		reqs[i] = tc.build()
		reqs[i].ApplyDefaults(StandardDefaults())
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		_ = reqs[i%len(reqs)].CanonicalKey()
		i++
	}
}
