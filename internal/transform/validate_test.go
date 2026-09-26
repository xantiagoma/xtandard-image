package transform

import (
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// validBase is a defaulted request with every optional section present and
// valid, so each table case breaks exactly one thing.
func validBase() Request {
	r := Request{
		Source: SourceSpec{Raw: "https://example.com/a.jpg", Hashsum: &Hashsum{Algorithm: HashMD5, Digest: strings.Repeat("0f", 16)}},
		Pipelines: []Pipeline{{
			Resize:          Resize{Width: Dim{Px: 100}, Height: Dim{Rel: 0.5}},
			Gravity:         Gravity{Type: GravityObjectWeighted, Classes: []string{"dog"}, Weights: []float64{1}, FaceIndex: new(0), ObjectsPosition: &ObjectsPosition{Type: GravityNorth}},
			Crop:            &Crop{Width: Dim{Px: 10}, Height: Dim{Px: 10}},
			CropObjects:     &CropObjects{Scale: 1, Classes: []string{"cat"}},
			Trim:            &Trim{Threshold: 10},
			Padding:         &Padding{Top: 1},
			Border:          &Border{Top: 1},
			Radius:          &Radius{TopLeft: 1},
			Mask:            &Mask{Shape: MaskCircle},
			Adjust:          Adjust{Brightness: new(10.0), Gamma: new(2.0)},
			Monochrome:      &Monochrome{Intensity: 0.5},
			Duotone:         &Duotone{Intensity: 0.5},
			Colorize:        &Colorize{Opacity: 0.5},
			Gradient:        &Gradient{Opacity: 0.5, Start: 0, Stop: 1},
			Unsharp:         &Unsharp{Mode: UnsharpAuto, Weight: 1, Divider: 24},
			BlurAreas:       &BlurAreas{Sigma: 1, Areas: []Area{{0, 0, 0.5, 0.5}}},
			ProgressiveBlur: &ProgressiveBlur{Sigma: 1, Stop: 1},
			BlurDetections:  &DetectionFilter{Sigma: 1, Classes: []string{"face"}},
			DrawDetections:  &DetectionFilter{},
			Watermarks:      []Watermark{{Opacity: 1, Source: &SourceSpec{Raw: "local:///wm.png"}}},
			Page:            new(0),
			Pages:           new(1),
			Video: &VideoOptions{
				Second:    new(1.0),
				Tile:      &VideoTile{Columns: 1, Rows: 1},
				Animation: &VideoAnimation{Frames: 1},
			},
		}},
		Output: Output{
			Quality:       80,
			FormatQuality: map[Format]int{FormatJPEG: 80},
			AutoQuality:   &AutoQuality{Method: AutoQualityDSSIM, Min: 10, Max: 90},
			JPEG:          &JPEGOptions{QuantTable: new(0)},
			PNG:           &PNGOptions{QuantizationColors: 256, Compression: new(9)},
			WebP:          &WebPOptions{},
			AVIF:          &AVIFOptions{Subsample: Subsampling420},
		},
		Delivery: Delivery{Fallback: &SourceSpec{Raw: "local:///f.png"}, SkipProcessing: []Format{FormatSVG}, CacheTags: []string{"t"}},
	}
	r.ApplyDefaults(StandardDefaults())
	return r
}

func invalidFields(err error) []string {
	var out []string
	var walk func(error)
	walk = func(e error) {
		if ve, ok := e.(*ValidationError); ok { //nolint:errorlint // walking a Join tree by hand, one level at a time
			out = append(out, ve.Field)
			return
		}
		if j, ok := e.(interface{ Unwrap() []error }); ok {
			for _, c := range j.Unwrap() {
				walk(c)
			}
		}
	}
	if err != nil {
		walk(err)
	}
	return out
}

func TestValidateBaseIsValid(t *testing.T) {
	t.Parallel()
	r := validBase()
	if err := r.Validate(); err != nil {
		t.Fatalf("validBase: %v", err)
	}
	info := Request{Kind: KindInfo, Source: src("x"), Info: &InfoOptions{
		Crop: &Crop{Width: Dim{Px: 1}}, Palette: 2, Blurhash: &InfoBlurhash{XComponents: 9, YComponents: 1},
		CalcHashsums: []HashAlgorithm{HashSHA512},
	}}
	info.ApplyDefaults(StandardDefaults())
	if err := info.Validate(); err != nil {
		t.Fatalf("valid info request: %v", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	type mut = func(r *Request)
	p0 := func(f func(p *Pipeline)) mut { return func(r *Request) { f(&r.Pipelines[0]) } }
	nan := math.NaN()
	inf := math.Inf(1)
	tests := []struct {
		name  string
		mut   mut
		field string
	}{
		// request
		{"kind unknown", func(r *Request) { r.Kind = numKinds }, "kind"},
		{"info without kind info", func(r *Request) { r.Info = &InfoOptions{} }, "info"},
		{"no pipelines", func(r *Request) { r.Pipelines = nil }, "pipelines"},
		{"too many pipelines", func(r *Request) { r.Pipelines = make([]Pipeline, MaxPipelines+1) }, "pipelines"},
		{"client hints width step negative", func(r *Request) { r.ClientHints.WidthStep = -1 }, "client_hints.width_step"},
		{"client hints width step huge", func(r *Request) { r.ClientHints.WidthStep = MaxDimension + 1 }, "client_hints.width_step"},

		// source
		{"source empty", func(r *Request) { r.Source.Raw = "" }, "source.raw"},
		{"source encoding unknown", func(r *Request) { r.Source.Encoding = numSourceEncodings }, "source.encoding"},
		{"hashsum algorithm unset", func(r *Request) { r.Source.Hashsum.Algorithm = HashUnset }, "source.hashsum.algorithm"},
		{"hashsum algorithm unknown", func(r *Request) { r.Source.Hashsum.Algorithm = numHashAlgorithms }, "source.hashsum.algorithm"},
		{"hashsum digest short", func(r *Request) { r.Source.Hashsum.Digest = "0f" }, "source.hashsum.digest"},
		{"hashsum digest upper", func(r *Request) { r.Source.Hashsum.Digest = strings.Repeat("0F", 16) }, "source.hashsum.digest"},
		{"hashsum digest non hex", func(r *Request) { r.Source.Hashsum.Digest = strings.Repeat("zz", 16) }, "source.hashsum.digest"},

		// resize
		{"resize type unset", p0(func(p *Pipeline) { p.Resize.Type = ResizeUnset }), "pipelines[0].resize.type"},
		{"resize type unknown", p0(func(p *Pipeline) { p.Resize.Type = numResizeTypes }), "pipelines[0].resize.type"},
		{"width px negative", p0(func(p *Pipeline) { p.Resize.Width.Px = -1 }), "pipelines[0].resize.width.px"},
		{"width px huge", p0(func(p *Pipeline) { p.Resize.Width.Px = MaxDimension + 1 }), "pipelines[0].resize.width.px"},
		{"width rel negative", p0(func(p *Pipeline) { p.Resize.Width = Dim{Rel: -0.5} }), "pipelines[0].resize.width.rel"},
		{"width rel huge", p0(func(p *Pipeline) { p.Resize.Width = Dim{Rel: MaxRelative + 1} }), "pipelines[0].resize.width.rel"},
		{"width rel nan", p0(func(p *Pipeline) { p.Resize.Width = Dim{Rel: nan} }), "pipelines[0].resize.width.rel"},
		{"width px and rel", p0(func(p *Pipeline) { p.Resize.Width = Dim{Px: 1, Rel: 0.5} }), "pipelines[0].resize.width"},
		{"height px negative", p0(func(p *Pipeline) { p.Resize.Height = Dim{Px: -1} }), "pipelines[0].resize.height.px"},
		{"min width negative", p0(func(p *Pipeline) { p.Resize.MinWidth = -1 }), "pipelines[0].resize.min_width"},
		{"min height huge", p0(func(p *Pipeline) { p.Resize.MinHeight = MaxDimension + 1 }), "pipelines[0].resize.min_height"},
		{"max width negative", p0(func(p *Pipeline) { p.Resize.MaxWidth = -1 }), "pipelines[0].resize.max_width"},
		{"max height huge", p0(func(p *Pipeline) { p.Resize.MaxHeight = MaxDimension + 1 }), "pipelines[0].resize.max_height"},
		{"aspect ratio negative", p0(func(p *Pipeline) { p.Resize.AspectRatio = -1 }), "pipelines[0].resize.aspect_ratio"},
		{"aspect ratio inf", p0(func(p *Pipeline) { p.Resize.AspectRatio = inf }), "pipelines[0].resize.aspect_ratio"},
		{"zoom x zero", p0(func(p *Pipeline) { p.Resize.ZoomX = 0 }), "pipelines[0].resize.zoom_x"},
		{"zoom y huge", p0(func(p *Pipeline) { p.Resize.ZoomY = MaxFactor + 1 }), "pipelines[0].resize.zoom_y"},
		{"dpr negative", p0(func(p *Pipeline) { p.Resize.DPR = -1 }), "pipelines[0].resize.dpr"},
		{"dpr nan", p0(func(p *Pipeline) { p.Resize.DPR = nan }), "pipelines[0].resize.dpr"},
		{"algorithm unset", p0(func(p *Pipeline) { p.Resize.Algorithm = KernelUnset }), "pipelines[0].resize.algorithm"},
		{"algorithm unknown", p0(func(p *Pipeline) { p.Resize.Algorithm = numKernels }), "pipelines[0].resize.algorithm"},

		// gravity
		{"gravity unknown", p0(func(p *Pipeline) { p.Gravity.Type = numGravityTypes }), "pipelines[0].gravity.type"},
		{"gravity x offset huge", p0(func(p *Pipeline) { p.Gravity.XOffset = MaxDimension + 1 }), "pipelines[0].gravity.x_offset"},
		{"gravity y offset nan", p0(func(p *Pipeline) { p.Gravity.YOffset = nan }), "pipelines[0].gravity.y_offset"},
		{"focus x above 1", p0(func(p *Pipeline) { p.Gravity.FocusX = 1.1 }), "pipelines[0].gravity.focus_x"},
		{"focus y negative", p0(func(p *Pipeline) { p.Gravity.FocusY = -0.1 }), "pipelines[0].gravity.focus_y"},
		{"focus zoom negative", p0(func(p *Pipeline) { p.Gravity.FocusZoom = -1 }), "pipelines[0].gravity.focus_zoom"},
		{"focus px without focus point", p0(func(p *Pipeline) { p.Gravity.FocusPx = &FocusRect{X2: 1, Y2: 1} }), "pipelines[0].gravity.focus_px"},
		{"focus px with fraction", p0(func(p *Pipeline) {
			p.Gravity = Gravity{Type: GravityFocusPoint, FocusX: 0.5, FocusPx: &FocusRect{}}
		}), "pipelines[0].gravity.focus_px"},
		{"focus px negative", p0(func(p *Pipeline) {
			p.Gravity = Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{X1: -1}}
		}), "pipelines[0].gravity.focus_px.x1"},
		{"focus px y2 above max", p0(func(p *Pipeline) {
			p.Gravity = Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{Y2: MaxSourceCoord + 1}}
		}), "pipelines[0].gravity.focus_px.y2"},
		{"focus px corners reversed", p0(func(p *Pipeline) {
			p.Gravity = Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{X1: 10, X2: 5}}
		}), "pipelines[0].gravity.focus_px.x2"},
		{"crop focus px reversed", p0(func(p *Pipeline) {
			p.Crop.Gravity = Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{Y1: 10, Y2: 5}}
		}), "pipelines[0].crop.gravity.focus_px.y2"},
		{"extend focus px", p0(func(p *Pipeline) {
			p.Extend.Gravity = Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{}}
		}), "pipelines[0].extend.gravity.focus_px"},
		{"watermark focus px", p0(func(p *Pipeline) {
			p.Watermarks[0].Position = Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{}}
		}), "pipelines[0].watermarks[0].position.focus_px"},
		{"class empty", p0(func(p *Pipeline) { p.Gravity.Classes = []string{""} }), "pipelines[0].gravity.classes[0]"},
		{"too many classes", p0(func(p *Pipeline) {
			p.Gravity.Classes = slices.Repeat([]string{"dog"}, MaxListLen+1)
			p.Gravity.Weights = nil
		}), "pipelines[0].gravity.classes"},
		{"weights mismatch", p0(func(p *Pipeline) { p.Gravity.Weights = []float64{1, 2} }), "pipelines[0].gravity.weights"},
		{"weight negative", p0(func(p *Pipeline) { p.Gravity.Weights = []float64{-1} }), "pipelines[0].gravity.weights[0]"},
		{"face index negative", p0(func(p *Pipeline) { p.Gravity.FaceIndex = new(-1) }), "pipelines[0].gravity.face_index"},
		{"face pad negative", p0(func(p *Pipeline) { p.Gravity.FacePad = -1 }), "pipelines[0].gravity.face_pad"},
		{"objects position type smart", p0(func(p *Pipeline) { p.Gravity.ObjectsPosition.Type = GravitySmart }), "pipelines[0].gravity.objects_position.type"},
		{"objects position x huge", p0(func(p *Pipeline) { p.Gravity.ObjectsPosition.X = inf }), "pipelines[0].gravity.objects_position.x"},
		{"objects position y huge", p0(func(p *Pipeline) { p.Gravity.ObjectsPosition.Y = -inf }), "pipelines[0].gravity.objects_position.y"},

		// crop, trim, rotation, padding
		{"crop width negative", p0(func(p *Pipeline) { p.Crop.Width.Px = -1 }), "pipelines[0].crop.width.px"},
		{"crop height rel huge", p0(func(p *Pipeline) { p.Crop.Height = Dim{Rel: MaxRelative * 2} }), "pipelines[0].crop.height.rel"},
		{"crop gravity unknown", p0(func(p *Pipeline) { p.Crop.Gravity.Type = numGravityTypes }), "pipelines[0].crop.gravity.type"},
		{"crop aspect ratio negative", p0(func(p *Pipeline) { p.Crop.AspectRatio = -1 }), "pipelines[0].crop.aspect_ratio"},
		{"crop objects scale negative", p0(func(p *Pipeline) { p.CropObjects.Scale = -1 }), "pipelines[0].crop_objects.scale"},
		{"crop objects class empty", p0(func(p *Pipeline) { p.CropObjects.Classes = []string{""} }), "pipelines[0].crop_objects.classes[0]"},
		{"crop objects class unknown", p0(func(p *Pipeline) { p.CropObjects.Classes = []string{"unicorn"} }), "pipelines[0].crop_objects.classes[0]"},
		{"crop objects index negative", p0(func(p *Pipeline) { p.CropObjects.Index = new(-1) }), "pipelines[0].crop_objects.index"},
		{"crop objects padding negative", p0(func(p *Pipeline) { p.CropObjects.Padding = -1 }), "pipelines[0].crop_objects.padding"},
		{"gravity class unknown", p0(func(p *Pipeline) { p.Gravity.Classes = []string{"dog", "dragon"} }), "pipelines[0].gravity.classes[1]"},
		{"blur detections class unknown", p0(func(p *Pipeline) { p.BlurDetections.Classes = []string{"ghost"} }), "pipelines[0].blur_detections.classes[0]"},
		{"trim threshold negative", p0(func(p *Pipeline) { p.Trim.Threshold = -1 }), "pipelines[0].trim.threshold"},
		{"trim threshold huge", p0(func(p *Pipeline) { p.Trim.Threshold = 256 }), "pipelines[0].trim.threshold"},
		{"rotate not multiple of 90", p0(func(p *Pipeline) { p.Rotate = 45 }), "pipelines[0].rotate"},
		{"rotate negative", p0(func(p *Pipeline) { p.Rotate = -90 }), "pipelines[0].rotate"},
		{"rotate 360", p0(func(p *Pipeline) { p.Rotate = 360 }), "pipelines[0].rotate"},
		{"rotate any -180", p0(func(p *Pipeline) { p.RotateAny = -180 }), "pipelines[0].rotate_any"},
		{"rotate any above 180", p0(func(p *Pipeline) { p.RotateAny = 181 }), "pipelines[0].rotate_any"},
		{"rotate any nan", p0(func(p *Pipeline) { p.RotateAny = nan }), "pipelines[0].rotate_any"},
		{"padding top negative", p0(func(p *Pipeline) { p.Padding.Top = -1 }), "pipelines[0].padding.top"},
		{"padding right huge", p0(func(p *Pipeline) { p.Padding.Right = MaxDimension + 1 }), "pipelines[0].padding.right"},
		{"padding bottom negative", p0(func(p *Pipeline) { p.Padding.Bottom = -1 }), "pipelines[0].padding.bottom"},
		{"padding left negative", p0(func(p *Pipeline) { p.Padding.Left = -1 }), "pipelines[0].padding.left"},

		// extend
		{"extend gravity smart", p0(func(p *Pipeline) { p.Extend.Gravity.Type = GravitySmart }), "pipelines[0].extend.gravity.type"},
		{"extend gravity unknown", p0(func(p *Pipeline) { p.Extend.Gravity.Type = numGravityTypes }), "pipelines[0].extend.gravity.type"},
		{"extend mode unknown", p0(func(p *Pipeline) { p.Extend.Mode = numExtendModes }), "pipelines[0].extend.mode"},
		{"extend ar gravity face", p0(func(p *Pipeline) { p.ExtendAR.Gravity.Type = GravityFace }), "pipelines[0].extend_ar.gravity.type"},

		// border, radius, mask
		{"border top negative", p0(func(p *Pipeline) { p.Border.Top = -1 }), "pipelines[0].border.top"},
		{"border right huge", p0(func(p *Pipeline) { p.Border.Right = MaxDimension + 1 }), "pipelines[0].border.right"},
		{"border bottom negative", p0(func(p *Pipeline) { p.Border.Bottom = -1 }), "pipelines[0].border.bottom"},
		{"border left negative", p0(func(p *Pipeline) { p.Border.Left = -1 }), "pipelines[0].border.left"},
		{"radius top left negative", p0(func(p *Pipeline) { p.Radius.TopLeft = -1 }), "pipelines[0].radius.top_left"},
		{"radius top right negative", p0(func(p *Pipeline) { p.Radius.TopRight = -1 }), "pipelines[0].radius.top_right"},
		{"radius bottom right huge", p0(func(p *Pipeline) { p.Radius.BottomRight = MaxDimension + 1 }), "pipelines[0].radius.bottom_right"},
		{"radius bottom left negative", p0(func(p *Pipeline) { p.Radius.BottomLeft = -1 }), "pipelines[0].radius.bottom_left"},
		{"mask shape unset", p0(func(p *Pipeline) { p.Mask.Shape = MaskUnset }), "pipelines[0].mask.shape"},
		{"mask shape unknown", p0(func(p *Pipeline) { p.Mask.Shape = numMaskShapes }), "pipelines[0].mask.shape"},

		// adjust
		{"brightness below", p0(func(p *Pipeline) { p.Adjust.Brightness = new(-256.0) }), "pipelines[0].adjust.brightness"},
		{"brightness above", p0(func(p *Pipeline) { p.Adjust.Brightness = new(256.0) }), "pipelines[0].adjust.brightness"},
		{"contrast negative", p0(func(p *Pipeline) { p.Adjust.Contrast = new(-1.0) }), "pipelines[0].adjust.contrast"},
		{"saturation nan", p0(func(p *Pipeline) { p.Adjust.Saturation = new(nan) }), "pipelines[0].adjust.saturation"},
		{"gamma zero", p0(func(p *Pipeline) { p.Adjust.Gamma = new(0.0) }), "pipelines[0].adjust.gamma"},
		{"hue above", p0(func(p *Pipeline) { p.Adjust.Hue = new(361.0) }), "pipelines[0].adjust.hue"},
		{"exposure negative", p0(func(p *Pipeline) { p.Adjust.Exposure = new(-1.0) }), "pipelines[0].adjust.exposure"},
		{"vibrance huge", p0(func(p *Pipeline) { p.Adjust.Vibrance = new(MaxFactor + 1) }), "pipelines[0].adjust.vibrance"},
		{"red negative", p0(func(p *Pipeline) { p.Adjust.Red = new(-1.0) }), "pipelines[0].adjust.red"},
		{"green negative", p0(func(p *Pipeline) { p.Adjust.Green = new(-1.0) }), "pipelines[0].adjust.green"},
		{"blue negative", p0(func(p *Pipeline) { p.Adjust.Blue = new(-1.0) }), "pipelines[0].adjust.blue"},
		{"opacity above 1", p0(func(p *Pipeline) { p.Adjust.Opacity = new(1.5) }), "pipelines[0].adjust.opacity"},

		// colour filters
		{"monochrome intensity above 1", p0(func(p *Pipeline) { p.Monochrome.Intensity = 2 }), "pipelines[0].monochrome.intensity"},
		{"duotone intensity negative", p0(func(p *Pipeline) { p.Duotone.Intensity = -1 }), "pipelines[0].duotone.intensity"},
		{"colorize opacity above 1", p0(func(p *Pipeline) { p.Colorize.Opacity = 1.1 }), "pipelines[0].colorize.opacity"},
		{"gradient opacity negative", p0(func(p *Pipeline) { p.Gradient.Opacity = -0.1 }), "pipelines[0].gradient.opacity"},
		{"gradient direction unknown", p0(func(p *Pipeline) { p.Gradient.Direction = numDirections }), "pipelines[0].gradient.direction"},
		{"gradient angle huge", p0(func(p *Pipeline) { p.Gradient.Angle = 361 }), "pipelines[0].gradient.angle"},
		{"gradient start above 1", p0(func(p *Pipeline) { p.Gradient.Start = 2 }), "pipelines[0].gradient.start"},
		{"gradient stop negative", p0(func(p *Pipeline) { p.Gradient.Stop = -1 }), "pipelines[0].gradient.stop"},

		// blur and sharpen
		{"blur negative", p0(func(p *Pipeline) { p.Blur = -1 }), "pipelines[0].blur"},
		{"blur huge", p0(func(p *Pipeline) { p.Blur = MaxSigma + 1 }), "pipelines[0].blur"},
		{"sharpen negative", p0(func(p *Pipeline) { p.Sharpen = -1 }), "pipelines[0].sharpen"},
		{"sharpen inf", p0(func(p *Pipeline) { p.Sharpen = inf }), "pipelines[0].sharpen"},
		{"unsharp mode unknown", p0(func(p *Pipeline) { p.Unsharp.Mode = numUnsharpModes }), "pipelines[0].unsharp.mode"},
		{"unsharp weight negative", p0(func(p *Pipeline) { p.Unsharp.Weight = -1 }), "pipelines[0].unsharp.weight"},
		{"unsharp divider negative", p0(func(p *Pipeline) { p.Unsharp.Divider = -1 }), "pipelines[0].unsharp.divider"},
		{"unsharp sigma negative", p0(func(p *Pipeline) { p.Unsharp.Sigma = -1 }), "pipelines[0].unsharp.sigma"},
		{"unsharp amount huge", p0(func(p *Pipeline) { p.Unsharp.Amount = MaxFactor + 1 }), "pipelines[0].unsharp.amount"},
		{"pixelate negative", p0(func(p *Pipeline) { p.Pixelate = -1 }), "pipelines[0].pixelate"},
		{"pixelate huge", p0(func(p *Pipeline) { p.Pixelate = MaxDimension + 1 }), "pipelines[0].pixelate"},
		{"blur areas sigma negative", p0(func(p *Pipeline) { p.BlurAreas.Sigma = -1 }), "pipelines[0].blur_areas.sigma"},
		{"too many blur areas", p0(func(p *Pipeline) { p.BlurAreas.Areas = make([]Area, MaxBlurAreas+1) }), "pipelines[0].blur_areas.areas"},
		{"blur area left above 1", p0(func(p *Pipeline) { p.BlurAreas.Areas[0].Left = 1.5 }), "pipelines[0].blur_areas.areas[0].left"},
		{"blur area top negative", p0(func(p *Pipeline) { p.BlurAreas.Areas[0].Top = -0.5 }), "pipelines[0].blur_areas.areas[0].top"},
		{"blur area width above 1", p0(func(p *Pipeline) { p.BlurAreas.Areas[0].Width = 2 }), "pipelines[0].blur_areas.areas[0].width"},
		{"blur area height nan", p0(func(p *Pipeline) { p.BlurAreas.Areas[0].Height = nan }), "pipelines[0].blur_areas.areas[0].height"},
		{"progressive blur sigma negative", p0(func(p *Pipeline) { p.ProgressiveBlur.Sigma = -1 }), "pipelines[0].progressive_blur.sigma"},
		{"progressive blur direction unknown", p0(func(p *Pipeline) { p.ProgressiveBlur.Direction = numDirections }), "pipelines[0].progressive_blur.direction"},
		{"progressive blur angle huge", p0(func(p *Pipeline) { p.ProgressiveBlur.Angle = -361 }), "pipelines[0].progressive_blur.angle"},
		{"progressive blur start negative", p0(func(p *Pipeline) { p.ProgressiveBlur.Start = -1 }), "pipelines[0].progressive_blur.start"},
		{"progressive blur stop above 1", p0(func(p *Pipeline) { p.ProgressiveBlur.Stop = 2 }), "pipelines[0].progressive_blur.stop"},
		{"blur detections sigma negative", p0(func(p *Pipeline) { p.BlurDetections.Sigma = -1 }), "pipelines[0].blur_detections.sigma"},
		{"blur detections pixelate negative", p0(func(p *Pipeline) { p.BlurDetections.Pixelate = -1 }), "pipelines[0].blur_detections.pixelate"},
		{"draw detections class empty", p0(func(p *Pipeline) { p.DrawDetections.Classes = []string{""} }), "pipelines[0].draw_detections.classes[0]"},

		// watermarks
		{"too many watermarks", p0(func(p *Pipeline) {
			p.Watermarks = slices.Repeat([]Watermark{{Opacity: 1, Position: Gravity{Type: GravityCenter}}}, MaxWatermarks+1)
		}), "pipelines[0].watermarks"},
		{"watermark opacity above 1", p0(func(p *Pipeline) { p.Watermarks[0].Opacity = 1.5 }), "pipelines[0].watermarks[0].opacity"},
		{"watermark position smart", p0(func(p *Pipeline) { p.Watermarks[0].Position.Type = GravitySmart }), "pipelines[0].watermarks[0].position.type"},
		{"watermark tile unknown", p0(func(p *Pipeline) { p.Watermarks[0].Tile = numTileModes }), "pipelines[0].watermarks[0].tile"},
		{"watermark scale negative", p0(func(p *Pipeline) { p.Watermarks[0].Scale = -1 }), "pipelines[0].watermarks[0].scale"},
		{"watermark source empty", p0(func(p *Pipeline) { p.Watermarks[0].Source.Raw = "" }), "pipelines[0].watermarks[0].source.raw"},
		{"watermark source and text", p0(func(p *Pipeline) { p.Watermarks[0].Text = "hi" }), "pipelines[0].watermarks[0].text"},
		{"watermark width negative", p0(func(p *Pipeline) { p.Watermarks[0].Width = -1 }), "pipelines[0].watermarks[0].width"},
		{"watermark height huge", p0(func(p *Pipeline) { p.Watermarks[0].Height = MaxDimension + 1 }), "pipelines[0].watermarks[0].height"},
		{"watermark rotate huge", p0(func(p *Pipeline) { p.Watermarks[0].Rotate = 400 }), "pipelines[0].watermarks[0].rotate"},
		{"watermark shadow negative", p0(func(p *Pipeline) { p.Watermarks[0].Shadow = -1 }), "pipelines[0].watermarks[0].shadow"},
		{"watermark text too long", p0(func(p *Pipeline) {
			p.Watermarks[0].Source, p.Watermarks[0].Text = nil, strings.Repeat("x", MaxWatermarkText+1)
		}), "pipelines[0].watermarks[0].text"},
		{"watermark text not utf8", p0(func(p *Pipeline) { p.Watermarks[0].Source, p.Watermarks[0].Text = nil, "\xff" }), "pipelines[0].watermarks[0].text"},
		{"watermark font markup", p0(func(p *Pipeline) {
			p.Watermarks[0].Source, p.Watermarks[0].Text, p.Watermarks[0].Font = nil, "x", "<b>"
		}), "pipelines[0].watermarks[0].font"},
		{"watermark font without text", p0(func(p *Pipeline) { p.Watermarks[0].Font = "sans 12" }), "pipelines[0].watermarks[0].text"},
		{"watermark text width negative", p0(func(p *Pipeline) {
			p.Watermarks[0].Source, p.Watermarks[0].Text, p.Watermarks[0].TextWidth = nil, "x", -1
		}), "pipelines[0].watermarks[0].text_width"},

		// pages and video
		{"page negative", p0(func(p *Pipeline) { p.Page = new(-1) }), "pipelines[0].page"},
		{"pages zero", p0(func(p *Pipeline) { p.Pages = new(0) }), "pipelines[0].pages"},
		{"pages -2", p0(func(p *Pipeline) { p.Pages = new(-2) }), "pipelines[0].pages"},
		{"video second out of range", p0(func(p *Pipeline) { p.Video.Second = new(-3e9) }), "pipelines[0].video.second"},
		{"video tile step negative", p0(func(p *Pipeline) { p.Video.Tile.Step = -1 }), "pipelines[0].video.tile.step"},
		{"video tile columns zero", p0(func(p *Pipeline) { p.Video.Tile.Columns = 0 }), "pipelines[0].video.tile.columns"},
		{"video tile rows zero", p0(func(p *Pipeline) { p.Video.Tile.Rows = 0 }), "pipelines[0].video.tile.rows"},
		{"video tile width negative", p0(func(p *Pipeline) { p.Video.Tile.TileWidth = -1 }), "pipelines[0].video.tile.tile_width"},
		{"video tile height negative", p0(func(p *Pipeline) { p.Video.Tile.TileHeight = -1 }), "pipelines[0].video.tile.tile_height"},
		{"video tile focus x above 1", p0(func(p *Pipeline) { p.Video.Tile.FocusX = 2 }), "pipelines[0].video.tile.focus_x"},
		{"video tile focus y negative", p0(func(p *Pipeline) { p.Video.Tile.FocusY = -1 }), "pipelines[0].video.tile.focus_y"},
		{"video animation step negative", p0(func(p *Pipeline) { p.Video.Animation.Step = -1 }), "pipelines[0].video.animation.step"},
		{"video animation delay negative", p0(func(p *Pipeline) { p.Video.Animation.Delay = -1 }), "pipelines[0].video.animation.delay"},
		{"video animation frames zero", p0(func(p *Pipeline) { p.Video.Animation.Frames = 0 }), "pipelines[0].video.animation.frames"},
		{"video animation frame width negative", p0(func(p *Pipeline) { p.Video.Animation.FrameWidth = -1 }), "pipelines[0].video.animation.frame_width"},
		{"video animation frame height negative", p0(func(p *Pipeline) { p.Video.Animation.FrameHeight = -1 }), "pipelines[0].video.animation.frame_height"},
		{"video animation focus x negative", p0(func(p *Pipeline) { p.Video.Animation.FocusX = -1 }), "pipelines[0].video.animation.focus_x"},
		{"video animation focus y above 1", p0(func(p *Pipeline) { p.Video.Animation.FocusY = 2 }), "pipelines[0].video.animation.focus_y"},
		{"second pipeline is validated too", func(r *Request) {
			r.Pipelines = append(r.Pipelines, r.Pipelines[0])
			r.Pipelines[1].Blur = -1
		}, "pipelines[1].blur"},

		// output
		{"format unknown", func(r *Request) { r.Output.Format = numFormats }, "output.format"},
		{"quality negative", func(r *Request) { r.Output.Quality = -1 }, "output.quality"},
		{"quality above 100", func(r *Request) { r.Output.Quality = 101 }, "output.quality"},
		{"format quality unset key", func(r *Request) { r.Output.FormatQuality = map[Format]int{FormatUnset: 50} }, "output.format_quality[]"},
		{"format quality zero", func(r *Request) { r.Output.FormatQuality = map[Format]int{FormatPNG: 0} }, "output.format_quality[png]"},
		{"format quality above 100", func(r *Request) { r.Output.FormatQuality = map[Format]int{FormatWebP: 101} }, "output.format_quality[webp]"},
		{"slow connection quality above 100", func(r *Request) { r.Output.SlowConnectionQuality = 101 }, "output.slow_connection_quality"},
		{"auto quality method unknown", func(r *Request) { r.Output.AutoQuality.Method = numAutoQualityMethods }, "output.auto_quality.method"},
		{"auto quality target negative", func(r *Request) { r.Output.AutoQuality.Target = -1 }, "output.auto_quality.target"},
		{"auto quality min negative", func(r *Request) { r.Output.AutoQuality.Min = -1 }, "output.auto_quality.min"},
		{"auto quality max above 100", func(r *Request) { r.Output.AutoQuality.Max = 101 }, "output.auto_quality.max"},
		{"auto quality min above max", func(r *Request) { r.Output.AutoQuality.Min = 95 }, "output.auto_quality.min"},
		{"auto quality allowed error above 1", func(r *Request) { r.Output.AutoQuality.AllowedError = 2 }, "output.auto_quality.allowed_error"},
		{"max bytes negative", func(r *Request) { r.Output.MaxBytes = -1 }, "output.max_bytes"},
		{"chroma subsampling unknown", func(r *Request) { r.Output.ChromaSubsampling = numSubsamplings }, "output.chroma_subsampling"},
		{"effort negative", func(r *Request) { r.Output.Effort = -1 }, "output.effort"},
		{"effort above 9", func(r *Request) { r.Output.Effort = 10 }, "output.effort"},
		{"jpeg quant table above 8", func(r *Request) { r.Output.JPEG.QuantTable = new(9) }, "output.jpeg.quant_table"},
		{"png colors 1", func(r *Request) { r.Output.PNG.QuantizationColors = 1 }, "output.png.quantization_colors"},
		{"png colors 257", func(r *Request) { r.Output.PNG.QuantizationColors = 257 }, "output.png.quantization_colors"},
		{"png compression above 9", func(r *Request) { r.Output.PNG.Compression = new(10) }, "output.png.compression"},
		{"webp compression unknown", func(r *Request) { r.Output.WebP.Compression = numWebPCompressions }, "output.webp.compression"},
		{"webp preset unknown", func(r *Request) { r.Output.WebP.Preset = numWebPPresets }, "output.webp.preset"},
		{"avif subsample 4:2:2", func(r *Request) { r.Output.AVIF.Subsample = Subsampling422 }, "output.avif.subsample"},
		{"dpi negative", func(r *Request) { r.Output.DPI = -1 }, "output.dpi"},
		{"dpi huge", func(r *Request) { r.Output.DPI = MaxDPI + 1 }, "output.dpi"},

		// delivery
		{"ttl negative", func(r *Request) { r.Delivery.TTL = -time.Second }, "delivery.ttl"},
		{"fallback empty", func(r *Request) { r.Delivery.Fallback.Raw = "" }, "delivery.fallback.raw"},
		{"skip processing unset", func(r *Request) { r.Delivery.SkipProcessing = []Format{FormatUnset} }, "delivery.skip_processing[0]"},
		{"skip processing too many", func(r *Request) {
			r.Delivery.SkipProcessing = slices.Repeat([]Format{FormatGIF}, MaxListLen+1)
		}, "delivery.skip_processing"},
		{"cache tag empty", func(r *Request) { r.Delivery.CacheTags = []string{""} }, "delivery.cache_tags[0]"},
		{"response encoding unknown", func(r *Request) { r.Delivery.Encoding = numResponseEncodings }, "delivery.encoding"},

		// limits
		{"max src resolution negative", func(r *Request) { r.Limits.MaxSrcResolution = -1 }, "limits.max_src_resolution"},
		{"max src file size negative", func(r *Request) { r.Limits.MaxSrcFileSize = -1 }, "limits.max_src_file_size"},
		{"max animation frames negative", func(r *Request) { r.Limits.MaxAnimationFrames = -1 }, "limits.max_animation_frames"},
		{"max animation frame resolution nan", func(r *Request) { r.Limits.MaxAnimationFrameResolution = nan }, "limits.max_animation_frame_resolution"},
		{"max result dimension huge", func(r *Request) { r.Limits.MaxResultDimension = MaxDimension + 1 }, "limits.max_result_dimension"},

		// info
		{"info schema unknown", infoMut(func(in *InfoOptions) { in.Schema = numInfoSchemas }), "info.schema"},
		{"info crop width negative", infoMut(func(in *InfoOptions) { in.Crop = &Crop{Width: Dim{Px: -1}} }), "info.crop.width.px"},
		{"info palette 1", infoMut(func(in *InfoOptions) { in.Palette = 1 }), "info.palette"},
		{"info palette 257", infoMut(func(in *InfoOptions) { in.Palette = 257 }), "info.palette"},
		{"info blurhash x zero", infoMut(func(in *InfoOptions) { in.Blurhash = &InfoBlurhash{XComponents: 0, YComponents: 1} }), "info.blurhash.x_components"},
		{"info blurhash y above 9", infoMut(func(in *InfoOptions) { in.Blurhash = &InfoBlurhash{XComponents: 1, YComponents: 10} }), "info.blurhash.y_components"},
		{"info hashsum unset", infoMut(func(in *InfoOptions) { in.CalcHashsums = []HashAlgorithm{HashUnset} }), "info.calc_hashsums[0]"},
		{"info too many hashsums", infoMut(func(in *InfoOptions) {
			in.CalcHashsums = slices.Repeat([]HashAlgorithm{HashMD5}, MaxListLen+1)
		}), "info.calc_hashsums"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := validBase()
			tc.mut(&r)
			err := r.Validate()
			fields := invalidFields(err)
			if !slices.Contains(fields, tc.field) {
				t.Fatalf("Validate() fields = %q, want %q (err: %v)", fields, tc.field, err)
			}
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("errors.AsType[*ValidationError] failed on %v", err)
			}
			if ve.HTTPStatus() != http.StatusBadRequest || !strings.HasPrefix(ve.PublicMessage(), "invalid ") {
				t.Fatalf("ValidationError status/message = %d %q", ve.HTTPStatus(), ve.PublicMessage())
			}
		})
	}
}

