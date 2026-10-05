package transform

import (
	"errors"
	"slices"
	"strconv"
	"time"
)

// Video transformations (WP-121, ADR 0012): a video source transcoded into
// a video (or audio-only) file, like Cloudinary's video transformations and
// Cloudflare Media Transformations. A request of KindVideo carries a
// VideoOp; the image fields (Pipelines, Output) do not apply to it.
//
// The model is plain data like the rest of the package. Every value of the
// transcode itself is a number or an enum: the filter graph FFmpeg runs is
// built from these numbers only (internal/video.TranscodePlan), never from
// text a URL carried. The only text, an overlay's source and markup
// (Overlays), is rendered into pixels by libvips before the graph exists.

// VideoOp describes one video transformation. The zero value transcodes
// the whole source into MP4 at its own size and frame rate, with its
// audio.
//
// The geometry is applied in this order: the source's display matrix
// (autorotation), Rotate and the flips, Crop (in the rotated frame), then
// the Fit of Width × Height, so Width and Height are always the size of the
// output.
type VideoOp struct {
	// Start is where the output begins in the source; 0 is its start.
	Start time.Duration
	// End is where the output ends in the source (exclusive with
	// Duration); 0 is the end of the source.
	End time.Duration
	// Duration is the length of the output from Start (exclusive with
	// End); 0 runs to End or the end of the source.
	Duration time.Duration

	// Width and Height are the output box in pixels; 0 derives one from
	// the other and the source's aspect ratio, both 0 keep the source
	// size. Odd values are rounded down to even (4:2:0 chroma).
	Width, Height int
	// Fit is how the source fills the box.
	Fit VideoFit
	// Gravity is the part of the frame VideoFitCover keeps (the centre by
	// default) and where VideoFitPad places the frame in the box (WP-125:
	// Cloudinary g_, ImageKit fo-).
	Gravity VideoGravity
	// Crop cuts a rectangle of the (rotated) source before the fit.
	Crop *VideoCrop
	// Background fills the bars of VideoFitPad; the zero value is black.
	// Alpha is ignored (the codecs are 4:2:0 without alpha).
	Background Color
	// Rotate turns the frame clockwise by 0, 90, 180 or 270 degrees,
	// after the display matrix.
	Rotate int
	// FlipH and FlipV mirror the frame after Rotate.
	FlipH, FlipV bool
	// FPS is the output frame rate; 0 keeps the source's (bounded by the
	// configured maximum).
	FPS float64
	// KeyframeInterval is the time between keyframes (the GOP length,
	// WP-122); 0 is two seconds.
	KeyframeInterval time.Duration
	// FadeIn and FadeOut fade the start of the output in from black and
	// silence and its end out to them (WP-123); 0 is no fade.
	FadeIn, FadeOut time.Duration

	// Container is the output file format.
	Container VideoContainer
	// AutoContainer lets the server pick the container from the request's
	// Accept header (Cloudinary f_auto, ImageKit f-auto; WP-125): WebM
	// when the client accepts video/webm and the codecs allow it,
	// otherwise Container (MP4). The response then varies on Accept.
	AutoContainer bool
	// VideoCodec and AudioCodec pick the codecs; auto takes the
	// container's default for this build (VideoContainer.DefaultCodecs).
	VideoCodec VideoCodec
	AudioCodec AudioCodec
	// Mute drops the audio. Audio-only containers cannot be muted.
	Mute bool
	// Quality (1–100) maps onto the codec's CRF scale like animated
	// output (internal/video.CRF); 0 is the configured CRF.
	Quality int
	// CRF sets the constant rate factor directly (wins over Quality); 0 is
	// unset.
	CRF int
	// MaxBitrate caps the video bit rate in bits per second (constrained
	// quality); 0 is uncapped.
	MaxBitrate int64
	// AudioBitrate is the audio bit rate in bits per second; 0 is the
	// codec's default.
	AudioBitrate int64
	// SampleRate is the output sample rate in Hz (WP-123); 0 keeps the
	// source's when the codec supports it.
	SampleRate int
	// Channels is the output channel count, 1 (mono) or 2 (stereo); 0 keeps
	// the source's, downmixed to stereo.
	Channels int
	// Volume multiplies the audio amplitude (1 unchanged, 0.5 is -6 dB); 0
	// is unset.
	Volume float64
	// Loudnorm normalises the loudness (EBU R128, single-pass loudnorm);
	// nil is off.
	Loudnorm *VideoLoudnorm
	// Waveform renders the audio as a waveform image instead of
	// transcoding it; set exactly when Container is an image (png, webp).
	Waveform *VideoWaveform

	// Overlays are images, text or a timecode drawn over the video, in
	// order (WP-124). Audio-only containers take none.
	Overlays []VideoOverlay

	// Stream delivers the output as adaptive streaming (HLS or DASH, WP-126)
	// instead of one file: a manifest of renditions whose segments are
	// transcoded on request. nil is a file.
	Stream *VideoStream
}

