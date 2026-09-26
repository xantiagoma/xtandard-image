package video_test

import (
	"math"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/video"
)

func TestSeek(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		second, duration float64
		want             float64
	}{
		{"start", 0, 10, 0},
		{"inside", 2.5, 10, 2.5},
		{"past the end", 12, 10, 10},
		{"from the end", -1.5, 10, 8.5},
		{"before the start", -20, 10, 0},
		{"unknown duration", 3, 0, 3},
		{"unknown duration, from the end", -3, 0, 0},
		{"nan", math.NaN(), 10, 0},
		{"inf", math.Inf(1), 10, 0},
		{"infinite duration", 4, math.Inf(1), 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := video.Seek(tc.second, tc.duration); got != tc.want {
				t.Errorf("Seek(%v, %v) = %v, want %v", tc.second, tc.duration, got, tc.want)
			}
		})
	}
}

func TestSamplePoints(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		duration float64
		span     time.Duration
		n        int
		want     []float64
	}{
		{"short video", 2, 10 * time.Second, 3, []float64{0.5, 1, 1.5}},
		{"span inside", 60, 10 * time.Second, 4, []float64{2, 4, 6, 8}},
		{"unknown duration", 0, 6 * time.Second, 2, []float64{2, 4}},
		{"defaults", 60, 0, 0, []float64{10.0 / 6, 20.0 / 6, 30.0 / 6, 40.0 / 6, 50.0 / 6}},
		{"capped", 33, 33 * time.Second, 1000, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := video.SamplePoints(tc.duration, tc.span, tc.n)
			if tc.want == nil {
				if len(got) != video.MaxSamples {
					t.Fatalf("%d points, want %d", len(got), video.MaxSamples)
				}
				return
			}
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateApprox(0, 1e-9)); diff != "" {
				t.Errorf("points (-want +got):\n%s", diff)
			}
		})
	}
}

// histogram builds a histogram from (value, count) pairs.
func histogram(pairs ...uint32) *video.Histogram {
	var h video.Histogram
	for i := 0; i+1 < len(pairs); i += 2 {
		h[pairs[i]] += pairs[i+1]
	}
	return &h
}

// ramp spreads n pixels evenly over [lo, hi].
func ramp(lo, hi int, n uint32) *video.Histogram {
	var h video.Histogram
	for v := lo; v <= hi; v++ {
		h[v] += n
	}
	return &h
}

func TestScore(t *testing.T) {
	t.Parallel()
	empty := new(video.Histogram)
	flat := histogram(128, 1000)
	black := histogram(2, 900, 40, 100)
	white := histogram(250, 950, 200, 50)
	busy := ramp(30, 220, 10)
	darkBusy := ramp(0, 60, 10)
	if s := video.Score(empty); s != 0 {
		t.Errorf("empty = %v, want 0", s)
	}
	if s := video.Score(flat); s != 0 {
		t.Errorf("flat = %v, want 0", s)
	}
	b, w, busyS, dark := video.Score(black), video.Score(white), video.Score(busy), video.Score(darkBusy)
	if !(busyS > dark && dark > b && busyS > w) {
		t.Errorf("busy %.3f, dark busy %.3f, near black %.3f, near white %.3f: want busy > dark busy > near black and busy > near white",
			busyS, dark, b, w)
	}
	if busyS != video.Score(ramp(30, 220, 10)) {
		t.Error("not deterministic")
	}
}

func TestOrientation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		rotation int
		hflip    bool
		want     int
	}{
		{0, false, 1},
		{90, false, 6},
		{180, false, 3},
		{270, false, 8},
		{-90, false, 8},
		{360, false, 1},
		{450, false, 6},
		{89, false, 6},
		{0, true, 2},
		{90, true, 7},
		{180, true, 4},
		{270, true, 5},
	}
	for _, tc := range tests {
		if got := video.Orientation(tc.rotation, tc.hflip); got != tc.want {
			t.Errorf("Orientation(%d, %v) = %d, want %d", tc.rotation, tc.hflip, got, tc.want)
		}
	}
	m := video.Meta{Rotation: 90}
	if m.Orientation() != 6 {
		t.Errorf("Meta.Orientation() = %d, want 6", m.Orientation())
	}
}

func TestSettingsRequest(t *testing.T) {
	t.Parallel()
	s := video.Settings{Enabled: true, Second: new(1.0), Keyframes: true, Samples: 3, Span: time.Second, Limits: video.Limits{MaxPixels: 9}}
	got := s.Request(nil, nil, 10, 20)
	want := video.FrameRequest{Second: new(1.0), Keyframes: true, Samples: 3, Span: time.Second, Width: 10, Height: 20, Limits: video.Limits{MaxPixels: 9}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("defaults (-want +got):\n%s", diff)
	}
	got = s.Request(new(-2.0), new(false), 0, 0)
	if *got.Second != -2 || got.Keyframes || got.Width != 0 {
		t.Errorf("overrides: %+v", got)
	}
	best := video.Settings{}
	if r := best.Request(nil, nil, 0, 0); r.Second != nil {
		t.Errorf("best frame: %+v", r)
	}
}
