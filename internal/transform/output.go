package transform

// Output controls encoding.
type Output struct {
	Format                Format // FormatUnset = source format or negotiated
	Quality               int    // 1..100; 0 = default for the format
	FormatQuality         map[Format]int
	SlowConnectionQuality int // quality when Save-Data or effective-connection-type hints are present (cloudflare scq, M2); 0 = config delta
	AutoQuality           *AutoQuality
	MaxBytes              int
	Lossless              *bool
	Progressive           *bool
	PreferJPEGIfOpaque    bool // thumbor autojpg()
	ChromaSubsampling     Subsampling
	Effort                int // 1 (fastest) .. 9 (smallest), normalised per encoder; 0 = format default
	JPEG                  *JPEGOptions
	PNG                   *PNGOptions
	WebP                  *WebPOptions
	AVIF                  *AVIFOptions
	StripMetadata         *bool // nil = config default
	KeepCopyright         *bool
	StripColorProfile     *bool
	ColorProfile          string // "srgb" | "cmyk" | file name
	PreserveHDR           *bool
	GainMap               GainMapMode // HDR gain maps: auto (follow preserve_hdr), hdr, sdr (WP-104)
	DPI                   int
	EnforceThumbnail      *bool
}

// AutoQuality is imgproxy's autoquality.
type AutoQuality struct {
	Method       AutoQualityMethod
	Target       float64
	Min, Max     int // quality bounds, 0 = config
	AllowedError float64
}

// JPEGOptions is imgproxy's jpeg_options. nil fields = config default.
type JPEGOptions struct {
	Progressive        *bool
	NoSubsample        *bool
	TrellisQuant       *bool
	OvershootDeringing *bool
	OptimizeScans      *bool
	QuantTable         *int // 0..8
}

// PNGOptions is imgproxy's png_options plus wsrv/imaginary compression knobs.
type PNGOptions struct {
	Interlaced         *bool
	Quantize           *bool
	QuantizationColors int  // 2..256; 0 = default
	Compression        *int // zlib level 0..9 (wsrv l, imaginary compression)
	AdaptiveFilter     *bool
}

// WebPOptions is imgproxy's webp_options.
type WebPOptions struct {
	Compression    WebPCompression
	SmartSubsample *bool
	Preset         WebPPreset
}

// AVIFOptions is imgproxy's avif_options. Subsample auto/on/off maps to
// SubsamplingAuto/Subsampling420/Subsampling444.
type AVIFOptions struct {
	Subsample Subsampling
}