// VideoCrop is a rectangle of the source in pixels, after the rotation.
// Width or Height 0 is the whole side; a rectangle larger than the frame
// is cut to it.
type VideoCrop struct {
	X, Y, Width, Height int
	// Gravity anchors the rectangle (WP-125: Cloudinary c_crop with g_,
	// ImageKit cm-extract with fo-): nil places it with X, Y from the
	// top-left corner; otherwise X and Y are offsets from that anchor
	// towards the inside of the frame (from the centre for
	// VideoGravityCenter, where they move right and down).
	Gravity *VideoGravity
}

// VideoLoudnorm is a loudness target (EBU R128, FFmpeg loudnorm): the
// integrated loudness in LUFS, the true peak in dBTP and the loudness
// range in LU.
type VideoLoudnorm struct {
	Integrated, TruePeak, Range float64
}

// Loudness target bounds (FFmpeg loudnorm's) and defaults (EBU R128).
const (
	MinLoudnessIntegrated     = -70.0
	MaxLoudnessIntegrated     = -5.0
	DefaultLoudnessIntegrated = -23.0
	MinLoudnessTruePeak       = -9.0
	MaxLoudnessTruePeak       = 0.0
	DefaultLoudnessTruePeak   = -1.0
	MinLoudnessRange          = 1.0
	MaxLoudnessRange          = 50.0
	DefaultLoudnessRange      = 7.0
)

// DefaultLoudnorm returns the EBU R128 target: -23 LUFS, -1 dBTP, 7 LU.
func DefaultLoudnorm() VideoLoudnorm {
	return VideoLoudnorm{Integrated: DefaultLoudnessIntegrated, TruePeak: DefaultLoudnessTruePeak, Range: DefaultLoudnessRange}
}

// VideoWaveform is a picture of the audio (FFmpeg showwavespic, like
// Cloudinary's fl_waveform), Width × Height of VideoOp (default
// DefaultWaveformWidth × DefaultWaveformHeight).
type VideoWaveform struct {
	// Color draws the wave (alpha ignored); the zero value is black.
	Color Color
	// Background fills the picture; alpha 0 (the zero value) leaves it
	// transparent, any other alpha makes it opaque.
	Background Color
	// SplitChannels draws each channel in its own band.
	SplitChannels bool
}

// Waveform size defaults.
const (
	DefaultWaveformWidth  = 1024
	DefaultWaveformHeight = 256
)

// VideoOverlay is an image, text or a running timecode drawn over the
// output video during a time window (WP-124, ADR 0012 §3), like an image
// watermark drawn on every frame.
//
// The embedded Watermark says what is drawn and how, with the image
// pipeline's semantics so that dialects map the same options onto both
// (imgproxy wm/wmu/wmt/wms/wmr/wmsh, Cloudinary l_, ImageKit l-image and
// l-text): Source is an image fetched like a source, Text is Pango markup
// (sanitised before rendering), neither is the configured watermark image
// (XI_PROCESSING_WATERMARK_SOURCE). Opacity 0 draws nothing, as on images.
// Sizes and offsets are output pixels (the frame after the fit), and the
// overlay is fitted, rotated, shadowed and placed (or tiled) on that frame
// exactly as on an image of the same size: libvips renders it once into
// RGBA pixels that FFmpeg composites over every frame of the window.
//
// A Timecode overlay draws the output's running time instead (FFmpeg
// drawtext with a fixed expansion); it uses Position, Opacity, Color and
// Shadow of the Watermark and TimecodeSize, and nothing a request says
// becomes its text.
type VideoOverlay struct {
	Watermark
	// Start and End bound when the overlay is shown, on the output's
	// timeline (0 is the first frame of the output, after the trim); End 0
	// is the end of the output.
	Start, End time.Duration
	// FadeIn and FadeOut fade the overlay in from Start and out to End
	// (FadeOut needs an end: End, or the output's known length).
	FadeIn, FadeOut time.Duration
	// Timecode draws the running time in this format instead of an image
	// or text; VideoTimecodeNone (the zero value) is off.
	Timecode VideoTimecode
	// TimecodeSize is the timecode's font size in output pixels; 0 is
	// DefaultTimecodeSize.
	TimecodeSize int
}

