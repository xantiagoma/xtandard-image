package video

import (
	"math"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Overlays (WP-124, ADR 0012 §3). An image or text overlay reaches FFmpeg
// as RGBA pixels rendered by libvips (internal/pipeline/exec.RenderOverlay)
// and their rectangle; a timecode as a drawtext filter with a fixed
// expansion. The shim builds the compositing graph itself (one buffer
// source and one overlay filter per image, a drawtext per timecode, after
// the stream's chain), with every option set as an AVOption value: the
// timeline expressions below are written here from numbers and a fixed
// vocabulary, like the chains, and no text of a request is ever among
// them.

// OverlayPlan is one overlay of the video stream, drawn in order over the
// output of the stream's chain.
type OverlayPlan struct {
	// Index is the overlay's index in transform.VideoOp.Overlays.
	Index int
	// Timecode is the drawtext of a timecode overlay; nil for an image or
	// text overlay, drawn from rendered pixels.
	Timecode *TimecodePlan
	// X, Y, Width and Height place the rendered pixels on the output frame
	// (Place); zero for a timecode.
	X, Y, Width, Height int
	// Start and End are the window on the output timeline in seconds; End
	// 0 runs to the end.
	Start, End float64
	// FadeIn fades the overlay in over FadeIn seconds from Start;
	// FadeOut fades it out over FadeOut seconds ending at FadeEnd (End, or
	// the output's length).
	FadeIn, FadeOut, FadeEnd float64
	// Enable is the overlay's timeline expression ("between(t,1,3)",
	// "gte(t,1)"); "" draws it on every frame.
	Enable string
}

// Image reports an overlay drawn from rendered pixels.
func (o *OverlayPlan) Image() bool { return o.Timecode == nil }

// Place sets the rectangle of the rendered pixels.
func (o *OverlayPlan) Place(x, y, w, h int) {
	o.X, o.Y, o.Width, o.Height = x, y, w, h
}

// TimecodePlan is the drawtext filter of a timecode overlay: options
// written from numbers and constants only.
type TimecodePlan struct {
	// Text is the fixed expansion of the format ("%{pts:hms}").
	Text string
	// Size is the font size in pixels.
	Size int
	// Color is the font colour, "0xRRGGBB".
	Color string
	// X and Y are the position expressions (tw and th are the text's
	// size, w and h the frame's).
	X, Y string
	// Alpha is the opacity expression (the fades included).
	Alpha string
	// Shadow is the shadow's offset in pixels; 0 is none.
	Shadow int
}

// timecodeText is the drawtext expansion of each format: constants, the
// only text a timecode ever has.
func timecodeText(t transform.VideoTimecode) string {
	switch t {
	case transform.VideoTimecodeHMS:
		return "%{pts:hms}"
	case transform.VideoTimecodeSeconds:
		return "%{pts:flt}"
	default:
		return ""
	}
}

// planOverlays plans op's overlays for a w × h output of duration seconds
// (0 when unknown): the time windows clamped to the output, the
// expressions, and the timecodes' drawtext options. An overlay with no
// opacity, or whose window starts after the output ends, is left out.
func planOverlays(op *transform.VideoOp, w, h int, duration float64) ([]OverlayPlan, error) {
	var out []OverlayPlan
	for i := range op.Overlays {
		o := &op.Overlays[i]
		if o.Opacity <= 0 {
			continue
		}
		start, end := o.Start.Seconds(), o.End.Seconds()
		if duration > 0 {
			if start >= duration {
				continue
			}
			if end >= duration {
				end = 0 // to the end
			}
		}
		p := OverlayPlan{Index: i, Start: start, End: end, FadeIn: o.FadeIn.Seconds()}
		p.FadeEnd = end
		if p.FadeEnd == 0 {
			p.FadeEnd = duration
		}
		if o.FadeOut > 0 && p.FadeEnd > start {
			p.FadeOut = min(o.FadeOut.Seconds(), p.FadeEnd-start)
		}
		if p.FadeOut == 0 {
			p.FadeEnd = 0
		}
		switch {
		case start > 0 && end > 0:
			p.Enable = "between(t," + ftoa(start) + "," + ftoa(end) + ")"
		case end > 0:
			p.Enable = "lte(t," + ftoa(end) + ")"
		case start > 0:
			p.Enable = "gte(t," + ftoa(start) + ")"
		}
		if o.Timecode != transform.VideoTimecodeNone {
			tc, err := planTimecode(o, &p, w, h)
			if err != nil {
				return nil, err
			}
			p.Timecode = tc
		}
		out = append(out, p)
	}
	return out, nil
}

// planTimecode writes the drawtext options of a timecode overlay.
func planTimecode(o *transform.VideoOverlay, p *OverlayPlan, w, h int) (*TimecodePlan, error) {
	text := timecodeText(o.Timecode)
	if text == "" {
		return nil, errs.Newf(errs.KindInternal, "video: timecode %s", o.Timecode)
	}
	tc := &TimecodePlan{Text: text, Size: o.TimecodeSize, Color: "0xffffff"}
	if tc.Size <= 0 {
		tc.Size = transform.DefaultTimecodeSize
	}
	alpha := o.Opacity
	if c := o.Color; c != nil {
		tc.Color = "0x" + hex2(c.R) + hex2(c.G) + hex2(c.B)
		alpha *= float64(c.A) / 255
	}
	if o.Shadow > 0 {
		tc.Shadow = max(int(math.Round(o.Shadow)), 1)
	}
	ox, oy := offsetPx(o.Position.XOffset, w), offsetPx(o.Position.YOffset, h)
	g := o.Position.Type
	switch g {
	case transform.GravityWest, transform.GravityNorthWest, transform.GravitySouthWest:
		tc.X = itoa(ox)
	case transform.GravityEast, transform.GravityNorthEast, transform.GravitySouthEast:
		tc.X = "w-tw-" + paren(ox)
	default:
		tc.X = "(w-tw)/2+" + paren(ox)
	}
	switch g {
	case transform.GravityNorth, transform.GravityNorthWest, transform.GravityNorthEast:
		tc.Y = itoa(oy)
	case transform.GravitySouth, transform.GravitySouthWest, transform.GravitySouthEast:
		tc.Y = "h-th-" + paren(oy)
	default:
		tc.Y = "(h-th)/2+" + paren(oy)
	}
	tc.Alpha = ftoa(min(max(alpha, 0), 1))
	var fades []string
	if p.FadeIn > 0 {
		fades = append(fades, "(t-"+ftoa(p.Start)+")/"+ftoa(p.FadeIn))
	}
	if p.FadeOut > 0 {
		fades = append(fades, "("+ftoa(p.FadeEnd)+"-t)/"+ftoa(p.FadeOut))
	}
	switch len(fades) {
	case 1:
		tc.Alpha += "*clip(" + fades[0] + ",0,1)"
	case 2:
		tc.Alpha += "*clip(min(" + fades[0] + "," + fades[1] + "),0,1)"
	}
	return tc, nil
}

// offsetPx is an overlay offset in pixels: absolute when |v| ≥ 1, else a
// fraction of size (imgproxy's watermark offsets).
func offsetPx(v float64, size int) int {
	if math.Abs(v) < 1 {
		v *= float64(size)
	}
	return int(math.Round(v))
}

// paren writes n, in parentheses when negative.
func paren(n int) string {
	if n < 0 {
		return "(" + itoa(n) + ")"
	}
	return itoa(n)
}

// checkOverlays verifies what the shim relies on: image overlays placed
// inside the w × h frame, at most transform.MaxVideoOverlays.
func checkOverlays(ovs []OverlayPlan, w, h int) error {
	if len(ovs) > transform.MaxVideoOverlays {
		return errs.Newf(errs.KindInternal, "video: %d overlays", len(ovs))
	}
	for i := range ovs {
		o := &ovs[i]
		if o.Image() && (o.Width <= 0 || o.Height <= 0 || o.X < 0 || o.Y < 0 || o.X+o.Width > w || o.Y+o.Height > h) {
			return errs.Newf(errs.KindInternal, "video: overlay %d of %dx%d at %d,%d on a %dx%d frame",
				o.Index, o.Width, o.Height, o.X, o.Y, w, h)
		}
	}
	return nil
}

// CheckOverlays verifies the placed overlays of v: the transcoder calls it
// before anything reaches C.
func (v *VideoStreamPlan) CheckOverlays() error { return checkOverlays(v.Overlays, v.Width, v.Height) }
