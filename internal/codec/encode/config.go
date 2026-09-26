package encode

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
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

	// AutoQuality is the configured auto-quality (XI_FORMATS_AUTOQUALITY_*).
	AutoQuality AutoQualityConfig

	// Best configures format=best (XI_FORMATS_BEST_*, EncodeBest).
	Best BestConfig

	// Video configures MP4, WebM and HEVC output (WP-76, XI_VIDEO_*).
	Video VideoConfig
}

// VideoConfig configures video output (FORMATS 5.1).
type VideoConfig struct {
	// Enabled allows video output (XI_VIDEO_ENABLED resolved against the
	// build); false answers errs.KindUnsupported.
	Enabled bool
	// Encoders are the encoders of this build (codec.VideoEncoders, probed
	// at startup): they decide the codec of mp4.
	Encoders video.EncoderSet
	// Settings are the CRF, speed and limit defaults (XI_VIDEO_ENCODE_*).
	Settings video.EncodeSettings
}

// BestConfig configures the best-format selection (FORMATS §6.3).
type BestConfig struct {
	// MaxPixels skips the selection above this many output pixels (all
	// frames): the negotiated format is encoded instead
	// (XI_FORMATS_BEST_MAX_PIXELS). 0 = no cap.
	MaxPixels int64
	// ComplexityThreshold separates graphics from photos
	// (negotiate.Analyze; XI_FORMATS_BEST_COMPLEXITY_THRESHOLD). 0 skips
	// the analysis: every image is tried as a photo.
	ComplexityThreshold float64
}

// AutoQualityConfig is the configured quality search (imgproxy
// IMGPROXY_AUTOQUALITY_*). A request's autoquality option overrides each
// field it sets.
type AutoQualityConfig struct {
	// Method is AutoQualityNone (the zero AutoQualityUnset means the same),
	// AutoQualitySize or AutoQualityDSSIM. ML is not implemented.
	Method transform.AutoQualityMethod
	// Target is the DSSIM target of the dssim method, or the byte budget of
	// the size method.
	Target float64
	// Min and Max bound the searched quality.
	Min, Max int
	// AllowedError is how far from Target a DSSIM may land and still stop
	// the search.
	AllowedError float64
	// FormatTarget, FormatMin and FormatMax override Target, Min and Max per
	// output format (XI_FORMATS_AUTOQUALITY_FORMAT_*).
	FormatTarget         map[transform.Format]float64
	FormatMin, FormatMax map[transform.Format]int
}

// DefaultAutoQuality returns the documented auto-quality defaults (CONFIG.md):
// off, DSSIM target 0.02 ± 0.001, quality 60–85, with JPEG and WebP searched
// in 40–90 and AVIF in 30–80.
func DefaultAutoQuality() AutoQualityConfig {
	return AutoQualityConfig{
		Method:       transform.AutoQualityNone,
		Target:       0.02,
		Min:          60,
		Max:          85,
		AllowedError: 0.001,
		FormatMin: map[transform.Format]int{
			transform.FormatJPEG: 40, transform.FormatWebP: 40, transform.FormatAVIF: 30,
		},
		FormatMax: map[transform.Format]int{
			transform.FormatJPEG: 90, transform.FormatWebP: 90, transform.FormatAVIF: 80,
		},
	}
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
		AutoQuality:     DefaultAutoQuality(),
		Best:            BestConfig{MaxPixels: 8_000_000, ComplexityThreshold: 5.5},
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
	if c.Best.MaxPixels < 0 {
		errList = append(errList, errors.New("encode: best max pixels must not be negative"))
	}
	if !(c.Best.ComplexityThreshold >= 0) {
		errList = append(errList, fmt.Errorf("encode: best complexity threshold %v must be >= 0", c.Best.ComplexityThreshold))
	}
	errList = append(errList, c.AutoQuality.validate()...)
	return errors.Join(errList...)
}

func (c *AutoQualityConfig) validate() []error {
	var errList []error
	switch c.Method {
	case transform.AutoQualityUnset, transform.AutoQualityNone, transform.AutoQualitySize, transform.AutoQualityDSSIM:
	default:
		errList = append(errList, fmt.Errorf("encode: autoquality method %q is not supported", c.Method.String()))
	}
	if !(c.Target > 0) {
		errList = append(errList, fmt.Errorf("encode: autoquality target %v must be > 0", c.Target))
	}
	if c.Method == transform.AutoQualitySize && c.Target < 1 {
		errList = append(errList, fmt.Errorf("encode: autoquality size target %v must be a byte count >= 1", c.Target))
	}
	if !(c.AllowedError >= 0 && c.AllowedError <= 1) {
		errList = append(errList, fmt.Errorf("encode: autoquality allowed error %v is not in 0..1", c.AllowedError))
	}
	bounds := func(name string, lo, hi int) {
		if lo < 1 || lo > 100 || hi < 1 || hi > 100 || lo > hi {
			errList = append(errList, fmt.Errorf("encode: autoquality %s bounds %d..%d are not ordered within 1..100", name, lo, hi))
		}
	}
	bounds("global", c.Min, c.Max)
	formats := slices.Sorted(maps.Keys(c.FormatMin))
	for _, f := range slices.Sorted(maps.Keys(c.FormatMax)) {
		if !slices.Contains(formats, f) {
			formats = append(formats, f)
		}
	}
	for _, f := range formats {
		lo, hi := c.Min, c.Max
		if v := c.FormatMin[f]; v > 0 {
			lo = v
		}
		if v := c.FormatMax[f]; v > 0 {
			hi = v
		}
		bounds(f.String(), lo, hi)
	}
	for _, f := range slices.Sorted(maps.Keys(c.FormatTarget)) {
		if !(c.FormatTarget[f] > 0) {
			errList = append(errList, fmt.Errorf("encode: autoquality %s target %v must be > 0", f, c.FormatTarget[f]))
		}
	}
	return errList
}