// VideoOverlayKind is what a VideoOverlay draws.
type VideoOverlayKind uint8

// VideoOverlayKind values.
const (
	// VideoOverlayDefault is the configured watermark image
	// (XI_PROCESSING_WATERMARK_SOURCE): neither source nor text.
	VideoOverlayDefault VideoOverlayKind = iota
	// VideoOverlayImage is the overlay's own image (Watermark.Source).
	VideoOverlayImage
	// VideoOverlayText is rendered Pango markup (Watermark.Text).
	VideoOverlayText
	// VideoOverlayTimecode is the running time (Timecode).
	VideoOverlayTimecode
)

// Kind is what o draws.
func (o *VideoOverlay) Kind() VideoOverlayKind {
	switch {
	case o.Timecode != VideoTimecodeNone:
		return VideoOverlayTimecode
	case o.Source != nil:
		return VideoOverlayImage
	case o.Text != "":
		return VideoOverlayText
	default:
		return VideoOverlayDefault
	}
}

// DefaultTimecodeSize is a timecode's font size in pixels when the overlay
// sets none.
const DefaultTimecodeSize = 24

// Video limits of the model (independent of configuration, which can only
// lower them).
const (
	// MaxVideoDimension bounds Width, Height and the crop rectangle.
	MaxVideoDimension = 8192
	// MaxVideoFPS bounds FPS.
	MaxVideoFPS = 240
	// MaxVideoTime bounds Start, End and Duration.
	MaxVideoTime = 24 * time.Hour
	// MaxVideoCRF is the top of the widest CRF scale (VP9, AV1).
	MaxVideoCRF = 63
	// Bit rate bounds, bits per second.
	MinVideoBitrate = 50_000
	MaxVideoBitrate = 200_000_000
	MinAudioBitrate = 8_000
	MaxAudioBitrate = 512_000
	// MaxVideoOverlays bounds Overlays: each image or text overlay is held
	// as RGBA pixels of at most the output frame for the whole transcode.
	MaxVideoOverlays = 8
	// MaxTimecodeSize bounds VideoOverlay.TimecodeSize (pixels).
	MaxTimecodeSize = 512
	// Keyframe interval bounds.
	MinKeyframeInterval = 100 * time.Millisecond
	MaxKeyframeInterval = time.Minute
	// MaxVolume bounds Volume (+20 dB); MinVolume is -60 dB.
	MinVolume = 0.001
	MaxVolume = 10.0
)

// SampleRates are the output sample rates a request may ask for.
var SampleRates = []int{8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 88200, 96000}

// EndTime returns where the output ends in the source: End, Start + Duration,
// or 0 (the end of the source).
func (o *VideoOp) EndTime() time.Duration {
	switch {
	case o.End > 0:
		return o.End
	case o.Duration > 0:
		return o.Start + o.Duration
	default:
		return 0
	}
}

// HasVideo reports whether the output has a video stream: false for the
// audio-only containers.
func (o *VideoOp) HasVideo() bool { return !o.Container.AudioOnly() && !o.Container.Image() }

// HasAudio reports whether the output asks for an audio stream (the source
// may still have none).
func (o *VideoOp) HasAudio() bool { return !o.Mute }

// Validate checks op on its own: the "video" fields of Request.Validate,
// for dialects that refuse an out-of-range video option while parsing.
func (o *VideoOp) Validate() error {
	v := &validator{}
	o.validate(v, "video")
	return errors.Join(v.errs...)
}

