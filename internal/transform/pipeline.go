package transform

// Pipeline is one processing pass. Chained pipelines run in order, each on the
// output of the previous one.
type Pipeline struct {
	Resize           Resize
	Gravity          Gravity
	Crop             *Crop        // pre-resize crop in source space
	CropObjects      *CropObjects // M3
	Trim             *Trim
	Rotate           int     // 0/90/180/270 after ApplyDefaults
	RotateAny        float64 // arbitrary angle, degrees clockwise, filled with Background (imgix rot, wsrv ro, cloudinary a)
	AutoRotate       *bool   // EXIF orientation; nil = config default
	Flip             Flip
	Padding          *Padding // after resize, follows DPR
	Extend           Extend
	ExtendAR         Extend
	Background       *Color // nil = keep alpha
	Border           *Border
	Radius           *Radius
	Mask             *Mask
	Adjust           Adjust
	Monochrome       *Monochrome
	Duotone          *Duotone
	Colorize         *Colorize
	Gradient         *Gradient
	Tint             *Color
	Negate           bool
	Sepia            bool
	Grayscale        bool
	Equalize         bool    // histogram equalisation (thumbor equalize, M2)
	Blur             float64 // Gaussian sigma; 0 = off
	Sharpen          float64 // sigma; 0 = off
	Unsharp          *Unsharp
	Pixelate         int // block size px; 0 = off
	BlurAreas        *BlurAreas
	ProgressiveBlur  *ProgressiveBlur
	BlurDetections   *DetectionFilter // M3
	DrawDetections   *DetectionFilter // M3
	Watermarks       []Watermark
	SVGStyle         string // CSS injected into SVG sources
	Page             *int   // first page/frame; nil = 0
	Pages            *int   // number of pages/frames, -1 = all; nil = format default
	DisableAnimation bool
	Video            *VideoOptions // M3
}

// Resize describes the target box.
type Resize struct {
	Type        ResizeType
	Width       Dim // zero = auto
	Height      Dim
	MinWidth    int
	MinHeight   int
	MaxWidth    int     // clamp the result (imgix max-w); 0 = none
	MaxHeight   int     // clamp the result (imgix max-h); 0 = none
	AspectRatio float64 // width/height used to derive a missing dimension (imgix ar, cloudinary ar_, bunny aspect_ratio); 0 = none
	ZoomX       float64 // 1 after ApplyDefaults
	ZoomY       float64
	DPR         float64 // 1 after ApplyDefaults; affects offsets and padding
	Enlarge     bool
	Adaptive    bool // swap Width/Height when the source orientation differs (thumbor adaptive-*)
	Algorithm   Kernel
}

// Dim is a dimension, absolute or relative to the source. At most one of Px
// and Rel is non-zero; both zero means "auto".
type Dim struct {
	Px  int
	Rel float64 // fraction of the source dimension, for example 0.5 = 50%
}

// IsZero reports whether d is "auto".
func (d Dim) IsZero() bool { return d.Px == 0 && d.Rel == 0 }

// Gravity anchors crops, extends and watermarks.
type Gravity struct {
	Type    GravityType
	XOffset float64 // ≥ 1 absolute px, < 1 relative (imgproxy semantics)
	YOffset float64
	FocusX  float64 // GravityFocusPoint, 0..1
	FocusY  float64
	// FocusPx is a GravityFocusPoint given in source pixels (thumbor
	// focal, Cloudinary g_xy_center with x_/y_) instead of FocusX/FocusY,
	// which must then be 0. The planner converts it to fractions of the
	// source as displayed; see FocusRect and FocusFromPixels.
	FocusPx   *FocusRect
	FocusZoom float64 // imgix fp-z, cloudinary z; 0 = none
	Classes   []string
	Weights   []float64 // GravityObjectWeighted, parallel to Classes
	FaceIndex *int      // imgix faceindex (M3)
	FacePad   float64   // imgix facepad (M3); 0 = none
	// ObjectsPosition places detected objects inside the result (imgproxy
	// objects_position, M3).
	ObjectsPosition *ObjectsPosition
}

// ObjectsPosition is imgproxy's objects_position.
type ObjectsPosition struct {
	Type         GravityType // compass or GravityFocusPoint
	X, Y         float64     // offsets (compass) or focus point (0..1)
	Proportional bool        // imgproxy "prop"
}

// Crop is a pre-resize crop in source space.
type Crop struct {
	Width         Dim
	Height        Dim
	Gravity       Gravity // GravityUnset inherits Pipeline.Gravity in ApplyDefaults
	AspectRatio   float64 // imgproxy crop_aspect_ratio (M2); 0 = none
	AspectEnlarge bool
}

// CropObjects crops to detected objects (imgproxy crop_objects, M3).
type CropObjects struct {
	Scale   float64
	Classes []string
}

// Trim removes a uniform border.
type Trim struct {
	Threshold            float64
	Color                *Color // nil = auto-detect from a corner pixel
	ColorFromBottomRight bool   // thumbor trim:bottom-right
	EqualHor             bool
	EqualVer             bool
}

