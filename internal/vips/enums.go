package vips

// Go mirrors of the libvips enums the API takes. The values equal the C
// ones (checked at compile time in enums_check.go), so the cgo wrappers
// convert with a plain cast.

// Kernel is VipsKernel, the resampling kernel of Resize.
type Kernel int

// Kernels.
const (
	KernelNearest Kernel = iota
	KernelLinear
	KernelCubic
	KernelMitchell
	KernelLanczos2
	KernelLanczos3
	KernelMKS2013
	KernelMKS2021
)

// Extend is VipsExtend, how Embed and Gravity fill new pixels.
type Extend int

// Extend modes.
const (
	ExtendBlack Extend = iota
	ExtendCopy
	ExtendRepeat
	ExtendMirror
	ExtendWhite
	ExtendBackground
)

// Interesting is VipsInteresting, the SmartCrop and Thumbnail crop
// strategy.
type Interesting int

// Crop strategies.
const (
	InterestingNone Interesting = iota
	InterestingCentre
	InterestingEntropy
	InterestingAttention
	InterestingLow
	InterestingHigh
	InterestingAll
)

// Size is VipsSize, whether Thumbnail may enlarge or shrink.
type Size int

// Size modes.
const (
	SizeBoth Size = iota
	SizeUp
	SizeDown
	SizeForce
)

// Angle is VipsAngle, a right-angle rotation for Rot.
type Angle int

// Angles, clockwise.
const (
	Angle0 Angle = iota
	Angle90
	Angle180
	Angle270
)

// Direction is VipsDirection for Flip and Join.
type Direction int

// Directions.
const (
	DirectionHorizontal Direction = iota
	DirectionVertical
)

// Compass is VipsCompassDirection, the anchor of Gravity.
type Compass int

// Compass anchors.
const (
	CompassCentre Compass = iota
	CompassNorth
	CompassEast
	CompassSouth
	CompassWest
	CompassNorthEast
	CompassSouthEast
	CompassSouthWest
	CompassNorthWest
)

// Intent is VipsIntent, the ICC rendering intent.
type Intent int

// Rendering intents.
const (
	IntentPerceptual Intent = 0
	IntentRelative   Intent = 1
	IntentSaturation Intent = 2
	IntentAbsolute   Intent = 3
	IntentAuto       Intent = 32
)

// Access is VipsAccess, the pixel access pattern a loader must support.
// Sequential is cheaper but only allows top-to-bottom reads; operations
// such as Rot, Trim, SmartCrop or a random crop need Random.
type Access int

// Access patterns.
const (
	AccessRandom Access = iota
	AccessSequential
)

// FailOn is the decoder strictness (VipsFailOn). Unlike libvips, the zero
// value is the strictest level, which untrusted input needs
// (ARCHITECTURE §8.2).
type FailOn int

// Strictness levels, strictest first.
const (
	FailOnWarning   FailOn = iota // any warning aborts (default)
	FailOnError                   // decoder errors abort
	FailOnTruncated               // only truncated input aborts
	FailOnNone                    // never abort
)

// BandFormat is VipsBandFormat, the numeric type of one band.
type BandFormat int

// Band formats.
const (
	BandFormatNotSet    BandFormat = -1
	BandFormatUchar     BandFormat = 0
	BandFormatChar      BandFormat = 1
	BandFormatUshort    BandFormat = 2
	BandFormatShort     BandFormat = 3
	BandFormatUint      BandFormat = 4
	BandFormatInt       BandFormat = 5
	BandFormatFloat     BandFormat = 6
	BandFormatComplex   BandFormat = 7
	BandFormatDouble    BandFormat = 8
	BandFormatDpcomplex BandFormat = 9
)

// Interpretation is VipsInterpretation, the colour space of the pixels.
type Interpretation int

// Interpretations.
const (
	InterpretationError     Interpretation = -1
	InterpretationMultiband Interpretation = 0
	InterpretationBW        Interpretation = 1
	InterpretationHistogram Interpretation = 10
	InterpretationXYZ       Interpretation = 12
	InterpretationLab       Interpretation = 13
	InterpretationCMYK      Interpretation = 15
	InterpretationLabQ      Interpretation = 16
	InterpretationRGB       Interpretation = 17
	InterpretationCMC       Interpretation = 18
	InterpretationLCh       Interpretation = 19
	InterpretationLabS      Interpretation = 21
	InterpretationSRGB      Interpretation = 22
	InterpretationYXY       Interpretation = 23
	InterpretationFourier   Interpretation = 24
	InterpretationRGB16     Interpretation = 25
	InterpretationGrey16    Interpretation = 26
	InterpretationMatrix    Interpretation = 27
	InterpretationScRGB     Interpretation = 28
	InterpretationHSV       Interpretation = 29
	InterpretationOKLab     Interpretation = 30
	InterpretationOKLCh     Interpretation = 31
)