func (o *VideoOp) validate(v *validator, f string) {
	for _, t := range []struct {
		name string
		d    time.Duration
	}{{"start", o.Start}, {"end", o.End}, {"duration", o.Duration}} {
		if t.d < 0 || t.d > MaxVideoTime {
			v.fail(f+"."+t.name, between("0", MaxVideoTime.String()))
		}
	}
	if o.End != 0 && o.Duration != 0 {
		v.fail(f+".end", "end and duration are mutually exclusive")
	}
	if o.End != 0 && o.End <= o.Start {
		v.fail(f+".end", "must be after start")
	}
	v.intRange(f+".width", o.Width, 0, MaxVideoDimension)
	v.intRange(f+".height", o.Height, 0, MaxVideoDimension)
	v.enum(f+".fit", validEnum(o.Fit, numVideoFits))
	v.enum(f+".gravity", validEnum(o.Gravity, numVideoGravities))
	if o.Fit.NeedsBox() && (o.Width == 0 || o.Height == 0) {
		v.fail(f+".fit", o.Fit.String()+" needs both width and height")
	}
	if c := o.Crop; c != nil {
		v.intRange(f+".crop.x", c.X, 0, MaxVideoDimension)
		v.intRange(f+".crop.y", c.Y, 0, MaxVideoDimension)
		if c.Width != 0 {
			v.intRange(f+".crop.width", c.Width, 2, MaxVideoDimension)
		}
		if c.Height != 0 {
			v.intRange(f+".crop.height", c.Height, 2, MaxVideoDimension)
		}
		if c.Gravity != nil {
			v.enum(f+".crop.gravity", validEnum(*c.Gravity, numVideoGravities))
		}
	}
	switch o.Rotate {
	case 0, 90, 180, 270:
	default:
		v.fail(f+".rotate", "must be 0, 90, 180 or 270")
	}
	if o.FPS != 0 {
		v.floatRange(f+".fps", o.FPS, 1, MaxVideoFPS)
	}
	if k := o.KeyframeInterval; k != 0 && (k < MinKeyframeInterval || k > MaxKeyframeInterval) {
		v.fail(f+".keyframe_interval", between(MinKeyframeInterval.String(), MaxKeyframeInterval.String()))
	}
	for _, t := range []struct {
		name string
		d    time.Duration
	}{{"fade_in", o.FadeIn}, {"fade_out", o.FadeOut}} {
		if t.d < 0 || t.d > MaxVideoTime {
			v.fail(f+"."+t.name, between("0", MaxVideoTime.String()))
		}
	}
	v.enum(f+".container", validEnum(o.Container, numVideoContainers))
	v.enum(f+".video_codec", validEnum(o.VideoCodec, numVideoCodecs))
	v.enum(f+".audio_codec", validEnum(o.AudioCodec, numAudioCodecs))
	if validEnum(o.Container, numVideoContainers) && validEnum(o.VideoCodec, numVideoCodecs) &&
		validEnum(o.AudioCodec, numAudioCodecs) {
		if !o.Container.AllowsVideo(o.VideoCodec) && (o.Stream == nil || o.VideoCodec != VideoCodecVP9) { // CMAF carries VP9 (vp09)
			v.fail(f+".video_codec", o.VideoCodec.String()+" cannot be muxed into "+o.Container.String())
		}
		if !o.Container.AllowsAudio(o.AudioCodec) {
			v.fail(f+".audio_codec", o.AudioCodec.String()+" cannot be muxed into "+o.Container.String())
		}
		if o.Mute && !o.Container.HasVideoStream() {
			v.fail(f+".mute", o.Container.String()+" has no video")
		}
		if (o.Waveform != nil) != o.Container.Image() {
			v.fail(f+".waveform", "set if and only if the container is an image")
		}
		if o.AutoContainer && o.Container != VideoContainerMP4 {
			v.fail(f+".auto_container", "the automatic container starts from mp4")
		}
	}
	v.intRange(f+".quality", o.Quality, 0, 100)
	v.intRange(f+".crf", o.CRF, 0, MaxVideoCRF)
	if o.MaxBitrate != 0 && (o.MaxBitrate < MinVideoBitrate || o.MaxBitrate > MaxVideoBitrate) {
		v.fail(f+".max_bitrate", between(strconv.Itoa(MinVideoBitrate), strconv.Itoa(MaxVideoBitrate)))
	}
	if o.AudioBitrate != 0 && (o.AudioBitrate < MinAudioBitrate || o.AudioBitrate > MaxAudioBitrate) {
		v.fail(f+".audio_bitrate", between(strconv.Itoa(MinAudioBitrate), strconv.Itoa(MaxAudioBitrate)))
	}
	o.validateAudio(v, f)
	if o.Stream != nil {
		o.validateStream(v, f)
	}
	if len(o.Overlays) > MaxVideoOverlays {
		v.fail(f+".overlays", "at most "+strconv.Itoa(MaxVideoOverlays)+" overlays")
	} else if len(o.Overlays) > 0 && validEnum(o.Container, numVideoContainers) && !o.Container.HasVideoStream() {
		v.fail(f+".overlays", o.Container.String()+" has no video")
	}
	for i := range min(len(o.Overlays), MaxVideoOverlays) {
		o.Overlays[i].validate(v, f+".overlays["+strconv.Itoa(i)+"]")
	}
}

