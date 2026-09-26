package encode

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Config is the encoder part of the config tree (CONFIG.md "Processing,
// formats, limits"); internal/app maps config.Config.Formats onto it.
type Config struct {
	// Quality is the global default quality (XI_FORMATS_QUALITY).
	Quality int
	// FormatQuality holds the per-format default qualities
	// (XI_FORMATS_<FMT>_QUALITY, imgproxy IMGPROXY_FORMAT_QUALITY). A missing
	// or zero entry falls back to Quality.
	FormatQuality map[transform.Format]int

	// JPEGProgressive writes progressive JPEGs (XI_FORMATS_JPEG_PROGRESSIVE).
	JPEGProgressive bool
	// PNGQuantize writes palette PNGs (XI_FORMATS_PNG_QUANTIZE).
	PNGQuantize bool

	// Efforts on each encoder's native scale (XI_FORMATS_<FMT>_EFFORT):
	// WebP 0–6, AVIF 0–9, JPEG XL 1–10, GIF 1–10.
	WebPEffort int
	AVIFEffort int
	JXLEffort  int
	GIFEffort  int
	// AVIFLargePixels and AVIFLargeEffort bound AVIF latency: above
	// AVIFLargePixels the configured effort is capped at AVIFLargeEffort
	// unless the request set an effort (FORMATS §5). 0 pixels disables it.
	AVIFLargePixels int64
	AVIFLargeEffort int

	// HEICEncode allows HEIC output (XI_FORMATS_HEIC_ENCODE). Without it a
	// HEIC target fails with errs.KindUnsupported. vips.Config.HEICEncode must
	// match.
	HEICEncode bool
}

// DefaultConfig returns the documented defaults (CONFIG.md, FORMATS §5).
func DefaultConfig() Config {
	return Config{
		Quality: 80,
		FormatQuality: map[transform.Format]int{
			transform.FormatJPEG: 80,
			transform.FormatWebP: 80,
			transform.FormatAVIF: 60,
			transform.FormatHEIC: 60,
			transform.FormatJXL:  75,
		},
		JPEGProgressive: true,
		WebPEffort:      4,
		AVIFEffort:      4,
		JXLEffort:       5,
		GIFEffort:       7,
		AVIFLargePixels: 4_000_000,
		AVIFLargeEffort: 2,
	}
}

// Validate reports every invalid field.
func (c *Config) Validate() error {
	var errList []error
	if c.Quality < 1 || c.Quality > 100 {
		errList = append(errList, fmt.Errorf("encode: quality %d is not in 1..100", c.Quality))
	}
	for _, f := range slices.Sorted(maps.Keys(c.FormatQuality)) {
		if q := c.FormatQuality[f]; q < 0 || q > 100 {
			errList = append(errList, fmt.Errorf("encode: %s quality %d is not in 0..100", f, q))
		}
	}
	for _, r := range []struct {
		name         string
		v, low, high int
	}{
		{"webp effort", c.WebPEffort, 0, 6},
		{"avif effort", c.AVIFEffort, 0, 9},
		{"avif large-image effort", c.AVIFLargeEffort, 0, 9},
		{"jxl effort", c.JXLEffort, 1, 10},
		{"gif effort", c.GIFEffort, 1, 10},
	} {
		if r.v < r.low || r.v > r.high {
			errList = append(errList, fmt.Errorf("encode: %s %d is not in %d..%d", r.name, r.v, r.low, r.high))
		}
	}
	if c.AVIFLargePixels < 0 {
		errList = append(errList, errors.New("encode: avif large-image pixels must not be negative"))
	}
	return errors.Join(errList...)
}
