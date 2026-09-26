package encode

import (
	"github.com/xantiagoma/xtandard-image/internal/negotiate"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Target is what one Encode call produces.
type Target struct {
	// Format is the output format (negotiate.Decision.Format).
	Format transform.Format
	// Quality is the request's quality (transform.Output.Quality); 0 = unset.
	Quality int
	// FormatQuality is the request's format_quality; it applies when Quality
	// is unset.
	FormatQuality map[transform.Format]int
	// Options are the request's encoder options; unset fields take the
	// Config defaults.
	Options Options
	// Metadata is the metadata policy.
	Metadata Metadata
	// Animated keeps every frame of a multi-frame image when the format can
	// hold an animation; otherwise only the first frame is encoded.
	Animated bool
	// MaxBytes bounds the output size (imgproxy max_bytes); 0 = no bound.
	MaxBytes int
	// AutoQuality is the request's autoquality option; nil or zero fields
	// take Config.AutoQuality.
	AutoQuality *transform.AutoQuality
}

// Options are the per-format encoder options of the request
// (transform.Output) plus the format decision's hints. Nil pointers and zero
// values mean "config default".
type Options struct {
	Lossless          *bool
	Progressive       *bool
	ChromaSubsampling transform.Subsampling
	// Effort is 1 (fastest) .. 9 (smallest), mapped onto each encoder's
	// scale; 0 = config default.
	Effort int
	JPEG   *transform.JPEGOptions
	PNG    *transform.PNGOptions
	WebP   *transform.WebPOptions
	AVIF   *transform.AVIFOptions

	// PreferLossless is negotiate.Decision.Lossless (graphics rows): used
	// only when neither Lossless nor a WebP compression is set.
	PreferLossless bool
	// Quantize is negotiate.Decision.Quantize: a palette PNG unless the
	// request's png_options say otherwise.
	Quantize bool
}

// Metadata is the metadata policy of FORMATS §4, after the config defaults
// were applied (transform.Request.ApplyDefaults).
type Metadata struct {
	// Strip removes EXIF, XMP, IPTC and other metadata (strip_metadata).
	Strip bool
	// KeepCopyright keeps the EXIF copyright tags when stripping
	// (keep_copyright).
	KeepCopyright bool
	// StripColorProfile drops the ICC profile (strip_color_profile).
	StripColorProfile bool
}

// NewTarget builds the Target of a request whose output was decided by d.
// animated says whether the processed image is an animation that should stay
// one (source animated and animation not disabled). The Output must have
// passed ApplyDefaults; nil metadata switches default to the documented
// config defaults (strip, keep copyright, strip profile).
func NewTarget(out *transform.Output, d *negotiate.Decision, animated bool) Target {
	return Target{
		Format:        d.Format,
		Quality:       out.Quality,
		FormatQuality: out.FormatQuality,
		Options: Options{
			Lossless:          out.Lossless,
			Progressive:       out.Progressive,
			ChromaSubsampling: out.ChromaSubsampling,
			Effort:            out.Effort,
			JPEG:              out.JPEG,
			PNG:               out.PNG,
			WebP:              out.WebP,
			AVIF:              out.AVIF,
			PreferLossless:    d.Lossless,
			Quantize:          d.Quantize,
		},
		Metadata: Metadata{
			Strip:             boolOr(out.StripMetadata, true),
			KeepCopyright:     boolOr(out.KeepCopyright, true),
			StripColorProfile: boolOr(out.StripColorProfile, true),
		},
		Animated:    animated,
		MaxBytes:    out.MaxBytes,
		AutoQuality: out.AutoQuality,
	}
}

// Body is encoded output: a *vips.Blob for the libvips savers, Go memory for
// BMP and ICO. Bytes is valid until the final Release.
type Body interface {
	Bytes() []byte
	Len() int
	Retain()
	Release()
}

// Result is an encoded image. The caller writes Blob.Bytes() and then calls
// Blob.Release().
type Result struct {
	Blob        Body
	Format      transform.Format
	ContentType string
	// Quality is the quality of the returned save; 0 for formats and modes
	// without one (PNG, GIF, BMP, ICO, lossless).
	Quality int
	// Attempts counts the saves made (1 unless max_bytes or auto-quality
	// searched).
	Attempts int
	// OverBudget is set when MaxBytes > 0 (or the size auto-quality
	// targets a byte count) and even the returned (smallest) output
	// exceeds it.
	OverBudget bool
	// AutoQuality reports the auto-quality search.
	AutoQuality AutoQualityResult
}

// goBody is a Body over Go memory (BMP and ICO output).
type goBody struct{ b []byte }

func (g *goBody) Bytes() []byte { return g.b }
func (g *goBody) Len() int      { return len(g.b) }
func (*goBody) Retain()         {}
func (*goBody) Release()        {}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
