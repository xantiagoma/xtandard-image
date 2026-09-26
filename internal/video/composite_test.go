package video_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

func TestNewLayout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		o         video.CompositeOptions
		w, h      int
		duration  float64
		maxFrames int
		maxSide   int
		want      video.Layout
	}{
		{
			name: "tile from the duration", w: 1280, h: 720, duration: 2,
			o: video.CompositeOptions{Tile: true, Step: -1, Columns: 2, Rows: 2, Width: 160, Height: 90},
			want: video.Layout{
				Tile: true, Times: []float64{0, 0.5, 1, 1.5}, Columns: 2, Rows: 2, Frames: 1,
				ScaleWidth: 160, ScaleHeight: 90, CellWidth: 160, CellHeight: 90, Width: 320, Height: 180,
			},
		},
		{
			name: "tile step past the end, untrimmed", w: 1280, h: 720, duration: 2,
			o: video.CompositeOptions{Tile: true, Step: 0.75, Columns: 3, Rows: 2, Width: 160},
			want: video.Layout{
				Tile: true, Times: []float64{0, 0.75, 1.5}, Columns: 3, Rows: 2, Frames: 1,
				ScaleWidth: 160, ScaleHeight: 90, CellWidth: 160, CellHeight: 90, Width: 480, Height: 180,
			},
		},
		{
			name: "tile trimmed", w: 1280, h: 720, duration: 2,
			o: video.CompositeOptions{Tile: true, Step: 0.4, Columns: 3, Rows: 4, Height: 90, Trim: true},
			want: video.Layout{
				Tile: true, Times: []float64{0, 0.4, 0.8, 1.2, 1.6}, Columns: 3, Rows: 2, Frames: 1,
				ScaleWidth: 160, ScaleHeight: 90, CellWidth: 160, CellHeight: 90, Width: 480, Height: 180,
			},
		},
		{
			name: "tile extended", w: 1280, h: 720, duration: 1,
			o: video.CompositeOptions{Tile: true, Columns: 1, Rows: 1, Width: 100, Height: 100, Extend: true},
			want: video.Layout{
				Tile: true, Times: []float64{0}, Columns: 1, Rows: 1, Frames: 1,
				ScaleWidth: 100, ScaleHeight: 56, OffsetY: 22, CellWidth: 100, CellHeight: 100, Width: 100, Height: 100,
			},
		},
		{
			name: "tile filled around the focus", w: 1280, h: 720, duration: 1,
			o: video.CompositeOptions{Tile: true, Columns: 1, Rows: 1, Width: 100, Height: 100, Fill: true, FocusX: 1, FocusY: 0.5},
			want: video.Layout{
				Tile: true, Times: []float64{0}, Columns: 1, Rows: 1, Frames: 1,
				ScaleWidth: 178, ScaleHeight: 100, CropX: 78, CellWidth: 100, CellHeight: 100, Width: 100, Height: 100,
			},
		},
		{
			name: "tile capped keeps the columns", w: 160, h: 90, duration: 10, maxFrames: 10,
			o: video.CompositeOptions{Tile: true, Columns: 4, Rows: 10},
			want: video.Layout{
				Tile: true, Times: []float64{0, 1.25, 2.5, 3.75, 5, 6.25, 7.5, 8.75}, Columns: 4, Rows: 2, Frames: 1,
				ScaleWidth: 160, ScaleHeight: 90, CellWidth: 160, CellHeight: 90, Width: 640, Height: 180,
			},
		},
		{
			name: "sprite shrunk to the side limit", w: 1280, h: 720, duration: 4, maxSide: 1000,
			o: video.CompositeOptions{Tile: true, Columns: 2, Rows: 1},
			want: video.Layout{
				Tile: true, Times: []float64{0, 2}, Columns: 2, Rows: 1, Frames: 1,
				ScaleWidth: 500, ScaleHeight: 281, CellWidth: 500, CellHeight: 281, Width: 1000, Height: 281,
			},
		},
		{
			name: "animation at the step's pace", w: 1280, h: 720, duration: 2,
			o: video.CompositeOptions{Step: 0.25, Frames: 4, Width: 320},
			want: video.Layout{
				Times: []float64{0, 0.25, 0.5, 0.75}, Columns: 1, Rows: 1, Frames: 4,
				ScaleWidth: 320, ScaleHeight: 180, CellWidth: 320, CellHeight: 180, Width: 320, Height: 180, Delay: 250,
			},
		},
		{
			name: "animation padded with black frames", w: 1280, h: 720, duration: 1,
			o: video.CompositeOptions{Step: 0.5, Frames: 4, Delay: 0.1, Width: 64},
			want: video.Layout{
				Times: []float64{0, 0.5}, Columns: 1, Rows: 1, Frames: 4,
				ScaleWidth: 64, ScaleHeight: 36, CellWidth: 64, CellHeight: 36, Width: 64, Height: 36, Delay: 100,
			},
		},
		{
			name: "animation trimmed, delay floor", w: 1280, h: 720, duration: 1,
			o: video.CompositeOptions{Step: 0.5, Frames: 4, Delay: 0.001, Width: 64, Trim: true},
			want: video.Layout{
				Times: []float64{0, 0.5}, Columns: 1, Rows: 1, Frames: 2,
				ScaleWidth: 64, ScaleHeight: 36, CellWidth: 64, CellHeight: 36, Width: 64, Height: 36, Delay: 20,
			},
		},
		{
			name: "unknown duration", w: 100, h: 100,
			o: video.CompositeOptions{Frames: 2},
			want: video.Layout{
				Times: []float64{0, 0.5}, Columns: 1, Rows: 1, Frames: 2,
				ScaleWidth: 100, ScaleHeight: 100, CellWidth: 100, CellHeight: 100, Width: 100, Height: 100, Delay: 500,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := video.NewLayout(&tc.o, tc.w, tc.h, tc.duration, tc.maxFrames, tc.maxSide)
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateApprox(0, 1e-9)); diff != "" {
				t.Errorf("layout (-want +got):\n%s", diff)
			}
			if n := got.Cells(); len(got.Times) > n {
				t.Errorf("%d timestamps for %d cells", len(got.Times), n)
			}
		})
	}
}

