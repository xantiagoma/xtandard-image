package transform

import (
	"strconv"
	"time"
)

// Video transformations (WP-121, ADR 0012): a video source transcoded into
// a video (or audio-only) file, like Cloudinary's video transformations and
// Cloudflare Media Transformations. A request of KindVideo carries a
// VideoOp; the image fields (Pipelines, Output) do not apply to it.
//
// The model is plain data like the rest of the package. Every value is a
// number or an enum: the filter graph FFmpeg runs is built from these
// numbers only (internal/video.TranscodePlan), never from text a URL
// carried.

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

	// Container is the output file format.
	Container VideoContainer
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

	// Overlays are images or text drawn over the video (WP-124). Not
	// implemented yet: a request with any is refused.
	Overlays []VideoOverlay
}

// VideoCrop is a rectangle of the source in pixels, after the rotation.
type VideoCrop struct {
	X, Y, Width, Height int
}

// VideoOverlay is a placeholder for WP-124 (image and text overlays).
type VideoOverlay struct {
	Source SourceSpec
	// X and Y place the overlay's top-left corner in output pixels.
	X, Y int
	// Start and End limit when it is shown; 0 is the whole output.
	Start, End time.Duration
}

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
)

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
func (o *VideoOp) HasVideo() bool { return !o.Container.AudioOnly() }

// HasAudio reports whether the output asks for an audio stream (the source
// may still have none).
func (o *VideoOp) HasAudio() bool { return !o.Mute }

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
	if o.Fit.NeedsBox() && (o.Width == 0 || o.Height == 0) {
		v.fail(f+".fit", o.Fit.String()+" needs both width and height")
	}
	if c := o.Crop; c != nil {
		v.intRange(f+".crop.x", c.X, 0, MaxVideoDimension)
		v.intRange(f+".crop.y", c.Y, 0, MaxVideoDimension)
		v.intRange(f+".crop.width", c.Width, 2, MaxVideoDimension)
		v.intRange(f+".crop.height", c.Height, 2, MaxVideoDimension)
	}
	switch o.Rotate {
	case 0, 90, 180, 270:
	default:
		v.fail(f+".rotate", "must be 0, 90, 180 or 270")
	}
	if o.FPS != 0 {
		v.floatRange(f+".fps", o.FPS, 1, MaxVideoFPS)
	}
	v.enum(f+".container", validEnum(o.Container, numVideoContainers))
	v.enum(f+".video_codec", validEnum(o.VideoCodec, numVideoCodecs))
	v.enum(f+".audio_codec", validEnum(o.AudioCodec, numAudioCodecs))
	if validEnum(o.Container, numVideoContainers) && validEnum(o.VideoCodec, numVideoCodecs) &&
		validEnum(o.AudioCodec, numAudioCodecs) {
		if !o.Container.AllowsVideo(o.VideoCodec) {
			v.fail(f+".video_codec", o.VideoCodec.String()+" cannot be muxed into "+o.Container.String())
		}
		if !o.Container.AllowsAudio(o.AudioCodec) {
			v.fail(f+".audio_codec", o.AudioCodec.String()+" cannot be muxed into "+o.Container.String())
		}
		if o.Mute && o.Container.AudioOnly() {
			v.fail(f+".mute", o.Container.String()+" is audio only")
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
	if len(o.Overlays) > 0 {
		v.fail(f+".overlays", "video overlays are not supported yet")
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
)

const numVideoContainers = 5

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
	}
	return invalidEnum("VideoContainer", uint8(c))
}

// AudioOnly reports a container without a video stream.
func (c VideoContainer) AudioOnly() bool {
	return c == VideoContainerM4A || c == VideoContainerOpus || c == VideoContainerMP3
}

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
	case c == VideoContainerWebM || c == VideoContainerOpus:
		return ac == AudioCodecOpus
	case c == VideoContainerMP3:
		return ac == AudioCodecMP3
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
)

const numAudioCodecs = 4

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
