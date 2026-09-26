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
	Format      string // source format name: "jpeg", "png", "webp", "gif", "svg", "pdf", "heic", "avif", ...
	IsVector    bool   // SVG, PDF: rendered at any scale
}

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
	// DisableShrinkOnLoad turns off shrink-on-load for raster formats.
	DisableShrinkOnLoad bool
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
// materialised result of the previous plan.
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
	// Chained plans follow this one; their Load is zero.
	Chained []*Plan
	// Output is the result of this plan (the last chained plan's is the
	// response).
	Output Output
	// Cost is the peak memory estimate of this plan and its chained plans.
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
	ShrinkOnLoad int
	// Scale is the WebP/SVG/PDF scale-on-load factor; 0 is off.
	Scale float64
	// Density is the vector rendering DPI; 0 keeps the loader default.
	Density float64
	Page    int // first page or frame
	NPages  int // pages or frames to load (1 = one)
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

// FilterStep applies imgproxy's filters in one pass.
type FilterStep struct {
	Blur     float64 // Gaussian sigma; 0 = off
	Sharpen  float64 // sigma; 0 = off
	Pixelate int     // block size; ≤ 1 = off
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

// FlattenStep removes alpha over a background colour.
type FlattenStep struct{ Background transform.Color }

// WatermarkStep overlays a watermark on the whole image, every frame of an
// animation (imgproxy applyWatermark).
type WatermarkStep struct {
	WatermarkGeometry
	Index   int     // index into the pipeline's Watermarks
	Opacity float64 // the request's opacity (0..1), before the configured one
	Frames  int     // frames of the strip it is applied to
	// Default is set when the watermark is the configured image
	// (XI_PROCESSING_WATERMARK_SOURCE): no source or text of its own.
	Default bool
}

// WatermarkGeometry sizes and places a watermark. Its methods reproduce
// imgproxy's prepareWatermark and calcPosition; the executor applies them
// to the real frame size.
type WatermarkGeometry struct {
	Position Gravity // placement and offsets; offsets are the tile spacing when tiled
	Tile     transform.TileMode
	Scale    float64 // fraction of the frame to fit the watermark into; 0 = natural size
	DPRScale float64 // scales pixel offsets
}

func (LoadStep) step()      {}
func (ColourStep) step()    {}
func (TrimStep) step()      {}
func (PreCropStep) step()   {}
func (CropStep) step()      {}
func (ResizeStep) step()    {}
func (RotateStep) step()    {}
func (FlipStep) step()      {}
func (FilterStep) step()    {}
func (ExtendStep) step()    {}
func (PaddingStep) step()   {}
func (FlattenStep) step()   {}
func (WatermarkStep) step() {}

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

// FitScale is the resize factor that fits a wmW×wmH watermark into Box
// (1 when there is no box).
func (w *WatermarkGeometry) FitScale(frameW, frameH, wmW, wmH int) float64 {
	bw, bh, ok := w.Box(frameW, frameH)
	if !ok || wmW <= 0 || wmH <= 0 {
		return 1
	}
	return 1 / max(float64(wmW)/float64(bw), float64(wmH)/float64(bh))
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
