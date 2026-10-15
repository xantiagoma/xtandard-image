package app

import (
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/dialect/imgproxy"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// videoPresetsPath names the video presets in startup errors.
const videoPresetsPath = "video.transform.presets (XI_VIDEO_TRANSFORM_PRESETS)"

// videoPresets parses XI_VIDEO_TRANSFORM_PRESETS (WP-132) and checks every
// preset as the dialects will apply it: the /video option table (an
// unknown option or a bad value fails), then the output limits of
// XI_VIDEO_TRANSFORM_MAX_* (a preset that every request using it would
// exceed fails startup instead of answering 422). nil when none are
// defined.
func videoPresets(cfg *config.Config) (*preset.Store, error) {
	t := &cfg.Video.Transform
	if len(t.Presets) == 0 {
		return nil, nil
	}
	sep := cfg.Dialects.Imgproxy.ArgumentsSeparator
	store, err := preset.New(preset.Options{Definitions: t.Presets, Separator: sep})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", videoPresetsPath, err)
	}
	p, err := imgproxy.New(&imgproxy.Options{ArgumentsSeparator: sep, Strict: true, VideoPresets: store})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", videoPresetsPath, err)
	}
	for _, name := range store.Names() {
		op, _, err := p.VideoPresetOp([]string{name})
		if err != nil {
			return nil, fmt.Errorf("%s: preset %q: %w", videoPresetsPath, name, err)
		}
		if reason := overVideoLimits(op, t); reason != "" {
			return nil, fmt.Errorf("%s: preset %q: %s", videoPresetsPath, name, reason)
		}
	}
	return store, nil
}

// overVideoLimits reports what of op (a preset with the default preset
// applied) exceeds the output limits, or "".
func overVideoLimits(op *transform.VideoOp, t *config.VideoTransform) string {
	length := op.Duration
	if length == 0 && op.End > 0 {
		length = op.End - op.Start
	}
	switch {
	case op.Width > 0 && op.Height > 0 && int64(op.Width)*int64(op.Height) > t.MaxOutputPixels:
		return fmt.Sprintf("%d×%d is more than video.transform.max_output_pixels (%d)", op.Width, op.Height, t.MaxOutputPixels)
	case op.FPS > t.MaxFPS:
		return fmt.Sprintf("fps %g is more than video.transform.max_fps (%g)", op.FPS, t.MaxFPS)
	case length > t.MaxOutputDuration:
		return fmt.Sprintf("a cut of %s is longer than video.transform.max_output_duration (%s)", length, t.MaxOutputDuration)
	}
	return ""
}
