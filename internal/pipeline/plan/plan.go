package plan

import "github.com/xantiagoma/xtandard-image/internal/transform"

// SourceMeta is the header metadata the planner needs. The executor maps
// codec/vips header data onto it.
type SourceMeta struct {
	Width  int // one page/frame, as stored (before EXIF orientation)
	Height int // one page/frame
	// Frames is the number of pages or frames in the file; ≤ 1 is one.
	Frames int
	// PageHeight is the height of one frame when the header describes a
	// vertical strip (Height = Frames × PageHeight); 0 means Height.
	PageHeight  int
	HasAlpha    bool
	Orientation int    // EXIF orientation 1..8; 0 is treated as 1
	IsAnimated  bool   // several frames with delays (GIF, WebP, JXL)
	Format      string // source format name: "jpeg", "png", "webp", "gif", "svg", "pdf", "heic", "avif", "raw", ...
	IsVector    bool   // SVG, PDF: rendered at any scale
	// Density is the DPI a vector source's Width and Height were measured
	// at (PDF: XI_FORMATS_PDF_DPI); it is rendered at the same density,
	// scaled. 0 is the loader default.
	Density float64
	// Selection reports that Width and Height describe the page selection
	// of the request (`page`, and `pages` stacked vertically) as loaded,
	// not the first page. The executor sets it after measuring a selection
	// the header could not describe.
	Selection bool
	// PreviewWidth and PreviewHeight are the size of a camera RAW's
	// embedded JPEG preview in the stored orientation (the RAW's
	// Orientation applies to it); 0 when there is none. Width and Height
	// are the demosaiced size.
	PreviewWidth, PreviewHeight int
	// HDR marks a scene-linear float source (OpenEXR, Radiance, PFM): it is
	// loaded with random access (the tone mapper measures it first) and
	// costs 4 bytes per sample until it is mapped to 8 bits.
	HDR bool
	// IsVideo marks a video source (MP4, MOV, WebM, MKV, AVI): one frame is
	// decoded by FFmpeg and scaled on load by libswscale (LoadStep.Scale),
	// leaving the last 2× of a reduction to libvips. Width and Height are
	// the display size, Orientation the display matrix.
	IsVideo bool
}

// DefaultRawPreviewMinRatio is the RawPreviewMinRatio used when the limit
// is zero: the preview must be at least the size the source is scaled to.
const DefaultRawPreviewMinRatio = 1.0

// Limits are the configured processing limits (XI_LIMITS_*). Zero means
// unlimited unless stated otherwise.
type Limits struct {
	MaxSrcPixels            int64 // width × height × frames of the loaded source
	MaxResultDimension      int   // result width and height, padding and extend included (downscales, like imgproxy)
	MaxAnimationFrames      int   // frames loaded; 1 disables animation
	MaxAnimationFramePixels int64 // per-frame pixels of animated sources (replaces MaxSrcPixels for them)
	// MemoryBudget rejects plans whose Cost.EstimatedBytes exceeds it
	// (XI_PROCESSING_MEMORY_BUDGET).
	MemoryBudget int64
	// AllowRaisingLimits lets transform.Request.Limits raise the values
	// above; otherwise overrides may only lower them.
	AllowRaisingLimits bool
	// DisableShrinkOnLoad turns off shrink-on-load for raster formats (and
	// the half-size RAW demosaic).
	DisableShrinkOnLoad bool
	// MaxRawDemosaicPixels caps the sensor width × height of a camera RAW
	// that is demosaiced (XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS); 0 is
	// unlimited. The embedded-preview path is not affected.
	MaxRawDemosaicPixels int64
	// RawPreviewMinRatio: a camera RAW is decoded from its embedded JPEG
	// preview when the preview is at least this many times the size the
	// source is scaled to, on both axes (XI_FORMATS_RAW_PREVIEW_MIN_RATIO);
	// 0 means DefaultRawPreviewMinRatio.
	RawPreviewMinRatio float64
}

