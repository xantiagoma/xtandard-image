package transform

import "testing"

// TestCheckRaw: the probe admits a camera RAW when its preview or its
// half-size demosaic fits (WP-102); the sensor alone never rejects it.
func TestCheckRaw(t *testing.T) {
	t.Parallel()
	gfx := RawSource{Width: 11662, Height: 8746, PreviewWidth: 4000, PreviewHeight: 3000}
	x1d := RawSource{Width: 8278, Height: 6208}
	huge := RawSource{Width: 20_000, Height: 15_000}
	he := RawSource{Width: 5568, Height: 3712, PreviewWidth: 5568, PreviewHeight: 3712, PreviewOnly: true}
	bigPreviewOnly := RawSource{Width: 9000, Height: 6000, PreviewWidth: 9000, PreviewHeight: 6000, PreviewOnly: true}
	for _, tc := range []struct {
		name string
		lim  SourceLimits
		r    RawSource
		want string
	}{
		{"102 MP with a preview", defaults, gfx, ""},
		{"51 MP without a preview: half size fits", defaults, x1d, ""},
		{
			"300 MP: half size too large", defaults, huge,
			"camera RAW half-size demosaic is 10000×7500 (75 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP",
		},
		{"preview only", defaults, he, ""},
		{
			"preview only, preview too large", defaults, bigPreviewOnly,
			"camera RAW embedded preview is 9000×6000 (54 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP",
		},
		{"lowered limit: the preview still fits", SourceLimits{MaxSrcPixels: 12_000_000}, gfx, ""},
		{
			"lowered limit: nothing fits",
			SourceLimits{MaxSrcPixels: 10_000_000},
			gfx,
			"camera RAW half-size demosaic is 5831×4373 (25.5 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 10 MP",
		},
		{"unlimited", SourceLimits{}, huge, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := publicOf(t, tc.lim.CheckRaw(&tc.r)); got != tc.want {
				t.Errorf("CheckRaw(%+v) = %q, want %q", tc.r, got, tc.want)
			}
		})
	}
}

// TestCheckRawDecode names the decode path the planner picked.
func TestCheckRawDecode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		d    RawDecode
		w, h int
		want string
	}{
		{"full demosaic over", RawDemosaic, 11662, 8746, "source is 11662×8746 (102 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP"},
		{"half size within", RawDemosaicHalf, 5831, 4373, ""},
		{
			"half size over", RawDemosaicHalf, 10_000, 7500,
			"camera RAW half-size demosaic is 10000×7500 (75 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP",
		},
		{
			"preview over", RawPreview, 9000, 6000,
			"camera RAW embedded preview is 9000×6000 (54 MP); limit XI_LIMITS_MAX_SRC_PIXELS is 50 MP",
		},
		{"preview within", RawPreview, 8256, 5504, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := publicOf(t, defaults.CheckRawDecode(tc.d, tc.w, tc.h)); got != tc.want {
				t.Errorf("CheckRawDecode(%d, %d, %d) = %q, want %q", tc.d, tc.w, tc.h, got, tc.want)
			}
		})
	}
}

func TestCheckRawSensor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		w, h int
		max  int64
		want string
	}{
		{"GFX100 within the default", 11662, 8746, 120_000_000, ""},
		{
			"over", 14204, 10652, 120_000_000,
			"camera RAW sensor is 14204×10652 (151.3 MP); limit XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS is 120 MP",
		},
		{"unlimited", 100_000, 100_000, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := publicOf(t, CheckRawSensor(tc.w, tc.h, tc.max)); got != tc.want {
				t.Errorf("CheckRawSensor(%d, %d, %d) = %q, want %q", tc.w, tc.h, tc.max, got, tc.want)
			}
		})
	}
}
