// Video transformation grammar (WP-122, WP-123, ADR 0012). Copyright
// xtandard-image contributors, MIT. imgproxy has no video transcoding: the
// /video endpoint reuses its URL shape, its signing and the names and
// argument rules of its geometry options, and adds the time, codec and
// audio options of docs/URL-DIALECTS.md §3.4.

package imgproxy

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// videoSigningPrefix is prepended to the signed path of a /video URL: the
// signature covers "/video/" + the path after the signature segment, so a
// signed image URL (a poster of the same source, say) is never a valid
// video transformation, which costs orders of magnitude more.
const videoSigningPrefix = "video/"

// videoState is the parse state of the options of a /video URL that only
// become a VideoOp once every option is known (finishVideo).
type videoState struct {
	// resizingType is the imgproxy resizing type ("" = fit).
	resizingType string
	enlarge      bool
	extend       bool
	// cropGravity reports a crop that names its own gravity; otherwise it
	// takes the gravity option's.
	cropGravity bool
	// containerSet reports an explicit format or extension.
	containerSet bool
}

// ParseVideo parses an imgproxy-style video transformation
// (docs/URL-DIALECTS.md §3.4). in.Path is the escaped path after the
// "/video" prefix:
//
//	/%signature/%video_options.../{plain/%url[@%ext]|%base64[.%ext]|enc/%token[.%ext]}
//
// The signature covers "/video" + in.Path without the signature segment.
// The result has Kind == KindVideo and Video set; the extension (or the
// format option) is the output container: mp4 (default), webm, m4a, mp3,
// opus, ogg, flac, wav, or png/webp for a waveform picture of the audio.
func (p *Parser) ParseVideo(in *dialect.Input) (*dialect.Result, error) {
	return p.ParseVideoPath(in.Path)
}

// ParseVideoPath is ParseVideo for a bare path such as
// "/unsafe/rs:fit:640:0/start:1/duration:5/plain/https://example.com/a.mov@webm".
func (p *Parser) ParseVideoPath(path string) (*dialect.Result, error) {
	return p.parsePath(path, parseVideo)
}

func newVideoState(p *Parser) *state {
	st := newState(p, false)
	st.r.Kind = transform.KindVideo
	st.r.Video = &transform.VideoOp{}
	st.video = &videoState{}
	return st
}

// parseVideo consumes the options and the source of a /video URL: no
// presets, no chained pipelines, and the extension is a container.
func (st *state) parseVideo(rest string) error {
	if st.p.o.Presets.Only() {
		return onlyPresetsError()
	}
	rest, err := st.parseOptions(rest)
	if err != nil {
		return err
	}
	ext, err := st.decodeSource(rest)
	if err != nil {
		return err
	}
	if ext != "" {
		if err := st.videoContainer("extension", ext); err != nil {
			return err
		}
	}
	return st.finish()
}