func TestLayoutPlace(t *testing.T) {
	t.Parallel()
	l := video.Layout{Tile: true, Columns: 3, Rows: 2, CellWidth: 10, CellHeight: 5}
	got := make([][2]int, 0, 6)
	for i := range 6 {
		x, y := l.Place(i)
		got = append(got, [2]int{x, y})
	}
	want := [][2]int{{0, 0}, {10, 0}, {20, 0}, {0, 5}, {10, 5}, {20, 5}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("places (-want +got):\n%s", diff)
	}
}

func TestRequestLayout(t *testing.T) {
	t.Parallel()
	m := &video.Meta{DisplayWidth: 1280, DisplayHeight: 720, Duration: 2, Rotation: 90}
	req := &transform.Request{Pipelines: []transform.Pipeline{{Video: &transform.VideoOptions{
		Tile:      &transform.VideoTile{Columns: 2, Rows: 1, TileHeight: 160, FocusX: 0.5, FocusY: 0.5},
		Animation: &transform.VideoAnimation{Frames: 3},
	}}}}
	l, ok := video.RequestLayout(req, m, 0)
	if !ok || !l.Tile {
		t.Fatalf("tile wins over animation: %+v %v", l, ok)
	}
	// Rotated 90°: the frame is 720×1280 upright.
	if l.CellWidth != 90 || l.CellHeight != 160 || l.Width != 180 {
		t.Errorf("cell %dx%d, sprite %dx%d; want 90x160 cells, 180x160", l.CellWidth, l.CellHeight, l.Width, l.Height)
	}
	req.Pipelines[0].Video.Tile = nil
	if l, ok := video.RequestLayout(req, m, 0); !ok || l.Tile || l.Frames != 3 {
		t.Errorf("animation: %+v %v", l, ok)
	}
	req.Pipelines[0].Video.Animation = nil
	if _, ok := video.RequestLayout(req, m, 0); ok {
		t.Error("a plain thumbnail has no layout")
	}
	if _, ok := video.RequestLayout(req, nil, 0); ok {
		t.Error("an image has no layout")
	}
}