// Observed is what the executor saw after a step whose outcome a header
// cannot predict (see the package documentation).
type Observed struct {
	// Width and Height are the image size as stored (before orientation),
	// one frame.
	Width, Height int
	// Loaded: the size after LoadStep (shrink-on-load rounding).
	Loaded bool
	// Thumbnail: the size of the embedded thumbnail LoadStep.Thumbnail
	// asked for.
	Thumbnail bool
	// Trimmed: the size after TrimStep.
	Trimmed bool
}

// Plan is the processing of one pipeline. Chained pipelines run on the
// result of the previous plan (materialised when Materialize is set).
type Plan struct {
	Load LoadStep
	// Animation is set when the image is processed as an animation: Steps
	// then run on every frame separately and After on the joined strip.
	Animation *AnimationStep
	// Steps run in order on the image (on every frame when animated).
	Steps []Step
	// After runs on the whole image once Steps are done (and frames are
	// joined): watermarks and, on the last plan, the output colour.
	After []Step
	// Chained plans follow this one; their Load is zero. Build predicts
	// them from the previous prediction; the executor replans each on the
	// observed result of the previous pipeline with Chain.
	Chained []*Plan
	// Materialize is set on chained plans whose input must be computed into
	// memory before they run (a step reads it out of order: trim, smart
	// crop, rotate, flip, filters). Otherwise the chained plan extends the
	// previous pipeline's lazy graph.
	Materialize bool
	// Output is the result of this plan (the last chained plan's is the
	// response).
	Output Output
	// Cost is the peak memory estimate of this plan and its chained plans
	// (on the root: the worst pipeline plus the materialised results held
	// while it runs).
	Cost Cost
	// Unplanned lists request options this planner does not turn into
	// steps yet (later work packages), for the debug header.
	Unplanned []string
}

// Output describes the result of a plan.
type Output struct {
	Width, Height int // one frame
	Frames        int // 1 unless animated
	// Format is the output format hint: the requested format, or the
	// source format when the request leaves it to negotiation.
	Format transform.Format
	// Alpha reports whether the result may have transparency.
	Alpha bool
	// DPRScale is the effective device-pixel-ratio scale (imgproxy
	// "imgproxy-dpr-scale"), reported to clients as the content DPR.
	DPRScale float64
}

// Cost is the memory-weighted admission estimate (ARCHITECTURE §11).
type Cost struct {
	Pixels         int64 // peak pixels held by one image in the pipeline, all frames
	EstimatedBytes int64 // Pixels × bands × bytes per sample, ×2 with random access
}

// Access is the pixel access pattern the load needs.
type Access uint8

// Access values.
const (
	AccessSequential Access = iota // streaming, top to bottom
	AccessRandom                   // some step needs random access (rotate, trim, smart crop, filters)
)

// Step is one processing operation. It is a sealed sum type: the concrete
// types are the *Step structs of this package.
type Step interface{ step() }

// LoadStep decodes the source.
type LoadStep struct {
	// ShrinkOnLoad is the JPEG shrink factor: 2, 4 or 8; 0 or 1 is off.
	// For a camera RAW it applies to the embedded preview (Preview).
	ShrinkOnLoad int
	// Preview decodes a camera RAW from its embedded JPEG preview
	// (SourceMeta.PreviewWidth × PreviewHeight) instead of demosaicing it.
	Preview bool
	// HalfSize demosaics a camera RAW at half its width and height.
	HalfSize bool
	// Scale is the WebP/SVG/PDF/video scale-on-load factor; 0 is off.
	Scale float64
	// Density is the vector rendering DPI; 0 keeps the loader default.
	Density float64
	Page    int // first page or frame
	// NPages is the number of pages or frames to load (1 = one). Several
	// pages of a still (the `pages` option) load as one image, stacked
	// vertically and left-aligned (imgproxy Pro).
	NPages int
	// Autorotate is always false: orientation is applied by explicit
	// RotateStep/FlipStep after resizing, where imgproxy applies it, so the
	// crop geometry matches. The executor drops the orientation tag.
	Autorotate bool
	// Thumbnail asks for the embedded HEIF/AVIF thumbnail; the executor
	// reports its size through Continue.
	Thumbnail bool
	Access    Access
	// Width and Height are the predicted loaded size of one frame, as
	// stored (before orientation).
	Width, Height int
}

