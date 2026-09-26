package transform

// Pipeline is one processing pass. Chained pipelines run in order, each on the
// output of the previous one.
type Pipeline struct {
	Resize           Resize
	Gravity          Gravity
	Crop             *Crop        // pre-resize crop in source space
	CropObjects      *CropObjects // pre-crop to detected objects (WP-72)
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
	BlurDetections   *DetectionFilter // blur or pixelate detected objects (WP-73)
	DrawDetections   *DetectionFilter // draw detected objects' boxes (WP-73)
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
//
// Face and object gravities (WP-72) centre the crop on detected faces or
// objects in the source (Classes: "face", COCO class names or
// supercategories, see ResolveClass; none = every class). They are only
// valid where a crop is placed, and fall back to the smart crop when
// nothing is found (or when detection did not run for the planner).
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
	// Classes are the classes GravityObject and GravityObjectWeighted
	// look for.
	Classes []string
	Weights []float64 // GravityObjectWeighted, parallel to Classes
	// FaceIndex selects one face (0-based, best score first) for
	// GravityFace (imgix faceindex); nil = every face. An index past the
	// faces found is no face (smart fallback).
	FaceIndex *int
	// FacePad > 0 on GravityFace zooms on the faces: the pipeline is
	// pre-cropped to the faces' box scaled by FacePad, like CropObjects
	// with Scale FacePad (imgix fit=facearea with facepad, Cloudinary
	// c_thumb with g_face). 0 = only place the crop.
	FacePad float64
	// ObjectsPosition places detected objects inside the result (imgproxy
	// objects_position); nil = centred.
	ObjectsPosition *ObjectsPosition
}

// ObjectsPosition is imgproxy's objects_position: where the detected
// objects' box lands in the crop.
type ObjectsPosition struct {
	// Type is a compass point (the box against that edge or corner, X and
	// Y the offsets: positive moves it away from the edge, for the centre
	// right and down; >= 1 pixels, < 1 fractions of the image) or
	// GravityFocusPoint (the box centre at X, Y of the crop, 0..1).
	Type         GravityType
	X, Y         float64
	Proportional bool // imgproxy "prop": the box keeps its relative position
}

// Crop is a pre-resize crop in source space.
type Crop struct {
	Width         Dim
	Height        Dim
	Gravity       Gravity // GravityUnset inherits Pipeline.Gravity in ApplyDefaults
	AspectRatio   float64 // imgproxy crop_aspect_ratio (M2); 0 = none
	AspectEnlarge bool
}

// CropObjects pre-crops to detected objects (imgproxy crop_objects,
// AWS SIH smartCrop, imgix fit=facearea): the union box of the selected
// detections, scaled by Scale around its centre and grown by Padding, and
// for fill and fill-down resizes grown to the result's aspect ratio. It
// takes precedence over Crop; without detections the pipeline runs as if
// it were not set.
type CropObjects struct {
	Scale   float64  // 1 = the box itself; 0 = 1
	Classes []string // see Gravity.Classes; none = every class
	// Index selects one detection of the classes (0-based, best score
	// first: AWS SIH faceIndex, imgix faceindex); nil = all.
	Index *int
	// Padding adds pixels of the source on every side (AWS SIH padding).
	Padding int
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

// DetectionFilter selects detected objects to blur, pixelate or draw
// (imgproxy blur_detections and draw_detections, imgix face-blur and
// face-pixel, Cloudinary e_blur_faces and e_pixelate_faces).
type DetectionFilter struct {
	Sigma    float64  // blur sigma; 0 with Pixelate 0 = the planner's default
	Pixelate int      // pixelate block size (pixels of the result) instead of blur
	Classes  []string // see Gravity.Classes; none = every class
}

// Watermark overlays an image or text. A watermark with neither Source nor
// Text is the configured image (XI_PROCESSING_WATERMARK_SOURCE).
type Watermark struct {
	Opacity  float64 // 0..1
	Position Gravity // GravityUnset → center; offsets are the spacing when tiled
	Tile     TileMode
	Scale    float64     // fit into Scale × the frame (both sides); 0 = natural size (or Width/Height)
	Source   *SourceSpec // per-request image (imgproxy wmu, imgix mark, cloudinary l_, imagekit l-image)
	// Text is Pango markup (imgproxy wmt, imgix txt, cloudinary l_text,
	// imagekit l-text). Dialects whose text is plain escape it; the
	// executor sanitises it again before rendering.
	Text  string
	Font  string // Pango font description for Text ("family [style] size"); "" = XI_PROCESSING_WATERMARK_FONT
	Color *Color // Text colour; nil = black (or the markup's)
	// TextWidth wraps Text at this width in CSS pixels (imgix txt-width,
	// imagekit text layer w); 0 wraps at the frame width.
	TextWidth int
	Width     int     // fit into Width×Height CSS pixels, enlarging (imgproxy watermark_size); 0 = auto; used when Scale is 0
	Height    int     //
	Rotate    float64 // degrees clockwise, after sizing
	Shadow    float64 // drop-shadow sigma in CSS pixels; 0 = off
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