// applyVideo applies one /video option. args has at least one element.
//
//nolint:gocyclo // one flat switch over the option table is the clearest form
func (st *state) applyVideo(name string, args []string) error {
	op, vs := st.r.Video, st.video
	switch name {
	// Time.
	case "start", "vs":
		return oneSeconds("start", args, &op.Start)
	case "end", "ve":
		return oneSeconds("end", args, &op.End)
	case "duration", "vd":
		return oneSeconds("duration", args, &op.Duration)

	// Geometry (imgproxy's names and argument rules).
	case "resize", "rs":
		if err := maxArgs("resize", args, 5); err != nil {
			return err
		}
		if args[0] != "" {
			if err := st.videoResizingType("resize.type", args[0]); err != nil {
				return err
			}
		}
		return st.videoSize("resize", args[1:])
	case "size", "s":
		if err := maxArgs("size", args, 4); err != nil {
			return err
		}
		return st.videoSize("size", args)
	case "resizing_type", "rt":
		if err := maxArgs("resizing_type", args, 1); err != nil {
			return err
		}
		return st.videoResizingType("resizing_type", args[0])
	case "width", "w":
		return oneVideoDim("width", args, &op.Width)
	case "height", "h":
		return oneVideoDim("height", args, &op.Height)
	case "enlarge", "el":
		return oneBool("enlarge", args, &vs.enlarge)
	case "extend", "ex":
		return oneBool("extend", args, &vs.extend)
	case "gravity", "g":
		return videoGravity(&op.Gravity, "gravity", args)
	case "crop", "c":
		return st.videoCrop(args)
	case "rotate", "rot":
		if err := maxArgs("rotate", args, 1); err != nil {
			return err
		}
		r, err := parseInt("rotate", args[0])
		if err != nil {
			return err
		}
		if r%90 != 0 {
			return dialect.InvalidArgument("rotate", args[0], expRotate)
		}
		op.Rotate = (r%360 + 360) % 360
	case "flip", "fl":
		if err := maxArgs("flip", args, 2); err != nil {
			return err
		}
		if args[0] != "" {
			if err := oneBool("flip.horizontal", args[:1], &op.FlipH); err != nil {
				return err
			}
		}
		if len(args) > 1 && args[1] != "" {
			return oneBool("flip.vertical", args[1:2], &op.FlipV)
		}
	case "background", "bg":
		if err := maxArgs("background", args, 1); err != nil {
			return err
		}
		if args[0] == "" {
			op.Background = transform.Color{}
			return nil
		}
		c, err := parseHexColor("background", args[0])
		if err != nil {
			return err
		}
		op.Background = c
	case "fps":
		if err := maxArgs("fps", args, 1); err != nil {
			return err
		}
		v, err := parseNonNegFloat("fps", args[0])
		if err != nil {
			return err
		}
		op.FPS = v

	// Encoding.
	case "format", "f", "ext":
		if err := maxArgs("format", args, 1); err != nil {
			return err
		}
		return st.videoContainer("format", args[0])
	case "video_codec", "vc":
		if err := maxArgs("video_codec", args, 1); err != nil {
			return err
		}
		c, ok := videoCodecs[args[0]]
		if !ok {
			return dialect.InvalidArgument("video_codec", args[0], expVideoCodec)
		}
		op.VideoCodec = c
	case "audio_codec", "ac":
		if err := maxArgs("audio_codec", args, 1); err != nil {
			return err
		}
		c, ok := audioCodecs[args[0]]
		if !ok {
			return dialect.InvalidArgument("audio_codec", args[0], expAudioCodec)
		}
		op.AudioCodec = c
	case "quality", "q":
		if err := maxArgs("quality", args, 1); err != nil {
			return err
		}
		q, err := parseQuality("quality", args[0])
		if err != nil {
			return err
		}
		op.Quality = q
	case "crf":
		return oneNonNegInt("crf", args, &op.CRF)
	case "video_bitrate", "vb":
		return oneBitrate("video_bitrate", args, &op.MaxBitrate)
	case "keyframe_interval", "ki":
		return oneSeconds("keyframe_interval", args, &op.KeyframeInterval)

	// Audio (WP-123).
	case "mute", "mu":
		return oneBool("mute", args, &op.Mute)
	case "audio_bitrate", "ab":
		return oneBitrate("audio_bitrate", args, &op.AudioBitrate)
	case "sample_rate", "sr":
		return oneNonNegInt("sample_rate", args, &op.SampleRate)
	case "audio_channels", "ach":
		return oneNonNegInt("audio_channels", args, &op.Channels)
	case "volume", "vol":
		if err := maxArgs("volume", args, 1); err != nil {
			return err
		}
		v, err := parseVolume("volume", args[0])
		if err != nil {
			return err
		}
		op.Volume = v
	case "loudnorm", "ln":
		return videoLoudnorm(op, args)
	case "fade", "fd":
		if err := maxArgs("fade", args, 2); err != nil {
			return err
		}
		if args[0] != "" {
			if err := oneSeconds("fade.in", args[:1], &op.FadeIn); err != nil {
				return err
			}
		}
		if len(args) > 1 && args[1] != "" {
			return oneSeconds("fade.out", args[1:2], &op.FadeOut)
		}
	case "waveform", "wf":
		return videoWaveform(op, args)

	// Delivery and source options shared with processing URLs.
	case "cachebuster", "cb",
		"expires", "exp",
		"filename", "fn",
		"return_attachment", "att",
		"hashsum", "hs",
		"bypass_cache", "bc",
		"cache_tags", "ct":
		return st.apply(name, args)
	default:
		if st.p.o.Strict {
			return dialect.UnknownOption(name)
		}
		st.ignore(name, reasonUnknown)
	}
	return nil
}

