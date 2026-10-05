package imagekit

import (
	"math"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Video transformations (WP-125, https://imagekit.io/docs/video-transformation):
// a file with a video extension, or <video>/ik-video.mp4, becomes a
// transform.KindVideo request. The steps of the transformation string
// (":"-separated) apply in order onto one VideoOp: trimming, codecs and
// output accumulate (a later value wins); geometry is at most one
// cm-extract crop followed by one resize. Parameters without an
// equivalent are recorded in Request.Ignored (400 in strict mode).
//
// The output format defaults to ImageKit's f-auto: the server answers
// WebM to clients that name video/webm in Accept and MP4 otherwise
// (Vary: Accept); ik-video.mp4 and f-mp4 force MP4, f-webm WebM.

// Reasons of the video mapping.
const (
	reasonOverlays    = "M5 (WP-124)"
	reasonStreaming   = "M5 (WP-126)"
	reasonRelative    = "needs the source size or duration"
	reasonNotForVideo = "not applicable to video"
)

// Expectations of the video parameters.
const (
	expVideoDim    = "integer pixels > 0"
	expVideoTime   = "seconds (a non-negative number with up to 2 decimals)"
	expVideoFormat = "auto, mp4 or webm"
	expVCodec      = "h264, vp9, av1 or none, holdable by the output format"
	expACodec      = "aac, opus or none, holdable by the output format"
	expFPS         = "frames per second 1-240"
	expVideoRotate = "a multiple of 90, N<degrees> for negative"
)

// videoExtensions are the file extensions ImageKit delivers as video.
var videoExtensions = map[string]bool{
	"mp4": true, "webm": true, "mov": true, "m4v": true, "mkv": true, "avi": true,
	"ogv": true, "3gp": true, "mpeg": true, "mpg": true, "ts": true, "flv": true,
	"wmv": true, "qt": true,
}

// isVideoFile reports whether the escaped source key names a video file.
func isVideoFile(key string) bool {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(key), "."))
	return videoExtensions[ext]
}

// imageOutput reports whether the transformation string asks for an image
// format (its last f-): a video file then gets the image path (a frame,
// as with ik-thumbnail.jpg), so detection and image options keep working
// on video sources.
func imageOutput(tr string) bool {
	format := ""
	for step := range strings.SplitSeq(tr, ":") {
		for param := range strings.SplitSeq(step, ",") {
			if v, ok := strings.CutPrefix(param, "f-"); ok {
				format = v
			}
		}
	}
	switch format {
	case "jpg", "jpeg", "png", "webp", "avif", "gif", "jxl":
		return true
	}
	return false
}

// videoState collects one video request.
type videoState struct {
	ps      *parseState
	op      transform.VideoOp
	format  string // f- as written; "" = auto
	vcNone  bool
	resized bool
	dpr     float64
	eo, du  bool
}

// video applies a video transformation string. forceMP4 is ik-video.mp4.
func (ps *parseState) video(tr string, forceMP4 bool) error {
	vs := &videoState{ps: ps}
	for s := range strings.SplitSeq(tr, ":") {
		var params []string
		for param := range strings.SplitSeq(s, ",") {
			if param != "" {
				params = append(params, param)
			}
		}
		if err := vs.step(params); err != nil {
			return err
		}
	}
	if forceMP4 {
		if vs.format != "" && vs.format != "mp4" {
			ps.b.Ignore("f", reasonNotApplied) // ik-video.mp4 is MP4
		}
		vs.format = "mp4"
	}
	if err := vs.finish(); err != nil {
		return err
	}
	if err := ps.b.Finish(); err != nil {
		return err
	}
	r := ps.b.Request()
	op := vs.op
	r.Kind, r.Video, r.Output = transform.KindVideo, &op, transform.Output{}
	return nil
}

// skip records a parameter that is not applied (400 in strict mode).
func (vs *videoState) skip(key, reason string) error {
	return vs.ps.unsupportedAs(key, reason)
}

// approx records a parameter applied with a documented difference.
func (vs *videoState) approx(key string) {
	vs.ps.b.Ignore(key, reasonApproximated)
}

// videoStep is the geometry of one step.
type videoStep struct {
	w, h       int
	ar         float64
	crop, mode string
	fo         string
	x, y       int
	hasXY      bool
	bg         *transform.Color
}

