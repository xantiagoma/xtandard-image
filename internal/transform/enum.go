package transform

import "strconv"

// EnumError reports a string that does not name a value of an enum. Parsers
// return it; dialects usually wrap it into a 400.
type EnumError struct {
	Enum  string // Go type name, for example "ResizeType"
	Value string // the rejected input, truncated to 64 bytes
}

func (e *EnumError) Error() string {
	return "transform: unknown " + e.Enum + " " + strconv.Quote(e.Value)
}

// maxEnumErrorValue bounds the attacker-controlled text an EnumError carries.
const maxEnumErrorValue = 64

// enum is the constraint shared by every enum in this package. Each enum is a
// uint8 whose String method returns its canonical lowercase name ("" for an
// Unset zero value) and whose count constant bounds the valid values.
type enum interface {
	~uint8
	String() string
}

func newEnumError(name, value string) *EnumError {
	if len(value) > maxEnumErrorValue {
		value = value[:maxEnumErrorValue]
	}
	return &EnumError{Enum: name, Value: value}
}

// parseEnum finds the value whose canonical name equals s exactly. The empty
// string never parses, so an Unset zero value can only come from omission.
func parseEnum[T enum](name, s string, count int) (T, error) {
	if s != "" {
		for i := range count {
			v := T(i)
			if v.String() == s {
				return v, nil
			}
		}
	}
	return 0, newEnumError(name, s)
}

func invalidEnum(name string, v uint8) string {
	return name + "(" + strconv.Itoa(int(v)) + ")"
}

func validEnum[T enum](v T, count int) bool { return int(v) < count }

func marshalEnum[T enum](name string, v T, count int) ([]byte, error) {
	if !validEnum(v, count) {
		return nil, newEnumError(name, v.String())
	}
	return []byte(v.String()), nil
}

