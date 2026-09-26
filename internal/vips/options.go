package vips

// LoadOptions configures Load. The zero value loads the first page with
// random access, fail_on=warning and no limits. Options a loader does not
// have are ignored (for example Shrink for PNG). Loading is always limited:
// libvips' "unlimited" is forced off for every loader that has it.
type LoadOptions struct {
	// Shrink is JPEG/UltraHDR shrink-on-load: 2, 4 or 8 (≤ 1 is off).
	Shrink int
	// Scale is WebP and SVG scale-on-load (≤ 0 is off).
	Scale float64
	// Density is the SVG and PDF rendering DPI (≤ 0 keeps the loader
	// default, 72).
	Density float64
	// Background is the PDF page background, one value per band on the
	// 0..255 scale (up to four: RGBA). Nil keeps the loader default, opaque
	// white; {0, 0, 0, 0} renders transparent. Other loaders ignore it.
	Background []float64
	// Page is the first page or frame to load.
	Page int
	// NPages is the number of pages or frames: -1 loads all of them as one
	// vertical strip with page-height, 0 is the loader default (one).
	NPages int
	// Access is the pixel access pattern (default AccessRandom).
	Access Access
	// FailOn is the decoder strictness (default FailOnWarning).
	FailOn FailOn
	// Autorotate applies and removes the EXIF orientation after loading,
	// frame by frame for animations. Rotating by 90° needs AccessRandom.
	Autorotate bool
	// HEIFThumbnail loads the embedded thumbnail of a HEIF/AVIF file.
	HEIFThumbnail bool
	// MaxPixels fails the load with errs.KindLimitExceeded when the loaded
	// width × height (all frames) exceeds it; 0 is no limit. Checked on the
	// header, before any pixel is decoded.
	MaxPixels int64
	// MaxFrames fails the load with errs.KindLimitExceeded when more frames
	// were loaded; 0 is no limit.
	MaxFrames int
}

// ThumbnailOptions configures Thumbnail (vips_thumbnail_buffer: load with
// shrink-on-load, colour-manage and resize in one step).
type ThumbnailOptions struct {
	// Width and Height bound the result; 0 means unbounded on that axis.
	Width, Height int
	// Size says whether the thumbnail may enlarge (default SizeBoth).
	Size Size
	// NoRotate skips the EXIF auto-rotation.
	NoRotate bool
	// Crop fills Width×Height exactly, cropping with this strategy;
	// InterestingNone (default) fits inside instead.
	Crop Interesting
	// Linear resizes in linear light.
	Linear bool
	// ImportProfile is the fallback input profile (a path or a built-in name
	// such as "srgb" or "cmyk"); empty uses the embedded profile only.
	ImportProfile string
	// ExportProfile converts to this output profile; empty keeps sRGB.
	ExportProfile string
	// Intent is the rendering intent for the profiles.
	Intent Intent
	// FailOn is the decoder strictness (default FailOnWarning).
	FailOn FailOn
	// Page and NPages select pages or frames, as in LoadOptions.
	Page   int
	NPages int
}

// CopyOptions are the header changes of Copy. Zero fields keep the input's
// value; InterpretationMultiband (0) cannot be requested.
type CopyOptions struct {
	Interpretation Interpretation
	XRes, YRes     float64 // pixels per millimetre
	PageHeight     int
}

// SharpenOptions are the vips_sharpen parameters (unsharp mask in LAB).
type SharpenOptions struct {
	Sigma  float64 // gaussian sigma
	X1     float64 // flat/jaggy threshold
	Y2     float64 // maximum brightening
	Y3     float64 // maximum darkening
	M1, M2 float64 // slope for flat and jaggy areas
}

// DefaultSharpenOptions returns the libvips defaults.
func DefaultSharpenOptions() SharpenOptions {
	return SharpenOptions{Sigma: 0.5, X1: 2, Y2: 10, Y3: 20, M1: 0, M2: 3}
}

// SaveOptions configures Save. Start from DefaultSaveOptions: the zero
// value of a field is an explicit setting, not "the libvips default".
type SaveOptions struct {
	// Keep selects the metadata to write (default KeepNone: strip all).
	Keep Keep
	JPEG JPEGOptions
	PNG  PNGOptions
	WebP WebPOptions
	GIF  GIFOptions
	HEIF HEIFOptions // AVIF (AV1) and HEIC (HEVC)
	JXL  JXLOptions
	TIFF TIFFOptions
}