// step applies one chain step.
//
//nolint:gocyclo,cyclop // one flat switch over the parameter table is the clearest form
func (vs *videoState) step(params []string) error {
	var st videoStep
	op := &vs.op
	for i := 0; i < len(params); i++ {
		key, v := splitParam(params[i])
		if strings.HasPrefix(key, "l-") {
			if key == "l-end" {
				return dialect.InvalidURL("l-end without a layer")
			}
			if err := vs.skip(key, reasonOverlays); err != nil {
				return err
			}
			i = layerEnd(params, i)
			continue
		}
		var err error
		switch key {
		case "w":
			st.w, err = vs.dim("w", v)
		case "h":
			st.h, err = vs.dim("h", v)
		case "ar":
			if isExpression(v) {
				err = vs.skip("ar", reasonExpression)
				break
			}
			st.ar, err = parseAspectRatio(v)
		case "c":
			switch v {
			case cropMaintainRatio, cropMaintainRatioNoEnlarge, cropForce, cropAtMax, cropAtMaxEnlarge, cropAtLeast:
				st.crop = v
			default:
				err = dialect.InvalidArgument("c", v, expCrop)
			}
		case "cm":
			switch v {
			case modePadResize, modePadResizeNoEnlarge, modeExtract, modePadExtract:
				st.mode = v
			default:
				err = dialect.InvalidArgument("cm", v, expCropMode)
			}
		case "fo":
			st.fo = v
		case "x", "y":
			if isExpression(v) {
				err = vs.skip(key, reasonExpression)
				break
			}
			n, convErr := strconv.Atoi(v)
			if convErr != nil || n < 0 || n > transform.MaxVideoDimension {
				err = dialect.InvalidArgument(key, v, expCoord)
				break
			}
			if key == "x" {
				st.x = n
			} else {
				st.y = n
			}
			st.hasXY = true
		case "xc", "yc":
			err = vs.skip(key, reasonUnsupported)
		case "bg":
			c, ok := parseColor(v)
			switch {
			case ok:
				st.bg = &c
			case v == "blurred" || strings.HasPrefix(v, "blurred_") || v == "dominant" || strings.HasPrefix(v, "gradient"):
				err = vs.skip("bg-"+firstWord(v), reasonUnsupported)
			default:
				err = dialect.InvalidArgument("bg", v, expColor)
			}
		case "so":
			err = vs.time("so", v, &op.Start)
		case "eo":
			vs.eo = true
			err = vs.time("eo", v, &op.End)
		case "du":
			vs.du = true
			err = vs.time("du", v, &op.Duration)
		case "f":
			switch v {
			case "auto", "mp4", "webm":
				vs.format = v
			case "orig":
				vs.format = ""
				vs.approx("f-orig") // the automatic format
			default:
				err = dialect.InvalidArgument("f", v, expVideoFormat)
			}
		case "vc":
			err = vs.videoCodec(v)
		case "ac":
			err = vs.audioCodec(v)
		case "q":
			n, convErr := strconv.Atoi(v)
			if convErr != nil || n < 1 || n > 100 {
				err = dialect.InvalidArgument("q", v, expQuality)
				break
			}
			op.Quality = n
		case "fps":
			f, ok := parseFloatIn(v, 1, transform.MaxVideoFPS)
			if !ok {
				err = dialect.InvalidArgument("fps", v, expFPS)
				break
			}
			op.FPS = f
		case "rt":
			err = vs.rotate(v)
		case "dpr":
			if v == "auto" {
				vs.approx("dpr-auto") // 1
				break
			}
			f, ok := parseFloatIn(v, 0.1, 5)
			if !ok {
				err = dialect.InvalidArgument("dpr", v, expDPR)
				break
			}
			vs.dpr = f
		case "sr":
			err = vs.skip("sr", reasonStreaming)
		case "br":
			err = vs.skip("br", reasonUnsupported) // the unit is not documented
		case "n":
			err = vs.skip("n", reasonUnsupported) // named transformations use image syntax
		case "b", "r", "bl", "blur", "o", "t", "z", "fl", "pr", "lo", "md", "cp", "orig", "di", "pg", "e-sharpen",
			"e-usm", "e-grayscale", "e-contrast", "e-shadow", "e-gradient", "e-bgremove", "e-removedotbg",
			"e-upscale", "e-retouch", "e-genvar", "e-dropshadow", "e-changebg", "e-edit", "e-distort", "e-colorize":
			err = vs.skip(key, reasonNotForVideo)
		default:
			err = vs.ps.unknown(key)
		}
		if err != nil {
			return err
		}
	}
	return vs.geometry(&st)
}

