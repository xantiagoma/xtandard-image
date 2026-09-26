package video

import (
	"math"
)

// Composite options (WP-75): imgproxy Pro's video_thumbnail_tile (a sprite
// sheet of frames) and video_thumbnail_animation (an animation of frames).
// Both sample frames every Step seconds and fit each into a cell; the
// layout (Layout) is pure arithmetic on the probed duration and display
// size, so the server (negotiation, limits), the planner and the executor
// all see the same sprite or animation.

// DefaultMaxCompositeFrames caps the frames a sprite or an animation
// decodes when no limit is configured (XI_VIDEO_MAX_ANIMATION_FRAMES).
const DefaultMaxCompositeFrames = 100

// minDelay is the shortest frame delay of a video animation, in
// milliseconds; browsers slow shorter GIF delays down to 100 ms anyway.
const minDelay = 20

// CompositeOptions is a request's video_thumbnail_tile or
// video_thumbnail_animation, in the terms both share.
type CompositeOptions struct {
	// Tile makes a sprite sheet of Columns × Rows cells; otherwise an
	// animation of Frames frames.
	Tile bool
	// Step is the time between frames in seconds; ≤ 0 spreads the frames
	// over the duration (duration / cells, or duration / frames).
	Step          float64
	Columns, Rows int
	Frames        int
	// Delay is the animation's frame delay in seconds; 0 plays the frames
	// at the pace they were sampled (Step), at least 20 ms.
	Delay float64
	// Width and Height are the cell each frame is fitted into (tile_width,
	// tile_height, frame_width, frame_height); 0 derives a side from the
	// other and the frame's aspect ratio, and both 0 keep the frame size.
	Width, Height int
	// Extend grows every cell to Width × Height on black, the frame
	// centred (extend_tile, extend_frame).
	Extend bool
	// Trim drops the cells (sprite) or frames (animation) the video is too
	// short to fill; otherwise they stay black.
	Trim bool
	// Fill covers the cell and crops what sticks out around FocusX,
	// FocusY (0..1) instead of fitting inside it.
	Fill           bool
	FocusX, FocusY float64
}

// Layout is a resolved composite: which timestamps to decode, the size
// each frame is scaled to and where it lands.
type Layout struct {
	Tile bool
	// Times are the timestamps to decode, in seconds, increasing.
	Times []float64
	// Columns and Rows are the sprite grid (1 × 1 for an animation).
	Columns, Rows int
	// Frames is the number of animation frames (black ones included when
	// not trimmed); 1 for a sprite.
	Frames int
	// ScaleWidth and ScaleHeight are the size a decoded frame (upright) is
	// scaled to.
	ScaleWidth, ScaleHeight int
	// CropX and CropY cut a CellWidth × CellHeight area out of the scaled
	// frame (fill); OffsetX and OffsetY place the scaled frame in the cell
	// (extend). The frame, cropped when filling, is at most the cell size.
	CropX, CropY     int
	OffsetX, OffsetY int
	// CellWidth and CellHeight are one sprite cell or animation frame.
	CellWidth, CellHeight int
	// Width and Height are the whole sprite, or one animation frame.
	Width, Height int
	// Delay is the animation frame delay in milliseconds.
	Delay int
}

// Cells is the number of cells of a sprite, or of frames of an animation.
func (l *Layout) Cells() int {
	if l.Tile {
		return l.Columns * l.Rows
	}
	return l.Frames
}

// Place is the cell a decoded frame i goes to: the top-left corner in the
// sprite (0, 0 for an animation frame).
func (l *Layout) Place(i int) (x, y int) {
	if !l.Tile || l.Columns <= 0 {
		return 0, 0
	}
	return (i % l.Columns) * l.CellWidth, (i / l.Columns) * l.CellHeight
}