// Expected-value texts of the video options.
const (
	expSeconds      = "seconds >= 0"
	expVideoDim     = "integer pixels between 0 and 8192"
	expVideoResize  = "fit, fill, fill-down or force"
	expVideoCodec   = "auto, h264, av1, vp9, hevc or h265"
	expAudioCodec   = "auto, aac, opus, mp3, flac or pcm"
	expVideoFormat  = "mp4, webm, m4a, mp3, opus, ogg, flac, wav, png or webp"
	expBitrate      = "bits per second (an integer, or a number with k or M)"
	expVolume       = "a gain factor (0.5) or decibels (-6dB)"
	expVideoGravity = "ce, no, so, ea, we, noea, nowe, soea, sowe or fp"
)

// videoResizingTypes are the imgproxy resizing types a video accepts;
// auto (which needs the source's orientation before the probe) is not one.
var videoResizingTypes = map[string]bool{"fit": true, "fill": true, "fill-down": true, "force": true}

// videoContainers maps format names and extensions to containers.
var videoContainers = map[string]transform.VideoContainer{
	"mp4": transform.VideoContainerMP4, "webm": transform.VideoContainerWebM,
	"m4a": transform.VideoContainerM4A, "mp3": transform.VideoContainerMP3,
	"opus": transform.VideoContainerOpus, "ogg": transform.VideoContainerOgg,
	"flac": transform.VideoContainerFLAC, "wav": transform.VideoContainerWAV,
	"png": transform.VideoContainerPNG, "webp": transform.VideoContainerWebP,
}

var videoCodecs = map[string]transform.VideoCodec{
	"auto": transform.VideoCodecAuto, "h264": transform.VideoCodecH264, "av1": transform.VideoCodecAV1,
	"vp9": transform.VideoCodecVP9, "hevc": transform.VideoCodecHEVC, "h265": transform.VideoCodecHEVC,
}

var audioCodecs = map[string]transform.AudioCodec{
	"auto": transform.AudioCodecAuto, "aac": transform.AudioCodecAAC, "opus": transform.AudioCodecOpus,
	"mp3": transform.AudioCodecMP3, "flac": transform.AudioCodecFLAC, "pcm": transform.AudioCodecPCM,
}

func (st *state) videoContainer(field, s string) error {
	c, ok := videoContainers[strings.ToLower(s)]
	if !ok {
		return dialect.InvalidArgument(field, s, expVideoFormat)
	}
	st.r.Video.Container = c
	st.video.containerSet = true
	return nil
}

func (st *state) videoResizingType(field, s string) error {
	if !videoResizingTypes[s] {
		return dialect.InvalidArgument(field, s, expVideoResize)
	}
	st.video.resizingType = s
	return nil
}

// videoSize is width:height:enlarge:extend (size, and resize after its
// type); empty arguments keep the previous values.
func (st *state) videoSize(field string, args []string) error {
	op, vs := st.r.Video, st.video
	if len(args) > 0 && args[0] != "" {
		if err := oneVideoDim(field+".width", args[:1], &op.Width); err != nil {
			return err
		}
	}
	if len(args) > 1 && args[1] != "" {
		if err := oneVideoDim(field+".height", args[1:2], &op.Height); err != nil {
			return err
		}
	}
	if len(args) > 2 && args[2] != "" {
		if err := oneBool(field+".enlarge", args[2:3], &vs.enlarge); err != nil {
			return err
		}
	}
	if len(args) > 3 && args[3] != "" {
		return oneBool(field+".extend", args[3:4], &vs.extend)
	}
	return nil
}