// JPEGOptions are the jpegsave options. mozjpeg-only options (trellis,
// overshoot deringing, scan optimisation, quant tables) are not exposed:
// the base image ships libjpeg-turbo.
type JPEGOptions struct {
	Quality         int  // 1..100
	Interlace       bool // progressive
	OptimizeCoding  bool // optimised Huffman tables
	Subsample       Subsample
	RestartInterval int // MCUs between restart markers; 0 is none
}

// PNGOptions are the pngsave options.
type PNGOptions struct {
	Compression int // zlib level 0..9
	Interlace   bool
	Filter      PNGFilter
	Palette     bool    // quantise to an 8-bit palette
	Quality     int     // quantisation quality 0..100
	Dither      float64 // quantisation dither 0..1
	BitDepth    int     // 1, 2, 4, 8 or 16; 0 derives it from the image
	Effort      int     // quantisation effort 1..10
}

// WebPOptions are the webpsave options.
type WebPOptions struct {
	Quality        int // 0..100
	Lossless       bool
	NearLossless   bool
	Effort         int // 0..6
	SmartSubsample bool
	Preset         WebPPreset
	MinSize        bool // minimise size for animations (slow)
	KMin, KMax     int  // min/max frames between keyframes
	AlphaQuality   int  // 0..100
	Mixed          bool // mix lossy and lossless frames in animations
	SmartDeblock   bool
	Exact          bool // keep RGB values under transparent pixels
}

// GIFOptions are the gifsave options.
type GIFOptions struct {
	Dither               float64 // 0..1
	Effort               int     // 1..10
	BitDepth             int     // 1..8
	InterframeMaxError   float64 // 0..32
	Reuse                bool    // reuse the input palette
	InterpaletteMaxError float64 // 0..256
	Interlace            bool
	// KeepDuplicateFrames writes identical consecutive frames instead of
	// merging them into one with the summed delay.
	KeepDuplicateFrames bool
}

// HEIFOptions are the heifsave options for AVIF and HEIC. The compression
// (AV1 or HEVC) follows the Format passed to Save.
type HEIFOptions struct {
	Quality   int // 1..100
	Lossless  bool
	Effort    int // 0..9
	Subsample Subsample
	BitDepth  int // 8, 10 or 12; 0 derives it from the image
}

// JXLOptions are the jxlsave options.
type JXLOptions struct {
	Quality  int     // 0..100, mapped to a distance by libjxl
	Distance float64 // butteraugli distance 0..25; > 0 overrides Quality
	Effort   int     // 1..10
	Lossless bool
	Tier     int // decode speed tier 0..4
}

// TIFFOptions are the tiffsave options.
type TIFFOptions struct {
	Quality     int // JPEG/WebP compression quality
	Compression TIFFCompression
	Predictor   TIFFPredictor
	Lossless    bool // WebP compression: lossless
	Level       int  // deflate 1..9 / zstd 1..22; 0 is the default
	BitDepth    int  // 1, 2, 4 or 8; 0 derives it from the image
}

// DefaultSaveOptions returns the libvips defaults of every saver, metadata
// stripped. Encoders (WP-30) override from config and the request.
func DefaultSaveOptions() SaveOptions {
	return SaveOptions{
		Keep: KeepNone,
		JPEG: JPEGOptions{Quality: 75, Subsample: SubsampleAuto},
		PNG: PNGOptions{
			Compression: 6, Filter: PNGFilterNone, Quality: 100, Dither: 1, Effort: 7,
		},
		WebP: WebPOptions{
			Quality: 75, Effort: 4, Preset: WebPPresetDefault,
			KMin: maxInt32 - 1, KMax: maxInt32, AlphaQuality: 100,
		},
		GIF:  GIFOptions{Dither: 1, Effort: 7, BitDepth: 8, InterpaletteMaxError: 3},
		HEIF: HEIFOptions{Quality: 50, Effort: 4, Subsample: SubsampleAuto},
		JXL:  JXLOptions{Quality: 75, Effort: 7},
		TIFF: TIFFOptions{
			Quality: 75, Compression: TIFFCompressionNone, Predictor: TIFFPredictorHorizontal,
		},
	}
}

const maxInt32 = 1<<31 - 1