// dim parses w-/h- in pixels; relative values and expressions are
// recorded (0).
func (vs *videoState) dim(key, v string) (int, error) {
	if n, err := strconv.Atoi(v); err == nil {
		if n <= 0 || n > transform.MaxVideoDimension {
			return 0, dialect.InvalidArgument(key, v, expVideoDim)
		}
		return n, nil
	}
	if isExpression(v) || v == "auto" {
		return 0, vs.skip(key, reasonRelative)
	}
	if f, ok := parseFloatIn(v, 0, 1); ok && f > 0 {
		return 0, vs.skip(key, reasonRelative)
	}
	return 0, dialect.InvalidArgument(key, v, expVideoDim)
}

// time parses so-/eo-/du- in seconds.
func (vs *videoState) time(key, v string, dst *time.Duration) error {
	if isExpression(v) {
		return vs.skip(key, reasonExpression)
	}
	f, ok := parseFloatIn(v, 0, transform.MaxVideoTime.Seconds())
	if !ok {
		return dialect.InvalidArgument(key, v, expVideoTime)
	}
	*dst = time.Duration(math.Round(f * float64(time.Second)))
	return nil
}

func (vs *videoState) videoCodec(v string) error {
	vs.vcNone = false
	switch v {
	case "h264":
		vs.op.VideoCodec = transform.VideoCodecH264
	case "vp9":
		vs.op.VideoCodec = transform.VideoCodecVP9
	case "av1":
		vs.op.VideoCodec = transform.VideoCodecAV1
	case "none":
		vs.op.VideoCodec, vs.vcNone = transform.VideoCodecAuto, true
	default:
		return dialect.InvalidArgument("vc", v, expVCodec)
	}
	return nil
}

func (vs *videoState) audioCodec(v string) error {
	switch v {
	case "aac":
		vs.op.AudioCodec, vs.op.Mute = transform.AudioCodecAAC, false
	case "opus":
		vs.op.AudioCodec, vs.op.Mute = transform.AudioCodecOpus, false
	case "none":
		vs.op.Mute = true
	default:
		return dialect.InvalidArgument("ac", v, expACodec)
	}
	return nil
}

func (vs *videoState) rotate(v string) error {
	if v == "auto" {
		return vs.skip("rt-auto", reasonUnsupported) // the display matrix always applies
	}
	deg, ok := parseSigned(v, 360)
	if !ok || deg != math.Trunc(deg) {
		return dialect.InvalidArgument("rt", v, expVideoRotate)
	}
	if int(deg)%90 != 0 {
		return vs.skip("rt", reasonUnsupported)
	}
	vs.op.Rotate = ((vs.op.Rotate+int(deg))%360 + 360) % 360
	return nil
}

// gravity maps fo- (compass positions; detection focus is approximated
// by the centre).
func (vs *videoState) gravity(v string) transform.VideoGravity {
	switch v {
	case "", "center":
		return transform.VideoGravityCenter
	case "top":
		return transform.VideoGravityNorth
	case "bottom":
		return transform.VideoGravitySouth
	case "left":
		return transform.VideoGravityWest
	case "right":
		return transform.VideoGravityEast
	case "top_left":
		return transform.VideoGravityNorthWest
	case "top_right":
		return transform.VideoGravityNorthEast
	case "bottom_left":
		return transform.VideoGravitySouthWest
	case "bottom_right":
		return transform.VideoGravitySouthEast
	}
	vs.approx("fo-" + firstWord(v))
	return transform.VideoGravityCenter
}