// videoCrop is c:width[:height[:gravity...]] in pixels (0 = the whole
// side).
func (st *state) videoCrop(args []string) error {
	c := transform.VideoCrop{}
	if st.r.Video.Crop != nil {
		c = *st.r.Video.Crop
	}
	if err := oneVideoDim("crop.width", args[:1], &c.Width); err != nil {
		return err
	}
	if len(args) > 1 {
		if err := oneVideoDim("crop.height", args[1:2], &c.Height); err != nil {
			return err
		}
	}
	if len(args) > 2 {
		if err := videoGravity(&c.Gravity, "crop.gravity", args[2:]); err != nil {
			return err
		}
		st.video.cropGravity = true
	}
	st.r.Video.Crop = &c
	return nil
}

// finishVideo turns the parse state into the VideoOp: the resizing type,
// enlarge and extend become a VideoFit, a crop without its own gravity
// takes the gravity option's (the centre by default), and a waveform and
// an image container imply each other. The op is validated here, so an
// out-of-range value is a 400 naming its field before anything else runs.
func (st *state) finishVideo() error {
	op, vs := st.r.Video, st.video
	box := op.Width > 0 && op.Height > 0
	switch {
	case box && (vs.resizingType == "fill" || vs.resizingType == "fill-down"):
		op.Fit = transform.VideoFitCover
	case box && vs.resizingType == "force":
		op.Fit = transform.VideoFitFill
	case box && vs.extend:
		op.Fit = transform.VideoFitPad
	case vs.enlarge:
		op.Fit = transform.VideoFitContain
	default:
		op.Fit = transform.VideoFitScaleDown
	}
	if c := op.Crop; c != nil && !vs.cropGravity {
		c.Gravity = op.Gravity
		if c.Gravity.Type == transform.GravityUnset {
			c.Gravity.Type = transform.GravityCenter
		}
	}
	switch {
	case op.Waveform != nil && !op.Container.Image():
		if vs.containerSet {
			return dialect.InvalidArgument("waveform", op.Container.String(), "a png or webp output")
		}
		op.Container = transform.VideoContainerPNG
	case op.Waveform == nil && op.Container.Image():
		op.Waveform = &transform.VideoWaveform{}
	}
	if err := op.Validate(); err != nil {
		public := "invalid video options"
		if ve, ok := errors.AsType[*transform.ValidationError](err); ok {
			public = ve.PublicMessage()
		}
		return &errs.Error{Kind: errs.KindInvalidURL, Public: public, Err: err}
	}
	if e := st.r.Delivery.Expires; !e.IsZero() && e.Before(st.p.o.Now()) {
		return dialect.Expired()
	}
	return nil
}

// oneSeconds parses a time in seconds (a decimal number >= 0) into dst,
// to the microsecond.
func oneSeconds(field string, args []string, dst *time.Duration) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	v, ok := parseFloatRaw(args[0])
	if !ok || v < 0 || v > transform.MaxVideoTime.Seconds() {
		return dialect.InvalidArgument(field, args[0], expSeconds)
	}
	*dst = time.Duration(math.Round(v*1e6)) * time.Microsecond
	return nil
}

// oneVideoDim parses an integer pixel size of a video.
func oneVideoDim(field string, args []string, dst *int) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	v, err := strconv.Atoi(args[0])
	if err != nil || v < 0 || v > transform.MaxVideoDimension {
		return dialect.InvalidArgument(field, args[0], expVideoDim)
	}
	*dst = v
	return nil
}

// oneBitrate parses bits per second: "800000", "800k", "2.5M"; "" or 0
// is unset.
func oneBitrate(field string, args []string, dst *int64) error {
	if err := maxArgs(field, args, 1); err != nil {
		return err
	}
	v, err := parseBitrate(field, args[0])
	if err != nil {
		return err
	}
	*dst = v
	return nil
}

func parseBitrate(field, s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'k', 'K':
		mult, s = 1e3, s[:len(s)-1]
	case 'm', 'M':
		mult, s = 1e6, s[:len(s)-1]
	}
	v, ok := parseFloatRaw(s)
	if !ok || v < 0 || v*mult > 1e12 {
		return 0, dialect.InvalidArgument(field, s, expBitrate)
	}
	return int64(math.Round(v * mult)), nil
}

