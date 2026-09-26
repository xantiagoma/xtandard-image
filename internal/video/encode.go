package video

import (
	"math"
	"time"
)

// Video output (WP-76, docs/FORMATS.md §5.1): animated results encoded as
// MP4 or WebM. Which codec a container gets depends on the encoders the
// linked FFmpeg has (EncoderSet, probed at startup), never on
// configuration: webm is VP9 everywhere, mp4 is H.264 when libx264 exists
// (the gpl image) and AV1 otherwise (the full image), and hevc is HEVC in
// MP4, only with libx265.

// Codec is a video encoder.
type Codec uint8

// Codec values.
const (
	CodecNone Codec = iota
	CodecVP9        // libvpx-vp9
	CodecAV1        // libaom-av1
	CodecH264       // libx264
	CodecHEVC       // libx265
)

// String is the codec's name as ffprobe reports it ("vp9", "av1", "h264",
// "hevc"); "" for CodecNone.
func (c Codec) String() string {
	switch c {
	case CodecVP9:
		return "vp9"
	case CodecAV1:
		return "av1"
	case CodecH264:
		return "h264"
	case CodecHEVC:
		return "hevc"
	default:
		return ""
	}
}

// Encoder is FFmpeg's name of the encoder that writes c.
func (c Codec) Encoder() string {
	switch c {
	case CodecVP9:
		return "libvpx-vp9"
	case CodecAV1:
		return "libaom-av1"
	case CodecH264:
		return "libx264"
	case CodecHEVC:
		return "libx265"
	default:
		return ""
	}
}

// MaxCRF is the top of c's CRF scale (worst quality): 63 for VP9 and AV1,
// 51 for H.264 and HEVC.
func (c Codec) MaxCRF() int {
	if c == CodecH264 || c == CodecHEVC {
		return 51
	}
	return 63
}

// Alpha reports whether c keeps an alpha channel (VP9's yuva420p, stored
// as a second stream in WebM's BlockAdditional).
func (c Codec) Alpha() bool { return c == CodecVP9 }

// Container is an output container.
type Container uint8

// Container values.
const (
	ContainerNone Container = iota
	ContainerMP4
	ContainerWebM
)

// Muxer is FFmpeg's muxer name ("mp4", "webm").
func (c Container) Muxer() string {
	switch c {
	case ContainerMP4:
		return "mp4"
	case ContainerWebM:
		return "webm"
	default:
		return ""
	}
}

// EncoderSet reports the video encoders the linked FFmpeg has.
type EncoderSet struct {
	VP9, AV1, H264, HEVC bool
}

// Has reports whether s holds c.
func (s EncoderSet) Has(c Codec) bool {
	switch c {
	case CodecVP9:
		return s.VP9
	case CodecAV1:
		return s.AV1
	case CodecH264:
		return s.H264
	case CodecHEVC:
		return s.HEVC
	default:
		return false
	}
}

// Names lists the codecs of s ("vp9", "av1", ...), for the startup log.
func (s EncoderSet) Names() []string {
	var out []string
	for _, c := range [...]Codec{CodecVP9, CodecAV1, CodecH264, CodecHEVC} {
		if s.Has(c) {
			out = append(out, c.String())
		}
	}
	return out
}

// Output is a requested video output format.
type Output uint8

// Output values: the transform formats mp4, webm and hevc.
const (
	OutputMP4 Output = iota + 1
	OutputWebM
	OutputHEVC
)

// Resolve applies the output contract of FORMATS §5.1 to the encoders of
// s: the codec and container of o, or ok false when s cannot write it.
func (s EncoderSet) Resolve(o Output) (Codec, Container, bool) {
	switch o {
	case OutputWebM:
		return CodecVP9, ContainerWebM, s.VP9
	case OutputMP4:
		switch {
		case s.H264:
			return CodecH264, ContainerMP4, true
		case s.AV1:
			return CodecAV1, ContainerMP4, true
		}
	case OutputHEVC:
		return CodecHEVC, ContainerMP4, s.HEVC
	}
	return CodecNone, ContainerNone, false
}

// EncodeSettings are the configured encoder defaults (XI_VIDEO_ENCODE_*).
type EncodeSettings struct {
	// CRF is the constant rate factor of each codec when the request sets
	// no quality.
	CRF map[Codec]int
	// Speed is libvpx's and libaom's cpu-used per codec (higher is faster).
	Speed map[Codec]int
	// Preset is x264's and x265's preset per codec ("veryfast").
	Preset map[Codec]string
	// MaxFrames, MaxDuration and MaxPixels (per frame) bound what is
	// encoded; 0 is unlimited.
	MaxFrames   int
	MaxDuration time.Duration
	MaxPixels   int64
}

// Default CRFs and speeds (docs/CONFIG.md, XI_VIDEO_ENCODE_*).
const (
	DefaultVP9CRF  = 36
	DefaultAV1CRF  = 36
	DefaultH264CRF = 25
	DefaultHEVCCRF = 28
	DefaultSpeed   = 8
	DefaultPreset  = "veryfast"
	// ReferenceQuality is the request quality that maps to the default
	// CRF (the image default, XI_FORMATS_QUALITY).
	ReferenceQuality = 80
)

// DefaultCRF is c's default CRF.
func DefaultCRF(c Codec) int {
	switch c {
	case CodecAV1:
		return DefaultAV1CRF
	case CodecH264:
		return DefaultH264CRF
	case CodecHEVC:
		return DefaultHEVCCRF
	default:
		return DefaultVP9CRF
	}
}

// CRF maps a request quality (1–100; ≤ 0 unset) onto c's CRF scale,
// anchored at def (the configured CRF): quality 80 is def, 100 is the
// best CRF (a quarter of def) and 1 the worst (MaxCRF), linear in
// between.
func CRF(c Codec, quality, def int) int {
	hi := c.MaxCRF()
	def = min(max(def, 0), hi)
	if quality <= 0 {
		return def
	}
	q := float64(min(quality, 100))
	lo := float64(def) / 4
	var crf float64
	if q >= ReferenceQuality {
		crf = float64(def) - (q-ReferenceQuality)/(100-ReferenceQuality)*(float64(def)-lo)
	} else {
		crf = float64(def) + (ReferenceQuality-q)/(ReferenceQuality-1)*float64(hi-def)
	}
	return min(max(int(math.Round(crf)), 0), hi)
}

// EncodeParams describes one encode.
type EncodeParams struct {
	Codec     Codec
	Container Container
	// Width and Height are even (4:2:0 chroma).
	Width, Height int
	// Alpha encodes the alpha channel (yuva420p); Codec must support it.
	Alpha bool
	// Bands is 3 (RGB) or 4 (RGBA): the layout of the frames handed over.
	Bands  int
	CRF    int
	Speed  int
	Preset string
	// FPS is the average frame rate, a rate-control hint (timestamps are
	// exact, in milliseconds).
	FPS float64
	// MaxBytes bounds the muxed output; 0 is unlimited.
	MaxBytes int64
}

// FrameDelay is the delay in milliseconds a frame of an animation is shown
// for in a video: browsers show a GIF frame of 10 ms or less (0 included)
// for 100 ms, and a video must match what the GIF looked like.
func FrameDelay(ms int) int {
	if ms <= 10 {
		return 100
	}
	return ms
}