// geometry applies one step's size, crop strategy and mode.
func (vs *videoState) geometry(st *videoStep) error {
	op := &vs.op
	w, h := st.w, st.h
	switch {
	case st.ar > 0 && w > 0 && h == 0:
		h = max(int(math.Round(float64(w)/st.ar)), 1)
	case st.ar > 0 && h > 0 && w == 0:
		w = max(int(math.Round(float64(h)*st.ar)), 1)
	case st.ar > 0 && w == 0 && h == 0:
		return vs.skip("ar", reasonRelative)
	}
	g := vs.gravity(st.fo)
	if st.mode == modeExtract {
		switch {
		case w < 2 || h < 2:
			return vs.skip("cm-extract", reasonRelative)
		case vs.resized || op.Crop != nil:
			return vs.skip("cm-extract", reasonUnsupported) // after a resize, or a second crop
		}
		crop := &transform.VideoCrop{X: st.x, Y: st.y, Width: w, Height: h}
		if st.fo != "" || !st.hasXY {
			crop.Gravity = &g
		}
		op.Crop = crop
		return nil
	}
	if w == 0 && h == 0 {
		if st.bg != nil && st.mode == "" && st.crop == "" {
			op.Background = *st.bg
			return nil
		}
		if st.crop != "" || st.mode != "" {
			return vs.skip(cropKey(st), reasonRelative)
		}
		return nil
	}
	box := w > 0 && h > 0
	fit := transform.VideoFitContain
	switch st.mode {
	case modePadResize:
		fit = transform.VideoFitPad
	case modePadResizeNoEnlarge:
		fit = transform.VideoFitPad
		vs.approx("cm-pad_resize_no_enlarge")
	case modePadExtract:
		if err := vs.skip("cm-pad_extract", reasonUnsupported); err != nil {
			return err
		}
	default:
		switch st.crop {
		case "", cropMaintainRatio:
			fit = transform.VideoFitCover
		case cropMaintainRatioNoEnlarge:
			fit = transform.VideoFitCover
			vs.approx("c-maintain_ratio_no_enlarge")
		case cropForce:
			fit = transform.VideoFitFill
		case cropAtMax:
			fit = transform.VideoFitScaleDown
		case cropAtMaxEnlarge:
			fit = transform.VideoFitContain
		case cropAtLeast:
			fit = transform.VideoFitCover
			vs.approx("c-at_least")
		}
	}
	if fit.NeedsBox() && !box {
		fit = transform.VideoFitContain
	}
	if st.hasXY {
		vs.ps.b.Ignore("x", reasonNotApplied)
	}
	if vs.resized {
		vs.approx(cropKey(st)) // a later resize replaces the earlier one
	}
	vs.resized = true
	op.Width, op.Height, op.Fit, op.Gravity = w, h, fit, g
	if st.bg != nil {
		op.Background = *st.bg
	}
	return nil
}

func cropKey(st *videoStep) string {
	if st.mode != "" {
		return "cm-" + st.mode
	}
	if st.crop != "" {
		return "c-" + st.crop
	}
	return "w"
}

// finish resolves the output container, the codecs, the cut and the DPR.
func (vs *videoState) finish() error {
	op := &vs.op
	if vs.eo && vs.du {
		vs.approx("du") // eo- wins
		op.Duration = 0
	}
	if op.End > 0 && op.End <= op.Start {
		return dialect.InvalidArgument("eo", strconv.FormatFloat(op.End.Seconds(), 'f', -1, 64), "a time after so-")
	}
	if vs.dpr > 0 && vs.dpr != 1 {
		scale := func(n int) int {
			if n == 0 {
				return 0
			}
			return min(max(int(math.Round(float64(n)*vs.dpr)), 2), transform.MaxVideoDimension)
		}
		op.Width, op.Height = scale(op.Width), scale(op.Height)
		if c := op.Crop; c != nil {
			c.X, c.Y, c.Width, c.Height = scale(c.X), scale(c.Y), scale(c.Width), scale(c.Height)
		}
	}
	switch {
	case vs.vcNone:
		// Audio only: AAC in M4A, or Opus in Ogg.
		op.Container = transform.VideoContainerM4A
		if op.AudioCodec == transform.AudioCodecOpus || vs.format == "webm" {
			op.Container = transform.VideoContainerOpus
		}
		if op.Mute {
			return dialect.InvalidArgument("ac", "none", "an audio codec for audio-only output")
		}
		op.Width, op.Height, op.Fit, op.Crop, op.FPS = 0, 0, 0, nil, 0
	case vs.format == "mp4":
		op.Container = transform.VideoContainerMP4
	case vs.format == "webm":
		op.Container = transform.VideoContainerWebM
	case op.VideoCodec == transform.VideoCodecVP9 || op.AudioCodec == transform.AudioCodecOpus:
		op.Container = transform.VideoContainerWebM
	case op.VideoCodec == transform.VideoCodecH264 || op.AudioCodec == transform.AudioCodecAAC:
		op.Container = transform.VideoContainerMP4
	default:
		op.Container, op.AutoContainer = transform.VideoContainerMP4, true
	}
	if !op.Container.AllowsVideo(op.VideoCodec) {
		return dialect.InvalidArgument("vc", op.VideoCodec.String(), expVCodec)
	}
	if !op.Container.AllowsAudio(op.AudioCodec) {
		return dialect.InvalidArgument("ac", op.AudioCodec.String(), expACodec)
	}
	return nil
}