func (o *VideoOverlay) validate(v *validator, f string) {
	wm := o.Watermark
	if o.Timecode != VideoTimecodeNone {
		wm.Color = nil // the timecode's colour, not a text's
	}
	wm.validate(v, f)
	for _, t := range []struct {
		name string
		d    time.Duration
	}{{"start", o.Start}, {"end", o.End}, {"fade_in", o.FadeIn}, {"fade_out", o.FadeOut}} {
		if t.d < 0 || t.d > MaxVideoTime {
			v.fail(f+"."+t.name, between("0", MaxVideoTime.String()))
		}
	}
	switch {
	case o.End != 0 && o.End <= o.Start:
		v.fail(f+".end", "must be after start")
	case o.End != 0 && o.FadeIn+o.FadeOut > o.End-o.Start:
		v.fail(f+".fade_out", "the fades are longer than the overlay")
	}
	v.enum(f+".timecode", validEnum(o.Timecode, numVideoTimecodes))
	v.intRange(f+".timecode_size", o.TimecodeSize, 0, MaxTimecodeSize)
	if o.Timecode == VideoTimecodeNone {
		if o.TimecodeSize != 0 {
			v.fail(f+".timecode_size", "needs a timecode")
		}
		return
	}
	// FFmpeg draws the timecode: only what drawtext does from numbers
	// applies.
	if o.Source != nil || o.Text != "" || o.Font != "" || o.TextWidth != 0 {
		v.fail(f+".timecode", "a timecode has no source, text, font or text width")
	}
	if o.Tile != TileNone || o.Scale != 0 || o.Width != 0 || o.Height != 0 || o.Rotate != 0 {
		v.fail(f+".timecode", "a timecode is not tiled, scaled, sized or rotated")
	}
	switch o.Position.Type {
	case GravityUnset, GravityCenter, GravityNorth, GravitySouth, GravityEast, GravityWest,
		GravityNorthEast, GravityNorthWest, GravitySouthEast, GravitySouthWest:
	default:
		v.fail(f+".position.type", "a timecode takes a compass gravity")
	}
}

// ---------------------------------------------------------------------------
// VideoTimecode

// VideoTimecode is the format of a timecode overlay.
type VideoTimecode uint8

// VideoTimecode values. The zero value is no timecode.
const (
	VideoTimecodeNone    VideoTimecode = iota
	VideoTimecodeHMS                   // HH:MM:SS.mmm
	VideoTimecodeSeconds               // seconds with microseconds: 12.345678
)

const numVideoTimecodes = 3

func (t VideoTimecode) String() string {
	switch t {
	case VideoTimecodeNone:
		return "none"
	case VideoTimecodeHMS:
		return "hms"
	case VideoTimecodeSeconds:
		return "seconds"
	}
	return invalidEnum("VideoTimecode", uint8(t))
}

// ParseVideoTimecode parses a canonical VideoTimecode name.
func ParseVideoTimecode(s string) (VideoTimecode, error) {
	return parseEnum[VideoTimecode]("VideoTimecode", s, numVideoTimecodes)
}