// AnimationStep describes an animation processed frame by frame.
type AnimationStep struct {
	Frames     int // frames loaded
	PageHeight int // height of one loaded frame
}

// ColourStage selects a colour conversion.
type ColourStage uint8

// ColourStage values.
const (
	ColourToProcessing ColourStage = iota // import ICC, convert to the working space
	ColourToResult                        // convert to the output space, embed or strip the profile
)

// ColourStep converts colour spaces.
type ColourStep struct{ Stage ColourStage }

// TrimStep removes a uniform border (imgproxy trim). The size after it is
// unknown: the executor reports it through Continue.
type TrimStep struct {
	Threshold            float64
	Color                *transform.Color // nil = detect from a corner pixel
	ColorFromBottomRight bool
	EqualHor, EqualVer   bool
}

// Gravity is a resolved placement: for GravityFocusPoint, X and Y are the
// focus point (0..1); otherwise offsets (|v| ≥ 1 pixels, scaled by the
// step's OffsetScale; |v| < 1 relative to the outer size).
type Gravity struct {
	Type transform.GravityType
	X, Y float64
}

// Interesting selects the smart-crop strategy.
type Interesting uint8

// Interesting values.
const (
	InterestingNone      Interesting = iota // gravity placement
	InterestingAttention                    // libvips attention (imgproxy sm)
	InterestingEntropy                      // libvips entropy
)

// CropGeometry is a crop of a W×H area placed by gravity.
type CropGeometry struct {
	// X, Y, W, H are the predicted rectangle.
	X, Y, W, H int
	// Width and Height are the requested size; 0 means the image size on
	// that axis.
	Width, Height int
	Gravity       Gravity
	OffsetScale   float64 // DPR scale applied to absolute offsets
	// Smart crops by content (Rect then only gives the size).
	Smart Interesting
}

// PreCropStep crops in source space before resizing (imgproxy crop).
type PreCropStep struct{ CropGeometry }

// CropStep crops the resized image to the result size.
type CropStep struct{ CropGeometry }

// ResizeStep scales the image.
type ResizeStep struct {
	ScaleX, ScaleY float64
	Kernel         transform.Kernel
	Premultiply    bool // the image has alpha: resize premultiplied
	W, H           int  // predicted result
}

// RotateStep rotates clockwise by a multiple of 90.
type RotateStep struct{ Angle int }

// FlipStep mirrors the image.
type FlipStep struct{ Horizontal, Vertical bool }

// FilterStep applies the filters of a pipeline in one pass, per frame, in
// this order: Blur, Sharpen, Unsharp, Pixelate, BlurAreas, ProgressiveBlur
// (imgproxy applies blur, sharpen and pixelate in one pass; the Pro filters
// follow on the result, before colour adjustments, the canvas and
// watermarks).
type FilterStep struct {
	Blur     float64 // Gaussian sigma; 0 = off
	Sharpen  float64 // sigma; 0 = off
	Pixelate int     // block size; ≤ 1 = off
	// Unsharp is the resolved unsharp mask; nil = off (mode none, or auto
	// without a downscale or with Sharpen set).
	Unsharp *UnsharpMask
	// BlurAreas are rectangles relative to the frame at this step; nil or
	// no areas = off.
	BlurAreas *BlurAreas
	// ProgressiveBlur blends a blurred copy in along a linear ramp; nil =
	// off.
	ProgressiveBlur *transform.ProgressiveBlur
}

// UnsharpMask is a resolved transform.Unsharp. With Sigma > 0 it is the
// classic mask img + Amount × (img − gaussblur(img, Sigma)) (imgix usm,
// ImageKit e-usm); otherwise it is imgproxy's unsharp_masking kernel, the
// 3×3 convolution
//
//	−w      −w      −w
//	−w   d + 8w     −w   ÷ d
//	−w      −w      −w
//
// with w = Weight and d = Divider: img + 8w/d × (img − mean of the 8
// neighbours).
type UnsharpMask struct {
	Weight, Divider float64
	Sigma, Amount   float64
}

// BlurAreas blurs rectangles of the frame with a Gaussian of Sigma. The
// areas are fractions of the frame at the FilterStep (the planner maps the
// request's source-relative areas through crops, rotations and flips) and
// lie within 0..1.
type BlurAreas struct {
	Sigma float64
	Areas []transform.Area
}