// Flip mirrors the image.
type Flip struct {
	H, V bool
}

// Padding adds space after resizing (CSS order).
type Padding struct {
	Top, Right, Bottom, Left int
}

// Extend grows the canvas to the requested size (or aspect ratio).
type Extend struct {
	Enabled bool
	Gravity Gravity
	Mode    ExtendMode
}

// Color is 8-bit sRGB with alpha (255 = opaque).
type Color struct {
	R, G, B, A uint8
}

// Border draws a border of the given widths.
type Border struct {
	Top, Right, Bottom, Left int
	Color                    Color
}

// Radius rounds corners.
type Radius struct {
	TopLeft, TopRight, BottomRight, BottomLeft int
	Max                                        bool // circle/ellipse (cloudinary r_max, imagekit r-max)
}

// Mask applies a shape mask (wsrv mask).
type Mask struct {
	Shape      MaskShape
	Trim       bool   // wsrv mtrim
	Background *Color // wsrv mbg; nil = transparent
}

// Adjust holds colour adjustments. nil = unchanged; ApplyDefaults also sets
// identity values (brightness 0, hue 0, multipliers 1) to nil.
type Adjust struct {
	Brightness *float64 // additive, −255..255
	Contrast   *float64 // multiplier
	Saturation *float64 // multiplier
	Gamma      *float64 // multiplier (> 0)
	Hue        *float64 // additive degrees
	Exposure   *float64 // multiplier
	Vibrance   *float64 // multiplier
	Red        *float64 // per-channel multiplier (thumbor rgb, M2)
	Green      *float64
	Blue       *float64
	Opacity    *float64 // 0..1 alpha multiplier (cloudinary o_)
}

// Monochrome is imgproxy's monochrome.
type Monochrome struct {
	Intensity float64 // 0..1
	Color     Color
}

// Duotone is imgproxy's duotone.
type Duotone struct {
	Intensity float64 // 0..1
	Shadow    Color
	Highlight Color
}

// Colorize is imgproxy's colorize.
type Colorize struct {
	Opacity   float64 // 0..1
	Color     Color
	KeepAlpha bool
}

// Gradient is imgproxy's gradient.
type Gradient struct {
	Opacity   float64 // 0..1
	Color     Color
	Direction Direction
	Angle     float64 // degrees, DirectionAngle only
	Start     float64 // 0..1
	Stop      float64 // 0..1
}

// Unsharp is unsharp masking: imgproxy's auto-sharpening controls (mode,
// weight, divider) and classic USM parameters (sigma, amount) from imgix usm,
// thumbor sharpen and cloudinary e_unsharp_mask.
type Unsharp struct {
	Mode    UnsharpMode
	Weight  float64
	Divider float64
	Sigma   float64
	Amount  float64
}

// BlurAreas blurs rectangles of the source.
type BlurAreas struct {
	Sigma float64
	Areas []Area
}

// Area is a rectangle in relative source coordinates (0..1).
type Area struct {
	Left, Top, Width, Height float64
}

// ProgressiveBlur is imgproxy's progressive_blur.
type ProgressiveBlur struct {
	Sigma     float64
	Direction Direction
	Angle     float64 // degrees, DirectionAngle only
	Start     float64 // 0..1
	Stop      float64 // 0..1
}

// DetectionFilter selects detected objects to blur, pixelate or draw (M3).
type DetectionFilter struct {
	Sigma    float64 // blur sigma; 0 with Pixelate 0 = config default
	Pixelate int     // pixelate block size instead of blur (cloudinary e_pixelate_faces)
	Classes  []string
}

// Watermark overlays an image or text.
type Watermark struct {
	Opacity  float64 // 0..1
	Position Gravity // GravityUnset → center
	Tile     TileMode
	Scale    float64     // relative to the result width; 0 = natural size
	Source   *SourceSpec // nil = config default watermark (XI_PROCESSING_WATERMARK_SOURCE)
	Text     string      // Pango markup (imgproxy wmt, imgix txt, cloudinary l_text)
	Font     string      // Pango font description for Text
	Color    *Color      // Text colour
	Width    int         // imgproxy watermark_size; 0 = auto
	Height   int
	Rotate   float64 // degrees
	Shadow   float64 // shadow sigma; 0 = off
}

// VideoOptions selects frames from video sources (M3).
type VideoOptions struct {
	Second    *float64 // nil = config default
	Keyframes *bool
	Tile      *VideoTile
	Animation *VideoAnimation
}

// VideoTile is imgproxy's video_thumbnail_tile.
type VideoTile struct {
	Step                  float64
	Columns, Rows         int
	TileWidth, TileHeight int
	ExtendTile            bool
	Trim                  bool
	Fill                  bool
	FocusX, FocusY        float64
}

// VideoAnimation is imgproxy's video_thumbnail_animation.
type VideoAnimation struct {
	Step                    float64
	Delay                   float64 // seconds per frame
	Frames                  int
	FrameWidth, FrameHeight int
	ExtendFrame             bool
	Trim                    bool
	Fill                    bool
	FocusX, FocusY          float64
}
