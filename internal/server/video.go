package server

import (
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// videoComposite turns the probed meta of a video source into that of the
// sprite (video_thumbnail_tile) or animation (video_thumbnail_animation)
// req asks for (video.RequestLayout, which the executor applies to the
// same probe): its size, frame count and animation flag, upright. The
// negotiation then sees an animation (so format=auto keeps it animated),
// and the input limits apply to what is decoded and assembled. A plain
// thumbnail, or an image, keeps m.
func videoComposite(req *transform.Request, m *SourceMeta, maxFrames int) {
	if m.Video == nil {
		return
	}
	l, ok := video.RequestLayout(req, m.Video, maxFrames)
	if !ok {
		return
	}
	m.Composite = true
	m.Width, m.Height = l.CellWidth, l.CellHeight
	m.Orientation = 1
	m.Frames, m.Pages, m.IsAnimated = 1, 1, false
	if l.Tile {
		m.Width, m.Height = l.Width, l.Height
		return
	}
	m.Frames, m.Pages = l.Frames, l.Frames
	m.IsAnimated = l.Frames > 1
}