// ColourAdjustStep applies the colour adjustments and effects of a pipeline
// in one pass, per frame, right after FilterStep: before the canvas grows,
// so extend and padding backgrounds keep their exact colour. The executor
// applies them in this order: Adjust tone curves (brightness, contrast,
// gamma, exposure, red/green/blue), Adjust saturation and hue, Grayscale,
// Sepia, Tint, Monochrome, Duotone, Negate, Colorize, Gradient, then
// Adjust.Opacity. Adjust.Vibrance is not applied (Plan.Unplanned).
type ColourAdjustStep struct {
	Adjust     transform.Adjust
	Grayscale  bool
	Sepia      bool
	Negate     bool
	Tint       *transform.Color
	Monochrome *transform.Monochrome
	Duotone    *transform.Duotone
	Colorize   *transform.Colorize
	Gradient   *transform.Gradient
}

// ExtendGeometry grows the canvas to Width×Height (0 = keep that axis),
// placing the image by gravity.
type ExtendGeometry struct {
	// X, Y are the predicted image position, W, H the canvas.
	X, Y, W, H    int
	Width, Height int
	Gravity       Gravity
	OffsetScale   float64
}

// ExtendStep extends the canvas (imgproxy extend, extend_aspect_ratio).
type ExtendStep struct {
	ExtendGeometry
	AspectRatio bool // extend_aspect_ratio rather than extend
	Mode        transform.ExtendMode
	Background  *transform.Color // nil = transparent (or white when flattened)
}

// PaddingStep adds space around the image (already scaled by DPR).
type PaddingStep struct {
	Top, Right, Bottom, Left int
	Background               *transform.Color
}

// FlattenStep removes alpha over a background colour. A translucent
// Background (A < 255, imgproxy background_alpha) keeps the alpha band: the
// image is composited over the background instead.
type FlattenStep struct{ Background transform.Color }

// WatermarkStep overlays a watermark on the whole image, every frame of an
// animation (imgproxy applyWatermark).
type WatermarkStep struct {
	WatermarkGeometry
	Kind WatermarkKind
	// Pipeline and Index identify the watermark in the request
	// (Pipelines[Pipeline].Watermarks[Index]): the executor finds a
	// per-request image by them.
	Pipeline int
	Index    int
	Opacity  float64 // the request's opacity (0..1), before the configured one
	Frames   int     // frames of the strip it is applied to
	// Text is set when Kind is WatermarkText.
	Text *TextWatermark
}

// WatermarkKind is what a watermark draws.
type WatermarkKind uint8

// WatermarkKind values.
const (
	// WatermarkDefault is the configured image
	// (XI_PROCESSING_WATERMARK_SOURCE): no source or text of its own.
	WatermarkDefault WatermarkKind = iota
	// WatermarkImage is the request's own image (imgproxy watermark_url),
	// fetched by the server like a source.
	WatermarkImage
	// WatermarkText is rendered Pango markup (imgproxy watermark_text).
	WatermarkText
)

// TextWatermark is a text watermark to render. Markup and Font come from
// the request unsanitised; the executor sanitises them.
type TextWatermark struct {
	Markup string
	Font   string           // "" = the configured font
	Color  *transform.Color // nil = the markup's (black by default)
	Width  int              // wrap width in pixels (DPR applied); 0 = the frame width
	DPI    int              // 72 × DPR: one point per CSS pixel
}

// WatermarkGeometry sizes and places a watermark. Its methods reproduce
// imgproxy's prepareWatermark and calcPosition; the executor applies them
// to the real frame size.
type WatermarkGeometry struct {
	Position Gravity // placement and offsets; offsets are the tile spacing when tiled
	Tile     transform.TileMode
	Scale    float64 // fraction of the frame to fit the watermark into; 0 = natural size
	DPRScale float64 // scales pixel offsets
	// Width and Height (pixels, DPR applied) are the box the watermark is
	// fitted into, enlarging, when Scale is 0 (imgproxy watermark_size);
	// 0 is derived from the other side and the aspect ratio.
	Width, Height int
	Rotate        float64 // degrees clockwise, after sizing
	Shadow        float64 // drop-shadow sigma in pixels (DPR applied); 0 = none
}

