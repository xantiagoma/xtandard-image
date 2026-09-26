package video

import "github.com/xantiagoma/xtandard-image/internal/transform"

// MaxCompositeSide bounds each side of a sprite or animation frame, the
// largest frame FFmpeg scales to.
const MaxCompositeSide = 16384

// Composite returns the composite a request's video options ask for:
// video_thumbnail_tile when set (it wins over video_thumbnail_animation, as
// a sprite is one image), else video_thumbnail_animation; ok is false for
// a plain thumbnail.
func Composite(vo *transform.VideoOptions) (CompositeOptions, bool) {
	switch {
	case vo == nil:
		return CompositeOptions{}, false
	case vo.Tile != nil:
		t := vo.Tile
		return CompositeOptions{
			Tile: true, Step: t.Step, Columns: t.Columns, Rows: t.Rows,
			Width: t.TileWidth, Height: t.TileHeight,
			Extend: t.ExtendTile, Trim: t.Trim, Fill: t.Fill, FocusX: t.FocusX, FocusY: t.FocusY,
		}, true
	case vo.Animation != nil:
		a := vo.Animation
		return CompositeOptions{
			Step: a.Step, Frames: a.Frames, Delay: a.Delay,
			Width: a.FrameWidth, Height: a.FrameHeight,
			Extend: a.ExtendFrame, Trim: a.Trim, Fill: a.Fill, FocusX: a.FocusX, FocusY: a.FocusY,
		}, true
	default:
		return CompositeOptions{}, false
	}
}

// RequestLayout is the layout of the composite req's first pipeline asks
// for, on a video described by m; ok is false for a plain thumbnail.
func RequestLayout(req *transform.Request, m *Meta, maxFrames int) (Layout, bool) {
	if req == nil || len(req.Pipelines) == 0 || m == nil {
		return Layout{}, false
	}
	o, ok := Composite(req.Pipelines[0].Video)
	if !ok {
		return Layout{}, false
	}
	w, h := m.UprightSize()
	return NewLayout(&o, w, h, m.Duration, maxFrames, MaxCompositeSide), true
}

// UprightSize is the display size with the display matrix applied: the size
// of a frame as shown.
func (m *Meta) UprightSize() (int, int) {
	if m.Rotation%180 != 0 {
		return m.DisplayHeight, m.DisplayWidth
	}
	return m.DisplayWidth, m.DisplayHeight
}
