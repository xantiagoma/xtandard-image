package codec

import (
	"context"
	"image"
	"slices"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// CompositeRequest is a sprite (vtt) or an animation (vta) to assemble
// from a video's frames (WP-75).
type CompositeRequest struct {
	Options video.CompositeOptions
	// MaxFrames caps the frames decoded (XI_VIDEO_MAX_ANIMATION_FRAMES).
	MaxFrames int
	// Keyframes decodes the keyframe at or before each timestamp
	// (video_thumbnail_keyframes).
	Keyframes bool
	Limits    video.Limits
}

// CompositeInfo describes the frames a composite was made of.
type CompositeInfo struct {
	// Layout is the resolved composite (video.NewLayout on the probed
	// duration and upright display size).
	Layout video.Layout
	// Times are the timestamps of the decoded frames.
	Times []float64
}

// String renders c for the X-Xi-Video-Frame debug header: "tile 3x2" or
// "animation 10", the frames decoded and the first and last timestamps
// ("tile 3x2 6 frames 0.000-1.667s").
func (c *CompositeInfo) String() string {
	var b []byte
	if c.Layout.Tile {
		b = append(b, "tile "...)
		b = strconv.AppendInt(b, int64(c.Layout.Columns), 10)
		b = append(b, 'x')
		b = strconv.AppendInt(b, int64(c.Layout.Rows), 10)
	} else {
		b = append(b, "animation "...)
		b = strconv.AppendInt(b, int64(c.Layout.Frames), 10)
	}
	b = append(b, ' ')
	b = strconv.AppendInt(b, int64(len(c.Times)), 10)
	b = append(b, " frames"...)
	if len(c.Times) > 0 {
		b = append(b, ' ')
		b = strconv.AppendFloat(b, c.Times[0], 'f', 3, 64)
		b = append(b, '-')
		b = strconv.AppendFloat(b, c.Times[len(c.Times)-1], 'f', 3, 64)
		b = append(b, 's')
	}
	return string(b)
}

// VideoComposite decodes the frames of the layout of req.Options from a video source on
// one FFmpeg handle (sess when it reads buf, else a new one) and assembles
// them, upright, with libvips arrayjoin: a sprite of Columns × Rows cells
// (Layout.Width × Height), or an animation strip of Layout.Frames frames
// with page-height, delays and an infinite loop. Cells the video is too
// short to fill are black (transparent black with alpha). The images are
// tracked in a; the result is 8-bit sRGB, RGBA when the stream has alpha.
// It must run inside vips.Run.
func VideoComposite(ctx context.Context, a *vips.Arena, buf *vips.Buffer, sess *VideoSession, req *CompositeRequest) (*vips.Image, CompositeInfo, error) {
	s := sess.session(buf)
	if s == nil {
		fs, err := ffmpeg.Open(ctx, buf, req.Limits)
		if err != nil {
			return nil, CompositeInfo{}, err
		}
		defer fs.Close()
		s = fs
	}
	m := s.Meta()
	uw, uh := m.UprightSize()
	info := CompositeInfo{Layout: video.NewLayout(&req.Options, uw, uh, m.Duration, req.MaxFrames, video.MaxCompositeSide)}
	l := &info.Layout
	if len(l.Times) == 0 || l.CellWidth <= 0 || l.CellHeight <= 0 {
		return nil, CompositeInfo{}, errs.New(errs.KindInternal, "codec: empty video composite")
	}
	orient := m.Orientation()
	// The layout is upright; FFmpeg scales the stored frame.
	sw, sh := l.ScaleWidth, l.ScaleHeight
	if m.Rotation%180 != 0 {
		sw, sh = sh, sw
	}
	frames, err := s.Frames(ctx, &video.FramesRequest{
		Times: l.Times, Keyframes: req.Keyframes, Width: sw, Height: sh, Limits: req.Limits,
	})
	if err != nil {
		return nil, CompositeInfo{}, err
	}
	info.Times = make([]float64, 0, len(frames))
	bands := 3
	if m.Alpha {
		bands = 4
	}
	cells := make([]*vips.Image, 0, l.Cells())
	for _, fr := range frames {
		info.Times = append(info.Times, fr.Time)
		img, err := a.Add(vips.NewFromBuffer(ctx, fr.Pixels, fr.Width, fr.Height, fr.Bands, vips.BandFormatUchar))
		fr.Release() // the image holds its own reference
		if err != nil {
			releaseFrames(frames)
			return nil, CompositeInfo{}, err
		}
		cell, err := compositeCell(ctx, a, img, l, orient)
		if err != nil {
			releaseFrames(frames)
			return nil, CompositeInfo{}, err
		}
		cells = append(cells, cell)
	}
	if n := l.Cells() - len(cells); n > 0 {
		black, err := a.Add(vips.Black(ctx, l.CellWidth, l.CellHeight, bands))
		if err != nil {
			return nil, CompositeInfo{}, err
		}
		cells = append(cells, slices.Repeat([]*vips.Image{black}, n)...)
	}
	cells = cells[:l.Cells()]
	across := l.Columns
	if !l.Tile {
		across = 1
	}
	out, err := a.Add(vips.Arrayjoin(ctx, cells, across))
	if err != nil {
		return nil, CompositeInfo{}, err
	}
	if l.Tile {
		return out, info, nil
	}
	if out, err = a.Add(out.SetPageHeight(ctx, l.CellHeight)); err != nil {
		return nil, CompositeInfo{}, err
	}
	if out, err = a.Add(out.SetInt(ctx, vips.FieldNPages, l.Frames)); err != nil {
		return nil, CompositeInfo{}, err
	}
	if out, err = a.Add(out.SetInts(ctx, vips.FieldDelay, slices.Repeat([]int{l.Delay}, l.Frames))); err != nil {
		return nil, CompositeInfo{}, err
	}
	out, err = a.Add(out.SetInt(ctx, vips.FieldLoop, 0))
	return out, info, err
}

func releaseFrames(frames []*ffmpeg.Frame) {
	for _, f := range frames {
		f.Release()
	}
}

// compositeCell turns a decoded frame (stored orientation, ScaleWidth ×
// ScaleHeight once upright) into one cell: upright, cropped when filling,
// centred on black when extending.
func compositeCell(ctx context.Context, a *vips.Arena, img *vips.Image, l *video.Layout, orient int) (*vips.Image, error) {
	var err error
	if orient != 1 {
		if img, err = a.Add(img.SetOrientation(ctx, orient)); err != nil {
			return nil, err
		}
		if img, err = a.Add(img.Autorot(ctx)); err != nil {
			return nil, err
		}
	}
	if img.Width() > l.CellWidth || img.Height() > l.CellHeight {
		w, h := min(img.Width(), l.CellWidth), min(img.Height(), l.CellHeight)
		x, y := min(l.CropX, img.Width()-w), min(l.CropY, img.Height()-h)
		if img, err = a.Add(img.ExtractArea(ctx, image.Rect(x, y, x+w, y+h))); err != nil {
			return nil, err
		}
	}
	if img.Width() != l.CellWidth || img.Height() != l.CellHeight {
		if img, err = a.Add(img.Embed(ctx, l.OffsetX, l.OffsetY, l.CellWidth, l.CellHeight, vips.ExtendBlack, nil)); err != nil {
			return nil, err
		}
	}
	return img, nil
}