// NewLayout resolves o for a video whose frames are width × height upright
// (display size, display matrix applied) and duration seconds long (≤ 0:
// unknown, sampled as if one second long). maxFrames caps the frames
// decoded (≤ 0: DefaultMaxCompositeFrames), and maxSide bounds each side
// of the result (≤ 0: none): a larger cell is shrunk to fit.
func NewLayout(o *CompositeOptions, width, height int, duration float64, maxFrames, maxSide int) Layout {
	width, height = max(width, 1), max(height, 1)
	if maxFrames <= 0 {
		maxFrames = DefaultMaxCompositeFrames
	}
	l := Layout{Tile: o.Tile, Columns: 1, Rows: 1, Frames: 1}
	cells := max(o.Frames, 1)
	if o.Tile {
		l.Columns, l.Rows = max(o.Columns, 1), max(o.Rows, 1)
		cells = l.Columns * l.Rows
	}
	cells = min(cells, maxFrames)
	if o.Tile && l.Columns*l.Rows > cells {
		// Capped: keep the columns, drop rows.
		l.Columns = min(l.Columns, cells)
		l.Rows = max(cells/l.Columns, 1)
		cells = l.Columns * l.Rows
	}

	d := duration
	if !(d > 0) || math.IsInf(d, 0) {
		d = 0
	}
	step := o.Step
	if !(step > 0) || math.IsInf(step, 0) {
		step = max(d, 1) / float64(cells)
	}
	for i := range cells {
		t := float64(i) * step
		if d > 0 && t >= d && i > 0 {
			break
		}
		l.Times = append(l.Times, t)
	}

	if o.Tile {
		if o.Trim && len(l.Times) < cells {
			n := len(l.Times)
			l.Columns = min(l.Columns, n)
			l.Rows = (n + l.Columns - 1) / l.Columns
		}
	} else {
		l.Frames = cells
		if o.Trim {
			l.Frames = len(l.Times)
		}
		delay := o.Delay
		if !(delay > 0) {
			delay = step
		}
		l.Delay = max(int(math.Round(delay*1000)), minDelay)
	}

	l.cell(o, width, height)
	if maxSide > 0 {
		gw, gh := l.Columns*l.CellWidth, l.Rows*l.CellHeight
		if gw > maxSide || gh > maxSide {
			f := min(float64(maxSide)/float64(gw), float64(maxSide)/float64(gh))
			shrunk := *o
			shrunk.Width = max(int(float64(l.CellWidth)*f), 1)
			shrunk.Height = max(int(float64(l.CellHeight)*f), 1)
			l.cell(&shrunk, width, height)
		}
	}
	l.Width, l.Height = l.Columns*l.CellWidth, l.Rows*l.CellHeight
	return l
}

// cell computes the scale, crop, offset and cell size of o's box for a
// width × height frame.
func (l *Layout) cell(o *CompositeOptions, width, height int) {
	bw, bh := o.Width, o.Height
	fw, fh := float64(width), float64(height)
	switch {
	case bw <= 0 && bh <= 0:
		bw, bh = width, height
	case bw <= 0:
		bw = max(int(math.Round(fw*float64(bh)/fh)), 1)
	case bh <= 0:
		bh = max(int(math.Round(fh*float64(bw)/fw)), 1)
	}
	l.CropX, l.CropY, l.OffsetX, l.OffsetY = 0, 0, 0, 0
	if o.Fill {
		s := max(float64(bw)/fw, float64(bh)/fh)
		l.ScaleWidth = max(int(math.Round(fw*s)), bw)
		l.ScaleHeight = max(int(math.Round(fh*s)), bh)
		l.CropX = focus(l.ScaleWidth, bw, o.FocusX)
		l.CropY = focus(l.ScaleHeight, bh, o.FocusY)
		l.CellWidth, l.CellHeight = bw, bh
		return
	}
	s := min(float64(bw)/fw, float64(bh)/fh)
	l.ScaleWidth = min(max(int(math.Round(fw*s)), 1), bw)
	l.ScaleHeight = min(max(int(math.Round(fh*s)), 1), bh)
	if o.Extend {
		l.CellWidth, l.CellHeight = bw, bh
		l.OffsetX, l.OffsetY = (bw-l.ScaleWidth)/2, (bh-l.ScaleHeight)/2
		return
	}
	l.CellWidth, l.CellHeight = l.ScaleWidth, l.ScaleHeight
}

// focus is the offset of a cell of size in a scaled side of total that
// centres the fraction f (0..1) as far as the edges allow.
func focus(total, size int, f float64) int {
	if total <= size {
		return 0
	}
	if math.IsNaN(f) {
		f = 0.5
	}
	x := int(math.Round(f*float64(total) - float64(size)/2))
	return min(max(x, 0), total-size)
}