// parseVolume parses a gain factor ("0.5") or decibels ("-6dB", "+3db").
func parseVolume(field, s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	if num, ok := strings.CutSuffix(strings.ToLower(s), "db"); ok {
		db, ok := parseFloatRaw(num)
		if !ok || db < -60 || db > 20 {
			return 0, dialect.InvalidArgument(field, s, expVolume)
		}
		return math.Pow(10, db/20), nil
	}
	v, ok := parseFloatRaw(s)
	if !ok || v < 0 {
		return 0, dialect.InvalidArgument(field, s, expVolume)
	}
	return v, nil
}

// videoGravity parses type[:x:y] for a compass point (offsets in pixels,
// or fractions of the frame below 1) or fp:x:y (0..1).
func videoGravity(g *transform.VideoGravity, field string, args []string) error {
	if err := maxArgs(field, args, 3); err != nil {
		return err
	}
	t := args[0]
	if t == "fp" {
		if len(args) != 3 {
			return dialect.InvalidArgument(field, joinArgs(args), "fp:x:y")
		}
		x, err := parseUnitFloat(field+".x", args[1])
		if err != nil {
			return err
		}
		y, err := parseUnitFloat(field+".y", args[2])
		if err != nil {
			return err
		}
		*g = transform.VideoGravity{Type: transform.GravityFocusPoint, X: x, Y: y}
		return nil
	}
	gt, ok := compass[t]
	if !ok {
		return dialect.InvalidArgument(field+".type", t, expVideoGravity)
	}
	ng := transform.VideoGravity{Type: gt}
	if g.Type != transform.GravityFocusPoint {
		ng.X, ng.Y = g.X, g.Y
	}
	if len(args) > 1 && args[1] != "" {
		x, err := parseFloat(field+".x_offset", args[1])
		if err != nil {
			return err
		}
		ng.X = x
	}
	if len(args) > 2 && args[2] != "" {
		y, err := parseFloat(field+".y_offset", args[2])
		if err != nil {
			return err
		}
		ng.Y = y
	}
	*g = ng
	return nil
}

// videoLoudnorm is ln:enabled[:integrated:true_peak:range]; omitted
// values are the EBU R128 target (-23 LUFS, -1 dBTP, 7 LU).
func videoLoudnorm(op *transform.VideoOp, args []string) error {
	if err := maxArgs("loudnorm", args, 4); err != nil {
		return err
	}
	on, err := parseBool("loudnorm", args[0])
	if err != nil {
		return err
	}
	if !on {
		op.Loudnorm = nil
		return nil
	}
	l := transform.DefaultLoudnorm()
	for i, f := range []struct {
		name string
		dst  *float64
	}{{"loudnorm.integrated", &l.Integrated}, {"loudnorm.true_peak", &l.TruePeak}, {"loudnorm.range", &l.Range}} {
		if len(args) > i+1 && args[i+1] != "" {
			if *f.dst, err = parseFloat(f.name, args[i+1]); err != nil {
				return err
			}
		}
	}
	op.Loudnorm = &l
	return nil
}

// videoWaveform is wf:enabled[:color:background:split_channels].
func videoWaveform(op *transform.VideoOp, args []string) error {
	if err := maxArgs("waveform", args, 4); err != nil {
		return err
	}
	on, err := parseBool("waveform", args[0])
	if err != nil {
		return err
	}
	if !on {
		op.Waveform = nil
		return nil
	}
	w := transform.VideoWaveform{}
	if len(args) > 1 && args[1] != "" {
		if w.Color, err = parseHexColor("waveform.color", args[1]); err != nil {
			return err
		}
	}
	if len(args) > 2 && args[2] != "" {
		if w.Background, err = parseHexColor("waveform.background", args[2]); err != nil {
			return err
		}
	}
	if len(args) > 3 {
		if err := oneBool("waveform.split_channels", args[3:4], &w.SplitChannels); err != nil {
			return err
		}
	}
	op.Waveform = &w
	return nil
}