func (LoadStep) step()         {}
func (ColourStep) step()       {}
func (TrimStep) step()         {}
func (PreCropStep) step()      {}
func (CropStep) step()         {}
func (ResizeStep) step()       {}
func (RotateStep) step()       {}
func (FlipStep) step()         {}
func (FilterStep) step()       {}
func (ColourAdjustStep) step() {}
func (ExtendStep) step()       {}
func (PaddingStep) step()      {}
func (FlattenStep) step()      {}
func (WatermarkStep) step()    {}

// Box is the box a scaled watermark is fitted into (imgproxy
// prepareWatermark: fit, enlarge allowed, relative to one frame): each side
// is Scale × the frame side, rounded to even and at least 1. ok is false
// when Scale is 0 (natural size).
func (w *WatermarkGeometry) Box(frameW, frameH int) (bw, bh int, ok bool) {
	if w.Scale <= 0 {
		return 0, 0, false
	}
	return max(scaleDimEven(frameW, w.Scale), 1), max(scaleDimEven(frameH, w.Scale), 1), true
}

// FitScale is the resize factor that fits a wmW×wmH watermark into Box,
// or into Width×Height when there is no box (1 when there is neither).
func (w *WatermarkGeometry) FitScale(frameW, frameH, wmW, wmH int) float64 {
	if wmW <= 0 || wmH <= 0 {
		return 1
	}
	if bw, bh, ok := w.Box(frameW, frameH); ok {
		return 1 / max(float64(wmW)/float64(bw), float64(wmH)/float64(bh))
	}
	sx, sy := float64(w.Width)/float64(wmW), float64(w.Height)/float64(wmH)
	switch {
	case w.Width > 0 && w.Height > 0:
		return min(sx, sy)
	case w.Width > 0:
		return sx
	case w.Height > 0:
		return sy
	default:
		return 1
	}
}

// TileSpacing is the padding added around a tiled watermark (imgproxy
// splits it left/right and top/bottom): the offsets, in pixels scaled by
// DPRScale when |offset| ≥ 1, else as a fraction of the frame.
func (w *WatermarkGeometry) TileSpacing(frameW, frameH int) (x, y int) {
	return offset(frameW, w.Position.X, w.DPRScale), offset(frameH, w.Position.Y, w.DPRScale)
}

// Place is where a wmW×wmH watermark goes on a frameW×frameH frame
// (imgproxy calcPosition with overflow: it may stick out of the frame but
// keeps at least one pixel inside).
func (w *WatermarkGeometry) Place(frameW, frameH, wmW, wmH int) (left, top int) {
	return positionOverflow(frameW, frameH, wmW, wmH, w.Position, w.DPRScale)
}

// Rect places the crop on an imgW×imgH image, exactly like imgproxy's
// cropImage. ok is false when the crop does nothing.
func (c *CropGeometry) Rect(imgW, imgH int) (x, y, w, h int, ok bool) {
	if c.Width == 0 && c.Height == 0 {
		return 0, 0, imgW, imgH, false
	}
	w = minNonZero(c.Width, imgW)
	h = minNonZero(c.Height, imgH)
	if w >= imgW && h >= imgH {
		return 0, 0, imgW, imgH, false
	}
	if c.Smart != InterestingNone {
		return 0, 0, w, h, true
	}
	x, y = position(imgW, imgH, w, h, c.Gravity, c.OffsetScale)
	return x, y, w, h, true
}

// Rect places an imgW×imgH image on the canvas, exactly like imgproxy's
// extendImage. ok is false when nothing grows.
func (e *ExtendGeometry) Rect(imgW, imgH int) (x, y, w, h int, ok bool) {
	w, h = e.Width, e.Height
	if w <= imgW && h <= imgH {
		return 0, 0, imgW, imgH, false
	}
	if w <= 0 {
		w = imgW
	}
	if h <= 0 {
		h = imgH
	}
	x, y = position(w, h, imgW, imgH, e.Gravity, e.OffsetScale)
	return x, y, w, h, true
}