func infoMut(f func(in *InfoOptions)) func(r *Request) {
	return func(r *Request) {
		r.Kind = KindInfo
		r.Info = &InfoOptions{}
		f(r.Info)
	}
}

func TestValidateAcceptsBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mut  func(r *Request)
	}{
		{"pages all", func(r *Request) { r.Pipelines[0].Pages = new(-1) }},
		{"rotate any 180", func(r *Request) { r.Pipelines[0].RotateAny = 180 }},
		{"rotate 270", func(r *Request) { r.Pipelines[0].Rotate = 270 }},
		{"brightness -255", func(r *Request) { r.Pipelines[0].Adjust.Brightness = new(-255.0) }},
		{"quality 100", func(r *Request) { r.Output.Quality = 100 }},
		{"effort 9", func(r *Request) { r.Output.Effort = 9 }},
		{"max dimension", func(r *Request) { r.Pipelines[0].Resize.Width = Dim{Px: MaxDimension} }},
		{"objects position proportional", func(r *Request) {
			r.Pipelines[0].Gravity.ObjectsPosition = &ObjectsPosition{Proportional: true}
		}},
		{"objects position focus point", func(r *Request) {
			r.Pipelines[0].Gravity.ObjectsPosition = &ObjectsPosition{Type: GravityFocusPoint, X: 0.5, Y: 0.5}
		}},
		{"pixel focus point", func(r *Request) {
			r.Pipelines[0].Gravity = Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{X1: 7, Y1: 9, X2: 7, Y2: 9}}
		}},
		{"pixel focus max", func(r *Request) {
			r.Pipelines[0].Crop.Gravity = Gravity{Type: GravityFocusPoint, FocusPx: &FocusRect{X2: MaxSourceCoord, Y2: MaxSourceCoord}}
		}},
		{"extend focus point", func(r *Request) { r.Pipelines[0].Extend.Gravity = Gravity{Type: GravityFocusPoint, FocusX: 1} }},
		{"watermark text only", func(r *Request) {
			r.Pipelines[0].Watermarks[0] = Watermark{Opacity: 1, Text: "x", Position: Gravity{Type: GravityCenter}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := validBase()
			tc.mut(&r)
			if err := r.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidationErrorMessages(t *testing.T) {
	t.Parallel()
	e := &ValidationError{Field: "output.quality", Reason: "must be between 0 and 100"}
	if got, want := e.Error(), "transform: invalid output.quality: must be between 0 and 100"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got, want := e.PublicMessage(), "invalid output.quality: must be between 0 and 100"; got != want {
		t.Errorf("PublicMessage() = %q, want %q", got, want)
	}
}