// unmarshalEnum accepts the canonical name, or "" for the zero value.
func unmarshalEnum[T enum](name string, dst *T, b []byte, count int) error {
	if len(b) == 0 {
		*dst = 0
		return nil
	}
	v, err := parseEnum[T](name, string(b), count)
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

// ---------------------------------------------------------------------------
// Kind

// Kind is what the request asks the server to produce.
type Kind uint8

// Kind values. The zero value is KindProcess.
const (
	KindProcess Kind = iota // transform and encode an image
	KindInfo                // return metadata as JSON (/info, fm=json, fl_getinfo, thumbor meta)
	KindRaw                 // stream the source unmodified (imgproxy raw, imagekit orig-true)
)

const numKinds = 3

func (k Kind) String() string {
	switch k {
	case KindProcess:
		return "process"
	case KindInfo:
		return "info"
	case KindRaw:
		return "raw"
	}
	return invalidEnum("Kind", uint8(k))
}

// ParseKind parses a canonical Kind name.
func ParseKind(s string) (Kind, error) { return parseEnum[Kind]("Kind", s, numKinds) }

// MarshalText implements encoding.TextMarshaler.
func (k Kind) MarshalText() ([]byte, error) { return marshalEnum("Kind", k, numKinds) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (k *Kind) UnmarshalText(b []byte) error { return unmarshalEnum("Kind", k, b, numKinds) }

// ---------------------------------------------------------------------------
// SourceEncoding

// SourceEncoding is how the source reference was written in the URL.
type SourceEncoding uint8

// SourceEncoding values. The zero value is SourcePlain.
const (
	SourcePlain     SourceEncoding = iota // plain/… or a query parameter
	SourceBase64                          // base64url-encoded URL
	SourceEncrypted                       // enc/… AES-CBC
	SourceRelative                        // key relative to a mounted source
)

const numSourceEncodings = 4

func (e SourceEncoding) String() string {
	switch e {
	case SourcePlain:
		return "plain"
	case SourceBase64:
		return "base64"
	case SourceEncrypted:
		return "encrypted"
	case SourceRelative:
		return "relative"
	}
	return invalidEnum("SourceEncoding", uint8(e))
}

// ParseSourceEncoding parses a canonical SourceEncoding name.
func ParseSourceEncoding(s string) (SourceEncoding, error) {
	return parseEnum[SourceEncoding]("SourceEncoding", s, numSourceEncodings)
}

// MarshalText implements encoding.TextMarshaler.
func (e SourceEncoding) MarshalText() ([]byte, error) {
	return marshalEnum("SourceEncoding", e, numSourceEncodings)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (e *SourceEncoding) UnmarshalText(b []byte) error {
	return unmarshalEnum("SourceEncoding", e, b, numSourceEncodings)
}

// ---------------------------------------------------------------------------
// HashAlgorithm

// HashAlgorithm names a digest used for source hashsums and info calc_hashsums.
type HashAlgorithm uint8

// HashAlgorithm values.
const (
	HashUnset HashAlgorithm = iota
	HashMD5
	HashSHA1
	HashSHA256
	HashSHA512
)

const numHashAlgorithms = 5

func (h HashAlgorithm) String() string {
	switch h {
	case HashUnset:
		return ""
	case HashMD5:
		return "md5"
	case HashSHA1:
		return "sha1"
	case HashSHA256:
		return "sha256"
	case HashSHA512:
		return "sha512"
	}
	return invalidEnum("HashAlgorithm", uint8(h))
}

// HexLen is the length of the lowercase hex digest, or 0 for HashUnset.
func (h HashAlgorithm) HexLen() int {
	switch h {
	case HashUnset:
		return 0
	case HashMD5:
		return 32
	case HashSHA1:
		return 40
	case HashSHA256:
		return 64
	case HashSHA512:
		return 128
	}
	return 0
}

// ParseHashAlgorithm parses a canonical HashAlgorithm name.
func ParseHashAlgorithm(s string) (HashAlgorithm, error) {
	return parseEnum[HashAlgorithm]("HashAlgorithm", s, numHashAlgorithms)
}

// MarshalText implements encoding.TextMarshaler.
func (h HashAlgorithm) MarshalText() ([]byte, error) {
	return marshalEnum("HashAlgorithm", h, numHashAlgorithms)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (h *HashAlgorithm) UnmarshalText(b []byte) error {
	return unmarshalEnum("HashAlgorithm", h, b, numHashAlgorithms)
}

// ---------------------------------------------------------------------------
// ResizeType

// ResizeType is the resizing mode (imgproxy resizing_type plus wsrv/imagekit
// "outside"). ApplyDefaults turns ResizeUnset into ResizeFit.
type ResizeType uint8

// ResizeType values.
const (
	ResizeUnset    ResizeType = iota
	ResizeFit                 // fit inside the box, keep aspect ratio
	ResizeFill                // cover the box, crop the overflow
	ResizeFillDown            // fill, but never upscale; the box shrinks instead
	ResizeForce               // stretch to exactly the box
	ResizeAuto                // fill if orientations match, else fit
	ResizeOutside             // cover the box without cropping (min dimensions)
)

const numResizeTypes = 7

func (t ResizeType) String() string {
	switch t {
	case ResizeUnset:
		return ""
	case ResizeFit:
		return "fit"
	case ResizeFill:
		return "fill"
	case ResizeFillDown:
		return "fill-down"
	case ResizeForce:
		return "force"
	case ResizeAuto:
		return "auto"
	case ResizeOutside:
		return "outside"
	}
	return invalidEnum("ResizeType", uint8(t))
}

// ParseResizeType parses a canonical ResizeType name.
func ParseResizeType(s string) (ResizeType, error) {
	return parseEnum[ResizeType]("ResizeType", s, numResizeTypes)
}

// MarshalText implements encoding.TextMarshaler.
func (t ResizeType) MarshalText() ([]byte, error) {
	return marshalEnum("ResizeType", t, numResizeTypes)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (t *ResizeType) UnmarshalText(b []byte) error {
	return unmarshalEnum("ResizeType", t, b, numResizeTypes)
}

// ---------------------------------------------------------------------------
// Kernel

// Kernel is the resampling algorithm. ApplyDefaults turns KernelUnset into
// KernelLanczos3.
type Kernel uint8

// Kernel values.
const (
	KernelUnset Kernel = iota
	KernelNearest
	KernelLinear
	KernelCubic
	KernelMitchell
	KernelLanczos2
	KernelLanczos3
)

const numKernels = 7

func (k Kernel) String() string {
	switch k {
	case KernelUnset:
		return ""
	case KernelNearest:
		return "nearest"
	case KernelLinear:
		return "linear"
	case KernelCubic:
		return "cubic"
	case KernelMitchell:
		return "mitchell"
	case KernelLanczos2:
		return "lanczos2"
	case KernelLanczos3:
		return "lanczos3"
	}
	return invalidEnum("Kernel", uint8(k))
}

// ParseKernel parses a canonical Kernel name.
func ParseKernel(s string) (Kernel, error) { return parseEnum[Kernel]("Kernel", s, numKernels) }

// MarshalText implements encoding.TextMarshaler.
func (k Kernel) MarshalText() ([]byte, error) { return marshalEnum("Kernel", k, numKernels) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (k *Kernel) UnmarshalText(b []byte) error { return unmarshalEnum("Kernel", k, b, numKernels) }

// ---------------------------------------------------------------------------
// GravityType

// GravityType selects the anchor for crops, extends and watermarks.
// ApplyDefaults turns GravityUnset into GravityCenter.
type GravityType uint8

// GravityType values.
const (
	GravityUnset GravityType = iota
	GravityCenter
	GravityNorth
	GravitySouth
	GravityEast
	GravityWest
	GravityNorthEast
	GravityNorthWest
	GravitySouthEast
	GravitySouthWest
	GravitySmart          // libvips attention (imgproxy sm)
	GravityEntropy        // libvips entropy
	GravityFocusPoint     // Gravity.FocusX/FocusY (imgproxy fp)
	GravityFace           // M3 face detection; falls back to smart
	GravityObject         // M3 object detection, Gravity.Classes
	GravityObjectWeighted // M3 weighted objects, Gravity.Classes + Weights
)

const numGravityTypes = 16

func (g GravityType) String() string {
	switch g {
	case GravityUnset:
		return ""
	case GravityCenter:
		return "center"
	case GravityNorth:
		return "north"
	case GravitySouth:
		return "south"
	case GravityEast:
		return "east"
	case GravityWest:
		return "west"
	case GravityNorthEast:
		return "north-east"
	case GravityNorthWest:
		return "north-west"
	case GravitySouthEast:
		return "south-east"
	case GravitySouthWest:
		return "south-west"
	case GravitySmart:
		return "smart"
	case GravityEntropy:
		return "entropy"
	case GravityFocusPoint:
		return "focus-point"
	case GravityFace:
		return "face"
	case GravityObject:
		return "object"
	case GravityObjectWeighted:
		return "object-weighted"
	}
	return invalidEnum("GravityType", uint8(g))
}

// IsCompass reports whether g is the centre or one of the eight compass points.
func (g GravityType) IsCompass() bool { return g >= GravityCenter && g <= GravitySouthWest }

// IsDetection reports whether g needs content analysis (smart, entropy, face,
// objects). Such gravities are invalid where the image is not being cropped.
func (g GravityType) IsDetection() bool {
	return g == GravitySmart || g == GravityEntropy || g == GravityFace ||
		g == GravityObject || g == GravityObjectWeighted
}

// ParseGravityType parses a canonical GravityType name.
func ParseGravityType(s string) (GravityType, error) {
	return parseEnum[GravityType]("GravityType", s, numGravityTypes)
}

// MarshalText implements encoding.TextMarshaler.
func (g GravityType) MarshalText() ([]byte, error) {
	return marshalEnum("GravityType", g, numGravityTypes)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (g *GravityType) UnmarshalText(b []byte) error {
	return unmarshalEnum("GravityType", g, b, numGravityTypes)
}

// ---------------------------------------------------------------------------
// ExtendMode

// ExtendMode is how extended canvas area is filled.
type ExtendMode uint8

// ExtendMode values. The zero value is ExtendBackground.
const (
	ExtendBackground ExtendMode = iota // Pipeline.Background (transparent/white)
	ExtendCopy                         // replicate edge pixels (imgix fit=clamp)
	ExtendBlur                         // blurred copy of the image (imgix fill=blur, thumbor fill(blur); M3)
)

const numExtendModes = 3

func (m ExtendMode) String() string {
	switch m {
	case ExtendBackground:
		return "background"
	case ExtendCopy:
		return "copy"
	case ExtendBlur:
		return "blur"
	}
	return invalidEnum("ExtendMode", uint8(m))
}

// ParseExtendMode parses a canonical ExtendMode name.
func ParseExtendMode(s string) (ExtendMode, error) {
	return parseEnum[ExtendMode]("ExtendMode", s, numExtendModes)
}

// MarshalText implements encoding.TextMarshaler.
func (m ExtendMode) MarshalText() ([]byte, error) {
	return marshalEnum("ExtendMode", m, numExtendModes)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *ExtendMode) UnmarshalText(b []byte) error {
	return unmarshalEnum("ExtendMode", m, b, numExtendModes)
}

// ---------------------------------------------------------------------------
// MaskShape

// MaskShape is a wsrv shape mask.
type MaskShape uint8

// MaskShape values.
const (
	MaskUnset MaskShape = iota
	MaskCircle
	MaskEllipse
	MaskTriangle
	MaskTriangle180
	MaskPentagon
	MaskPentagon180
	MaskHexagon
	MaskSquare
	MaskStar
	MaskHeart
)

const numMaskShapes = 11

func (m MaskShape) String() string {
	switch m {
	case MaskUnset:
		return ""
	case MaskCircle:
		return "circle"
	case MaskEllipse:
		return "ellipse"
	case MaskTriangle:
		return "triangle"
	case MaskTriangle180:
		return "triangle-180"
	case MaskPentagon:
		return "pentagon"
	case MaskPentagon180:
		return "pentagon-180"
	case MaskHexagon:
		return "hexagon"
	case MaskSquare:
		return "square"
	case MaskStar:
		return "star"
	case MaskHeart:
		return "heart"
	}
	return invalidEnum("MaskShape", uint8(m))
}

// ParseMaskShape parses a canonical MaskShape name.
func ParseMaskShape(s string) (MaskShape, error) {
	return parseEnum[MaskShape]("MaskShape", s, numMaskShapes)
}

// MarshalText implements encoding.TextMarshaler.
func (m MaskShape) MarshalText() ([]byte, error) {
	return marshalEnum("MaskShape", m, numMaskShapes)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *MaskShape) UnmarshalText(b []byte) error {
	return unmarshalEnum("MaskShape", m, b, numMaskShapes)
}

// ---------------------------------------------------------------------------
// Direction

// Direction orients gradients and progressive blurs (imgproxy gr, pbl).
type Direction uint8

// Direction values. The zero value is DirectionDown (imgproxy's default).
const (
	DirectionDown  Direction = iota // top → bottom
	DirectionUp                     // bottom → top
	DirectionLeft                   // right → left
	DirectionRight                  // left → right
	DirectionAngle                  // the Angle field, degrees
)

const numDirections = 5

func (d Direction) String() string {
	switch d {
	case DirectionDown:
		return "down"
	case DirectionUp:
		return "up"
	case DirectionLeft:
		return "left"
	case DirectionRight:
		return "right"
	case DirectionAngle:
		return "angle"
	}
	return invalidEnum("Direction", uint8(d))
}

// ParseDirection parses a canonical Direction name.
func ParseDirection(s string) (Direction, error) {
	return parseEnum[Direction]("Direction", s, numDirections)
}

// MarshalText implements encoding.TextMarshaler.
func (d Direction) MarshalText() ([]byte, error) {
	return marshalEnum("Direction", d, numDirections)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Direction) UnmarshalText(b []byte) error {
	return unmarshalEnum("Direction", d, b, numDirections)
}

// ---------------------------------------------------------------------------
// UnsharpMode

// UnsharpMode is imgproxy's unsharp_masking mode.
type UnsharpMode uint8

// UnsharpMode values. Unset means the config default.
const (
	UnsharpUnset UnsharpMode = iota
	UnsharpAuto
	UnsharpNone
	UnsharpAlways
)

const numUnsharpModes = 4

func (m UnsharpMode) String() string {
	switch m {
	case UnsharpUnset:
		return ""
	case UnsharpAuto:
		return "auto"
	case UnsharpNone:
		return "none"
	case UnsharpAlways:
		return "always"
	}
	return invalidEnum("UnsharpMode", uint8(m))
}

// ParseUnsharpMode parses a canonical UnsharpMode name.
func ParseUnsharpMode(s string) (UnsharpMode, error) {
	return parseEnum[UnsharpMode]("UnsharpMode", s, numUnsharpModes)
}

// MarshalText implements encoding.TextMarshaler.
func (m UnsharpMode) MarshalText() ([]byte, error) {
	return marshalEnum("UnsharpMode", m, numUnsharpModes)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *UnsharpMode) UnmarshalText(b []byte) error {
	return unmarshalEnum("UnsharpMode", m, b, numUnsharpModes)
}

// ---------------------------------------------------------------------------
// TileMode

// TileMode is how a watermark is repeated.
type TileMode uint8

// TileMode values. The zero value is TileNone.
const (
	TileNone       TileMode = iota
	TileRepeat              // imgproxy position "re", imgix mark-tile=grid
	TileChessboard          // imgproxy position "ch" (M3)
)

const numTileModes = 3

func (m TileMode) String() string {
	switch m {
	case TileNone:
		return "none"
	case TileRepeat:
		return "repeat"
	case TileChessboard:
		return "chessboard"
	}
	return invalidEnum("TileMode", uint8(m))
}

// ParseTileMode parses a canonical TileMode name.
func ParseTileMode(s string) (TileMode, error) {
	return parseEnum[TileMode]("TileMode", s, numTileModes)
}

// MarshalText implements encoding.TextMarshaler.
func (m TileMode) MarshalText() ([]byte, error) { return marshalEnum("TileMode", m, numTileModes) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *TileMode) UnmarshalText(b []byte) error {
	return unmarshalEnum("TileMode", m, b, numTileModes)
}

// ---------------------------------------------------------------------------
// Format

// Format is an output format. FormatUnset means "keep the source format or
// negotiate from Accept" (FORMATS §6).
type Format uint8

// Format values.
const (
	FormatUnset Format = iota
	FormatJPEG
	FormatPNG
	FormatWebP
	FormatAVIF
	FormatJXL
	FormatHEIC
	FormatGIF
	FormatTIFF
	FormatBMP
	FormatICO
	FormatSVG
	FormatMP4
	FormatWebM
	FormatJSON
	FormatBest // M2: encode the candidate set and keep the smallest
	// FormatHEVC is HEVC video in MP4 (hvc1), written only by images with
	// libx265 (the gpl flavor, FORMATS 5.1); never negotiated.
	FormatHEVC
)

const numFormats = 17

func (f Format) String() string {
	switch f {
	case FormatUnset:
		return ""
	case FormatJPEG:
		return "jpeg"
	case FormatPNG:
		return "png"
	case FormatWebP:
		return "webp"
	case FormatAVIF:
		return "avif"
	case FormatJXL:
		return "jxl"
	case FormatHEIC:
		return "heic"
	case FormatGIF:
		return "gif"
	case FormatTIFF:
		return "tiff"
	case FormatBMP:
		return "bmp"
	case FormatICO:
		return "ico"
	case FormatSVG:
		return "svg"
	case FormatMP4:
		return "mp4"
	case FormatWebM:
		return "webm"
	case FormatJSON:
		return "json"
	case FormatBest:
		return "best"
	case FormatHEVC:
		return "hevc"
	}
	return invalidEnum("Format", uint8(f))
}

// IsVideo reports the video output formats (MP4, WebM, HEVC): animations
// encoded by FFmpeg (FORMATS 5.1).
func (f Format) IsVideo() bool {
	return f == FormatMP4 || f == FormatWebM || f == FormatHEVC
}

// ParseFormat parses a canonical Format name. It also accepts the extensions
// "jpg" and "tif", and "h265" for "hevc".
func ParseFormat(s string) (Format, error) {
	switch s {
	case "jpg":
		return FormatJPEG, nil
	case "tif":
		return FormatTIFF, nil
	case "h265":
		return FormatHEVC, nil
	}
	return parseEnum[Format]("Format", s, numFormats)
}

// MarshalText implements encoding.TextMarshaler.
func (f Format) MarshalText() ([]byte, error) { return marshalEnum("Format", f, numFormats) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (f *Format) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*f = FormatUnset
		return nil
	}
	v, err := ParseFormat(string(b))
	if err != nil {
		return err
	}
	*f = v
	return nil
}

// ---------------------------------------------------------------------------
// Subsampling

// Subsampling is chroma subsampling for lossy encoders.
type Subsampling uint8

// Subsampling values. The zero value is SubsamplingAuto (encoder decides,
// 4:4:4 at quality ≥ 90).
const (
	SubsamplingAuto Subsampling = iota
	Subsampling420
	Subsampling422
	Subsampling444
)

const numSubsamplings = 4

func (s Subsampling) String() string {
	switch s {
	case SubsamplingAuto:
		return "auto"
	case Subsampling420:
		return "4:2:0"
	case Subsampling422:
		return "4:2:2"
	case Subsampling444:
		return "4:4:4"
	}
	return invalidEnum("Subsampling", uint8(s))
}

// ParseSubsampling parses a canonical Subsampling name.
func ParseSubsampling(s string) (Subsampling, error) {
	return parseEnum[Subsampling]("Subsampling", s, numSubsamplings)
}

// MarshalText implements encoding.TextMarshaler.
func (s Subsampling) MarshalText() ([]byte, error) {
	return marshalEnum("Subsampling", s, numSubsamplings)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *Subsampling) UnmarshalText(b []byte) error {
	return unmarshalEnum("Subsampling", s, b, numSubsamplings)
}

// ---------------------------------------------------------------------------
// AutoQualityMethod

// AutoQualityMethod is imgproxy's autoquality method.
type AutoQualityMethod uint8

// AutoQualityMethod values. Unset means the config default.
const (
	AutoQualityUnset AutoQualityMethod = iota
	AutoQualityNone
	AutoQualitySize
	AutoQualityDSSIM
	AutoQualityML
)

const numAutoQualityMethods = 5

func (m AutoQualityMethod) String() string {
	switch m {
	case AutoQualityUnset:
		return ""
	case AutoQualityNone:
		return "none"
	case AutoQualitySize:
		return "size"
	case AutoQualityDSSIM:
		return "dssim"
	case AutoQualityML:
		return "ml"
	}
	return invalidEnum("AutoQualityMethod", uint8(m))
}

// ParseAutoQualityMethod parses a canonical AutoQualityMethod name.
func ParseAutoQualityMethod(s string) (AutoQualityMethod, error) {
	return parseEnum[AutoQualityMethod]("AutoQualityMethod", s, numAutoQualityMethods)
}

// MarshalText implements encoding.TextMarshaler.
func (m AutoQualityMethod) MarshalText() ([]byte, error) {
	return marshalEnum("AutoQualityMethod", m, numAutoQualityMethods)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *AutoQualityMethod) UnmarshalText(b []byte) error {
	return unmarshalEnum("AutoQualityMethod", m, b, numAutoQualityMethods)
}

// ---------------------------------------------------------------------------
// WebPCompression

// WebPCompression is imgproxy's webp_options compression.
type WebPCompression uint8

// WebPCompression values. Unset means the config default.
const (
	WebPCompressionUnset WebPCompression = iota
	WebPLossy
	WebPNearLossless
	WebPLossless
)

const numWebPCompressions = 4

func (c WebPCompression) String() string {
	switch c {
	case WebPCompressionUnset:
		return ""
	case WebPLossy:
		return "lossy"
	case WebPNearLossless:
		return "near_lossless"
	case WebPLossless:
		return "lossless"
	}
	return invalidEnum("WebPCompression", uint8(c))
}

// ParseWebPCompression parses a canonical WebPCompression name.
func ParseWebPCompression(s string) (WebPCompression, error) {
	return parseEnum[WebPCompression]("WebPCompression", s, numWebPCompressions)
}

// MarshalText implements encoding.TextMarshaler.
func (c WebPCompression) MarshalText() ([]byte, error) {
	return marshalEnum("WebPCompression", c, numWebPCompressions)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (c *WebPCompression) UnmarshalText(b []byte) error {
	return unmarshalEnum("WebPCompression", c, b, numWebPCompressions)
}

// ---------------------------------------------------------------------------
// WebPPreset

// WebPPreset is the libwebp encoder preset.
type WebPPreset uint8

// WebPPreset values. Unset means the config default.
const (
	WebPPresetUnset WebPPreset = iota
	WebPPresetDefault
	WebPPresetPhoto
	WebPPresetPicture
	WebPPresetDrawing
	WebPPresetIcon
	WebPPresetText
)

const numWebPPresets = 7

func (p WebPPreset) String() string {
	switch p {
	case WebPPresetUnset:
		return ""
	case WebPPresetDefault:
		return "default"
	case WebPPresetPhoto:
		return "photo"
	case WebPPresetPicture:
		return "picture"
	case WebPPresetDrawing:
		return "drawing"
	case WebPPresetIcon:
		return "icon"
	case WebPPresetText:
		return "text"
	}
	return invalidEnum("WebPPreset", uint8(p))
}

// ParseWebPPreset parses a canonical WebPPreset name.
func ParseWebPPreset(s string) (WebPPreset, error) {
	return parseEnum[WebPPreset]("WebPPreset", s, numWebPPresets)
}

// MarshalText implements encoding.TextMarshaler.
func (p WebPPreset) MarshalText() ([]byte, error) {
	return marshalEnum("WebPPreset", p, numWebPPresets)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (p *WebPPreset) UnmarshalText(b []byte) error {
	return unmarshalEnum("WebPPreset", p, b, numWebPPresets)
}

// ---------------------------------------------------------------------------
// ResponseEncoding

// ResponseEncoding is how the encoded image is put on the wire.
type ResponseEncoding uint8

// ResponseEncoding values. The zero value is ResponseBinary.
const (
	ResponseBinary  ResponseEncoding = iota
	ResponseDataURL                  // wsrv encoding=base64 (M2)
)

const numResponseEncodings = 2

func (e ResponseEncoding) String() string {
	switch e {
	case ResponseBinary:
		return "binary"
	case ResponseDataURL:
		return "data-url"
	}
	return invalidEnum("ResponseEncoding", uint8(e))
}

// ParseResponseEncoding parses a canonical ResponseEncoding name.
func ParseResponseEncoding(s string) (ResponseEncoding, error) {
	return parseEnum[ResponseEncoding]("ResponseEncoding", s, numResponseEncodings)
}

// MarshalText implements encoding.TextMarshaler.
func (e ResponseEncoding) MarshalText() ([]byte, error) {
	return marshalEnum("ResponseEncoding", e, numResponseEncodings)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (e *ResponseEncoding) UnmarshalText(b []byte) error {
	return unmarshalEnum("ResponseEncoding", e, b, numResponseEncodings)
}

// ---------------------------------------------------------------------------
// InfoSchema

// InfoSchema is the JSON shape of an info response.
type InfoSchema uint8

// InfoSchema values. The zero value is InfoSchemaImgproxy.
const (
	InfoSchemaImgproxy InfoSchema = iota
	InfoSchemaThumbor             // thumbor /meta
)

const numInfoSchemas = 2

func (s InfoSchema) String() string {
	switch s {
	case InfoSchemaImgproxy:
		return "imgproxy"
	case InfoSchemaThumbor:
		return "thumbor"
	}
	return invalidEnum("InfoSchema", uint8(s))
}

// ParseInfoSchema parses a canonical InfoSchema name.
func ParseInfoSchema(s string) (InfoSchema, error) {
	return parseEnum[InfoSchema]("InfoSchema", s, numInfoSchemas)
}

// MarshalText implements encoding.TextMarshaler.
func (s InfoSchema) MarshalText() ([]byte, error) {
	return marshalEnum("InfoSchema", s, numInfoSchemas)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (s *InfoSchema) UnmarshalText(b []byte) error {
	return unmarshalEnum("InfoSchema", s, b, numInfoSchemas)
}

// ---------------------------------------------------------------------------
// GainMapMode

// GainMapMode says what an HDR gain map (UltraHDR, ISO 21496-1) does to the
// output (WP-104, FORMATS.md §1.4).
type GainMapMode uint8

// GainMapMode values. The zero value is GainMapAuto.
const (
	// GainMapAuto applies the gain map when preserve_hdr asks for HDR and
	// the output holds it (AVIF, JPEG XL, JPEG as UltraHDR); otherwise the
	// SDR base is used.
	GainMapAuto GainMapMode = iota
	// GainMapHDR applies the gain map for those outputs even without
	// preserve_hdr.
	GainMapHDR
	// GainMapSDR always uses the SDR base, preserve_hdr or not.
	GainMapSDR
)

const numGainMapModes = 3

func (m GainMapMode) String() string {
	switch m {
	case GainMapAuto:
		return "auto"
	case GainMapHDR:
		return "hdr"
	case GainMapSDR:
		return "sdr"
	}
	return invalidEnum("GainMapMode", uint8(m))
}

// ParseGainMapMode parses a canonical GainMapMode name.
func ParseGainMapMode(s string) (GainMapMode, error) {
	return parseEnum[GainMapMode]("GainMapMode", s, numGainMapModes)
}

// MarshalText implements encoding.TextMarshaler.
func (m GainMapMode) MarshalText() ([]byte, error) {
	return marshalEnum("GainMapMode", m, numGainMapModes)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *GainMapMode) UnmarshalText(b []byte) error {
	return unmarshalEnum("GainMapMode", m, b, numGainMapModes)
}