// MarshalText implements encoding.TextMarshaler.
func (t VideoTimecode) MarshalText() ([]byte, error) {
	return marshalEnum("VideoTimecode", t, numVideoTimecodes)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (t *VideoTimecode) UnmarshalText(b []byte) error {
	return unmarshalEnum("VideoTimecode", t, b, numVideoTimecodes)
}

// validateAudio checks the audio options of WP-123.
func (o *VideoOp) validateAudio(v *validator, f string) {
	if o.SampleRate != 0 && !slices.Contains(SampleRates, o.SampleRate) {
		v.fail(f+".sample_rate", "must be one of 8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 88200, 96000")
	}
	v.intRange(f+".channels", o.Channels, 0, 2)
	if o.Volume != 0 {
		v.floatRange(f+".volume", o.Volume, MinVolume, MaxVolume)
	}
	if l := o.Loudnorm; l != nil {
		v.floatRange(f+".loudnorm.integrated", l.Integrated, MinLoudnessIntegrated, MaxLoudnessIntegrated)
		v.floatRange(f+".loudnorm.true_peak", l.TruePeak, MinLoudnessTruePeak, MaxLoudnessTruePeak)
		v.floatRange(f+".loudnorm.range", l.Range, MinLoudnessRange, MaxLoudnessRange)
	}
	if o.Mute && (o.Volume != 0 || o.Loudnorm != nil || o.SampleRate != 0 || o.Channels != 0) {
		v.fail(f+".mute", "a muted output has no audio options")
	}
}

// ---------------------------------------------------------------------------
// VideoFit

// VideoFit is how a video fills its Width × Height box.
type VideoFit uint8

// VideoFit values. The zero value is VideoFitContain.
const (
	// VideoFitContain scales the frame to fit inside the box, keeping its
	// aspect ratio (enlarging it when smaller); the output may be smaller
	// than the box on one side.
	VideoFitContain VideoFit = iota
	// VideoFitScaleDown is VideoFitContain without enlarging.
	VideoFitScaleDown
	// VideoFitCover fills the box and crops the excess, centred.
	VideoFitCover
	// VideoFitPad fits inside the box and fills the bars with Background.
	VideoFitPad
	// VideoFitFill stretches the frame to the box.
	VideoFitFill
)

const numVideoFits = 5

func (f VideoFit) String() string {
	switch f {
	case VideoFitContain:
		return "contain"
	case VideoFitScaleDown:
		return "scale-down"
	case VideoFitCover:
		return "cover"
	case VideoFitPad:
		return "pad"
	case VideoFitFill:
		return "fill"
	}
	return invalidEnum("VideoFit", uint8(f))
}

// NeedsBox reports whether f needs both Width and Height.
func (f VideoFit) NeedsBox() bool {
	return f == VideoFitCover || f == VideoFitPad || f == VideoFitFill
}

// ParseVideoFit parses a canonical VideoFit name.
func ParseVideoFit(s string) (VideoFit, error) {
	return parseEnum[VideoFit]("VideoFit", s, numVideoFits)
}

// MarshalText implements encoding.TextMarshaler.
func (f VideoFit) MarshalText() ([]byte, error) { return marshalEnum("VideoFit", f, numVideoFits) }

// UnmarshalText implements encoding.TextUnmarshaler.
func (f *VideoFit) UnmarshalText(b []byte) error {
	return unmarshalEnum("VideoFit", f, b, numVideoFits)
}

// ---------------------------------------------------------------------------
// VideoGravity

// VideoGravity is an anchor in the frame: the centre, an edge or a corner.
type VideoGravity uint8

// VideoGravity values. The zero value is VideoGravityCenter.
const (
	VideoGravityCenter VideoGravity = iota
	VideoGravityNorth
	VideoGravitySouth
	VideoGravityEast
	VideoGravityWest
	VideoGravityNorthEast
	VideoGravityNorthWest
	VideoGravitySouthEast
	VideoGravitySouthWest
)

const numVideoGravities = 9

func (g VideoGravity) String() string {
	switch g {
	case VideoGravityCenter:
		return "center"
	case VideoGravityNorth:
		return "north"
	case VideoGravitySouth:
		return "south"
	case VideoGravityEast:
		return "east"
	case VideoGravityWest:
		return "west"
	case VideoGravityNorthEast:
		return "north-east"
	case VideoGravityNorthWest:
		return "north-west"
	case VideoGravitySouthEast:
		return "south-east"
	case VideoGravitySouthWest:
		return "south-west"
	}
	return invalidEnum("VideoGravity", uint8(g))
}

// Anchor returns where g sits on each axis: 0 is the left (top) edge, 1
// the centre and 2 the right (bottom) edge.
func (g VideoGravity) Anchor() (x, y int) {
	switch g {
	case VideoGravityCenter:
		return 1, 1
	case VideoGravityNorth:
		return 1, 0
	case VideoGravitySouth:
		return 1, 2
	case VideoGravityEast:
		return 2, 1
	case VideoGravityWest:
		return 0, 1
	case VideoGravityNorthEast:
		return 2, 0
	case VideoGravityNorthWest:
		return 0, 0
	case VideoGravitySouthEast:
		return 2, 2
	case VideoGravitySouthWest:
		return 0, 2
	}
	return 1, 1
}

// ParseVideoGravity parses a canonical VideoGravity name.
func ParseVideoGravity(s string) (VideoGravity, error) {
	return parseEnum[VideoGravity]("VideoGravity", s, numVideoGravities)
}

// MarshalText implements encoding.TextMarshaler.
func (g VideoGravity) MarshalText() ([]byte, error) {
	return marshalEnum("VideoGravity", g, numVideoGravities)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (g *VideoGravity) UnmarshalText(b []byte) error {
	return unmarshalEnum("VideoGravity", g, b, numVideoGravities)
}

// ---------------------------------------------------------------------------
// VideoContainer

// VideoContainer is the output file format of a video transformation.
type VideoContainer uint8

// VideoContainer values. The zero value is VideoContainerMP4.
const (
	VideoContainerMP4  VideoContainer = iota // MP4: H.264, AV1 or HEVC + AAC
	VideoContainerWebM                       // WebM: VP9 or AV1 + Opus
	VideoContainerM4A                        // MP4 audio only: AAC
	VideoContainerOpus                       // Ogg Opus, audio only
	VideoContainerMP3                        // MP3, audio only
	VideoContainerOgg                        // Ogg Opus with the .ogg name, audio only (WP-123)
	VideoContainerFLAC                       // FLAC, audio only
	VideoContainerWAV                        // WAV (16-bit PCM), audio only
	VideoContainerPNG                        // a waveform picture (VideoOp.Waveform)
	VideoContainerWebP                       // a waveform picture (VideoOp.Waveform)
)

const numVideoContainers = 10

func (c VideoContainer) String() string {
	switch c {
	case VideoContainerMP4:
		return "mp4"
	case VideoContainerWebM:
		return "webm"
	case VideoContainerM4A:
		return "m4a"
	case VideoContainerOpus:
		return "opus"
	case VideoContainerMP3:
		return "mp3"
	case VideoContainerOgg:
		return "ogg"
	case VideoContainerFLAC:
		return "flac"
	case VideoContainerWAV:
		return "wav"
	case VideoContainerPNG:
		return "png"
	case VideoContainerWebP:
		return "webp"
	}
	return invalidEnum("VideoContainer", uint8(c))
}

// AudioOnly reports an audio file: a container without a video stream.
func (c VideoContainer) AudioOnly() bool {
	switch c {
	case VideoContainerM4A, VideoContainerOpus, VideoContainerMP3, VideoContainerOgg, VideoContainerFLAC, VideoContainerWAV:
		return true
	default:
		return false
	}
}

// Image reports a picture of the audio (a waveform) instead of a media
// file.
func (c VideoContainer) Image() bool { return c == VideoContainerPNG || c == VideoContainerWebP }

// HasVideoStream reports a container that holds a video stream (MP4,
// WebM).
func (c VideoContainer) HasVideoStream() bool { return !c.AudioOnly() && !c.Image() }

// AllowsVideo reports whether c can hold vc (auto always; nothing but auto
// in an audio-only container).
func (c VideoContainer) AllowsVideo(vc VideoCodec) bool {
	if vc == VideoCodecAuto {
		return true
	}
	switch c {
	case VideoContainerMP4:
		return vc == VideoCodecH264 || vc == VideoCodecAV1 || vc == VideoCodecHEVC
	case VideoContainerWebM:
		return vc == VideoCodecVP9 || vc == VideoCodecAV1
	default:
		return false
	}
}

// AllowsAudio reports whether c can hold ac (auto always).
func (c VideoContainer) AllowsAudio(ac AudioCodec) bool {
	switch {
	case ac == AudioCodecAuto:
		return true
	case c == VideoContainerMP4 || c == VideoContainerM4A:
		return ac == AudioCodecAAC
	case c == VideoContainerWebM || c == VideoContainerOpus || c == VideoContainerOgg:
		return ac == AudioCodecOpus
	case c == VideoContainerMP3:
		return ac == AudioCodecMP3
	case c == VideoContainerFLAC:
		return ac == AudioCodecFLAC
	case c == VideoContainerWAV:
		return ac == AudioCodecPCM
	default:
		return false
	}
}

// ParseVideoContainer parses a canonical VideoContainer name.
func ParseVideoContainer(s string) (VideoContainer, error) {
	return parseEnum[VideoContainer]("VideoContainer", s, numVideoContainers)
}

// MarshalText implements encoding.TextMarshaler.
func (c VideoContainer) MarshalText() ([]byte, error) {
	return marshalEnum("VideoContainer", c, numVideoContainers)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (c *VideoContainer) UnmarshalText(b []byte) error {
	return unmarshalEnum("VideoContainer", c, b, numVideoContainers)
}

// ---------------------------------------------------------------------------
// VideoCodec

// VideoCodec is the video codec of a video transformation's output.
type VideoCodec uint8

// VideoCodec values. The zero value is VideoCodecAuto.
const (
	VideoCodecAuto VideoCodec = iota // the container's default for this build
	VideoCodecH264                   // libx264, gpl images only
	VideoCodecAV1                    // libaom-av1
	VideoCodecVP9                    // libvpx-vp9
	VideoCodecHEVC                   // libx265, gpl images only
)

const numVideoCodecs = 5

func (c VideoCodec) String() string {
	switch c {
	case VideoCodecAuto:
		return "auto"
	case VideoCodecH264:
		return "h264"
	case VideoCodecAV1:
		return "av1"
	case VideoCodecVP9:
		return "vp9"
	case VideoCodecHEVC:
		return "hevc"
	}
	return invalidEnum("VideoCodec", uint8(c))
}

// ParseVideoCodec parses a canonical VideoCodec name.
func ParseVideoCodec(s string) (VideoCodec, error) {
	return parseEnum[VideoCodec]("VideoCodec", s, numVideoCodecs)
}

// MarshalText implements encoding.TextMarshaler.
func (c VideoCodec) MarshalText() ([]byte, error) {
	return marshalEnum("VideoCodec", c, numVideoCodecs)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (c *VideoCodec) UnmarshalText(b []byte) error {
	return unmarshalEnum("VideoCodec", c, b, numVideoCodecs)
}

// ---------------------------------------------------------------------------
// AudioCodec

// AudioCodec is the audio codec of a video transformation's output.
type AudioCodec uint8

// AudioCodec values. The zero value is AudioCodecAuto.
const (
	AudioCodecAuto AudioCodec = iota // the container's default
	AudioCodecAAC                    // FFmpeg's native AAC encoder
	AudioCodecOpus                   // libopus
	AudioCodecMP3                    // LAME
	AudioCodecFLAC                   // FFmpeg's FLAC encoder (WP-123)
	AudioCodecPCM                    // 16-bit little-endian PCM (WAV)
)

const numAudioCodecs = 6

func (c AudioCodec) String() string {
	switch c {
	case AudioCodecAuto:
		return "auto"
	case AudioCodecAAC:
		return "aac"
	case AudioCodecOpus:
		return "opus"
	case AudioCodecMP3:
		return "mp3"
	case AudioCodecFLAC:
		return "flac"
	case AudioCodecPCM:
		return "pcm"
	}
	return invalidEnum("AudioCodec", uint8(c))
}

// ParseAudioCodec parses a canonical AudioCodec name.
func ParseAudioCodec(s string) (AudioCodec, error) {
	return parseEnum[AudioCodec]("AudioCodec", s, numAudioCodecs)
}

// MarshalText implements encoding.TextMarshaler.
func (c AudioCodec) MarshalText() ([]byte, error) {
	return marshalEnum("AudioCodec", c, numAudioCodecs)
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (c *AudioCodec) UnmarshalText(b []byte) error {
	return unmarshalEnum("AudioCodec", c, b, numAudioCodecs)
}
