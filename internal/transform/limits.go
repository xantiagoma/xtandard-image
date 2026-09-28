package transform

import (
	"fmt"
	"strconv"
)

// SourceLimits are the effective pixel limits on the source of one request
// (XI_LIMITS_*, lowered or raised by LimitOverrides). Zero means unlimited
// unless stated otherwise. The probe (codec, server) and the planner share
// them so all apply the same rule and answer with the same message.
type SourceLimits struct {
	// MaxSrcPixels caps width × height × frames loaded
	// (XI_LIMITS_MAX_SRC_PIXELS). For an animation processed as one it
	// applies only while MaxFramePixels is off, as in imgproxy.
	MaxSrcPixels int64
	// MaxFramePixels caps width × height of one frame of an animation
	// processed as one (XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS). When set it
	// replaces MaxSrcPixels for those animations (imgproxy's
	// IMGPROXY_MAX_ANIMATION_FRAME_RESOLUTION); zero turns it off and the
	// animation counts all its frames against MaxSrcPixels.
	MaxFramePixels int64
	// MaxAnimationPixels caps width × height × frames of an animation that
	// MaxFramePixels checks instead of MaxSrcPixels
	// (XI_LIMITS_MAX_ANIMATION_PIXELS; not in imgproxy): it keeps the
	// decoded frames bounded when the total no longer is.
	MaxAnimationPixels int64
}

// LimitError is a source over a limit. transform is a leaf (it cannot
// import errs): callers wrap it as errs.KindLimitExceeded with Public as
// the client message.
type LimitError struct {
	// Public says what the source has, which limit it exceeds (by its
	// documented variable) and both numbers. It holds nothing internal.
	Public string
}

// Error implements error.
func (e *LimitError) Error() string { return e.Public }

// CheckDimensions is imgproxy's security.CheckDimensions on the frames
// actually loaded: frames > 1 is an animation processed as one. It returns
// nil when the source fits.
func (l *SourceLimits) CheckDimensions(width, height, frames int) *LimitError {
	frames = max(frames, 1)
	frame := int64(width) * int64(height)
	total := frame * int64(frames)
	if frames > 1 && l.MaxFramePixels > 0 {
		if frame > l.MaxFramePixels {
			return limitError(frameSize(width, height), frame, "XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS", l.MaxFramePixels)
		}
		if l.MaxAnimationPixels > 0 && total > l.MaxAnimationPixels {
			return limitError(framesSize(width, height, frames), total, "XI_LIMITS_MAX_ANIMATION_PIXELS", l.MaxAnimationPixels)
		}
		return nil
	}
	if l.MaxSrcPixels > 0 && total > l.MaxSrcPixels {
		what := sourceSize(width, height)
		if frames > 1 {
			what = framesSize(width, height, frames)
		}
		return limitError(what, total, "XI_LIMITS_MAX_SRC_PIXELS", l.MaxSrcPixels)
	}
	return nil
}

// CheckFrame is the check the probe can make from a header, before the
// output format and the frames to load are known: one frame must fit a
// limit that could apply to it. A source that may be processed as an
// animation (animated) passes when its frame fits MaxFramePixels, even if
// it is larger than MaxSrcPixels; a still, or an animation whose first
// frame is processed alone, must fit MaxSrcPixels. The totals over all
// frames are the planner's CheckDimensions, on the frames it loads, before
// anything is decoded.
func (l *SourceLimits) CheckFrame(width, height int, animated bool) *LimitError {
	frame := int64(width) * int64(height)
	if l.MaxSrcPixels <= 0 || frame <= l.MaxSrcPixels {
		return nil
	}
	if animated && l.MaxFramePixels > 0 {
		if frame <= l.MaxFramePixels {
			return nil
		}
		// Too large as a still and as an animation frame: name the limit
		// of the larger allowance, the one the source would need raised.
		if l.MaxFramePixels > l.MaxSrcPixels {
			return limitError(frameSize(width, height), frame, "XI_LIMITS_MAX_ANIMATION_FRAME_PIXELS", l.MaxFramePixels)
		}
	}
	what := sourceSize(width, height)
	if animated {
		what = frameSize(width, height)
	}
	return limitError(what, frame, "XI_LIMITS_MAX_SRC_PIXELS", l.MaxSrcPixels)
}

// CheckFrames rejects an animation with more frames than maxFrames
// (XI_LIMITS_MAX_ANIMATION_FRAMES). A limit of 1 disables animation (the
// first frame is processed, as in imgproxy) and ≤ 0 is unlimited, so
// neither rejects anything.
func CheckFrames(frames, maxFrames int) *LimitError {
	if maxFrames <= 1 || frames <= maxFrames {
		return nil
	}
	return &LimitError{Public: fmt.Sprintf("source has %d frames; limit XI_LIMITS_MAX_ANIMATION_FRAMES is %d", frames, maxFrames)}
}

func sourceSize(w, h int) string { return fmt.Sprintf("source is %d×%d", w, h) }

func frameSize(w, h int) string { return fmt.Sprintf("source frame is %d×%d", w, h) }

func framesSize(w, h, frames int) string {
	return fmt.Sprintf("source has %d frames of %d×%d", frames, w, h)
}

// limitError is the LimitError of a pixel limit, in megapixels unless
// rounding would make the numbers look equal.
func limitError(what string, got int64, name string, limit int64) *LimitError {
	g, l := megapixels(got), megapixels(limit)
	unit := " MP"
	if g == l {
		g, l, unit = strconv.FormatInt(got, 10), strconv.FormatInt(limit, 10), " pixels"
	}
	return &LimitError{Public: fmt.Sprintf("%s (%s%s); limit %s is %s%s", what, g, unit, name, l, unit)}
}

// megapixels formats px in megapixels rounded to one decimal, without a
// trailing ".0".
func megapixels(px int64) string {
	tenths := (px + 50_000) / 100_000
	if tenths%10 == 0 {
		return strconv.FormatInt(tenths/10, 10)
	}
	return strconv.FormatInt(tenths/10, 10) + "." + strconv.FormatInt(tenths%10, 10)
}