// BlendMode is VipsBlendMode for Composite2.
type BlendMode int

// Blend modes (Porter-Duff and PDF separable/non-separable modes).
const (
	BlendClear BlendMode = iota
	BlendSource
	BlendOver
	BlendIn
	BlendOut
	BlendAtop
	BlendDest
	BlendDestOver
	BlendDestIn
	BlendDestOut
	BlendDestAtop
	BlendXor
	BlendAdd
	BlendSaturate
	BlendMultiply
	BlendScreen
	BlendOverlay
	BlendDarken
	BlendLighten
	BlendColourDodge
	BlendColourBurn
	BlendHardLight
	BlendSoftLight
	BlendDifference
	BlendExclusion
)

// Keep is VipsForeignKeep, the metadata a saver writes. The zero value
// strips everything (FORMATS.md §4 default).
type Keep int

// Metadata flags.
const (
	KeepNone    Keep = 0
	KeepEXIF    Keep = 1 << 0
	KeepXMP     Keep = 1 << 1
	KeepIPTC    Keep = 1 << 2
	KeepICC     Keep = 1 << 3
	KeepOther   Keep = 1 << 4
	KeepGainMap Keep = 1 << 5
	KeepAll          = KeepEXIF | KeepXMP | KeepIPTC | KeepICC | KeepOther | KeepGainMap
)

// Subsample is VipsForeignSubsample, chroma subsampling of JPEG and HEIF.
type Subsample int

// Subsample modes.
const (
	SubsampleAuto Subsample = iota // 4:2:0 unless quality >= 90
	SubsampleOn
	SubsampleOff
)

// PNGFilter is VipsForeignPngFilter, a bitmask of row filters.
type PNGFilter int

// PNG row filters.
const (
	PNGFilterNone  PNGFilter = 0x08
	PNGFilterSub   PNGFilter = 0x10
	PNGFilterUp    PNGFilter = 0x20
	PNGFilterAvg   PNGFilter = 0x40
	PNGFilterPaeth PNGFilter = 0x80
	PNGFilterAll   PNGFilter = 0xF8
)

// WebPPreset is VipsForeignWebpPreset.
type WebPPreset int

// WebP presets.
const (
	WebPPresetDefault WebPPreset = iota
	WebPPresetPicture
	WebPPresetPhoto
	WebPPresetDrawing
	WebPPresetIcon
	WebPPresetText
)

// TIFFCompression is VipsForeignTiffCompression.
type TIFFCompression int

// TIFF compressions.
const (
	TIFFCompressionNone TIFFCompression = iota
	TIFFCompressionJPEG
	TIFFCompressionDeflate
	TIFFCompressionPackbits
	TIFFCompressionCCITTFax4
	TIFFCompressionLZW
	TIFFCompressionWebP
	TIFFCompressionZstd
	TIFFCompressionJP2K
)

// TIFFPredictor is VipsForeignTiffPredictor.
type TIFFPredictor int

// TIFF predictors.
const (
	TIFFPredictorNone       TIFFPredictor = 1
	TIFFPredictorHorizontal TIFFPredictor = 2
	TIFFPredictorFloat      TIFFPredictor = 3
)

// MetaField names an image metadata item that the metadata methods read,
// set or remove.
type MetaField string

// Metadata fields (libvips names).
const (
	FieldEXIF        MetaField = "exif-data"
	FieldXMP         MetaField = "xmp-data"
	FieldIPTC        MetaField = "iptc-data"
	FieldICC         MetaField = "icc-profile-data"
	FieldOrientation MetaField = "orientation"
	FieldPageHeight  MetaField = "page-height"
	FieldNPages      MetaField = "n-pages"
	FieldLoop        MetaField = "loop"
	FieldDelay       MetaField = "delay"
	// FieldXOffset and FieldYOffset are the image's origin: an extracted
	// area (vips_extract_area, vips_smartcrop) has minus its left and top.
	FieldXOffset MetaField = "xoffset"
	FieldYOffset MetaField = "yoffset"
)
