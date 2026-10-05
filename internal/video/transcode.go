package video

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Video transformations (WP-121, ADR 0012): the cgo-free half of a
// transcode. Resolve picks the muxer and codecs of a transform.VideoOp from
// what the linked FFmpeg can do (TranscodeCaps, probed at startup), before
// anything is fetched; PlanTranscode turns the op and the probed source
// (TranscodeInput) into a TranscodePlan: concrete sizes, rates, encoder
// settings and the libavfilter chains internal/video/ffmpeg runs.
//
// The filter chains are written here from numbers only (integers and
// floats formatted by strconv, and fixed filter and option names): no text
// of a request ever reaches FFmpeg's filter syntax.

// AudioCodec is an audio encoder of video transformations.
type AudioCodec uint8

// AudioCodec values.
const (
	AudioNone AudioCodec = iota
	AudioAAC             // FFmpeg's native AAC encoder
	AudioOpus            // libopus
	AudioMP3             // libmp3lame
)

// String is the codec's name as ffprobe reports it ("aac", "opus", "mp3");
// "" for AudioNone.
func (c AudioCodec) String() string {
	switch c {
	case AudioAAC:
		return "aac"
	case AudioOpus:
		return "opus"
	case AudioMP3:
		return "mp3"
	default:
		return ""
	}
}

// Encoder is FFmpeg's name of the encoder that writes c.
func (c AudioCodec) Encoder() string {
	switch c {
	case AudioAAC:
		return "aac"
	case AudioOpus:
		return "libopus"
	case AudioMP3:
		return "libmp3lame"
	default:
		return ""
	}
}

// sampleFormat is the sample format c's encoder takes (aformat).
func (c AudioCodec) sampleFormat() string {
	if c == AudioOpus {
		return "flt"
	}
	return "fltp"
}

// defaultBitrate is c's bit rate when the request sets none.
func (c AudioCodec) defaultBitrate(channels int) int64 {
	switch c {
	case AudioOpus:
		if channels == 1 {
			return 64_000
		}
		return 96_000
	default:
		if channels == 1 {
			return 96_000
		}
		return 128_000
	}
}

// sampleRate is the output rate of c for a source at rate: Opus is always
// 48 kHz; AAC and MP3 keep a rate they support, else 48 kHz and 44.1 kHz.
func (c AudioCodec) sampleRate(rate int) int {
	switch c {
	case AudioOpus:
		return 48_000
	case AudioMP3:
		switch rate {
		case 8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000:
			return rate
		}
		return 44_100
	default:
		switch rate {
		case 8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 64000, 88200, 96000:
			return rate
		}
		return 48_000
	}
}

// TranscodeCaps is what the linked FFmpeg can do for video
// transformations, probed once at startup (ffmpeg.TranscodeCaps).
type TranscodeCaps struct {
	// Video holds the video encoders.
	Video EncoderSet
	// AAC, Opus and MP3 are the audio encoders (aac, libopus,
	// libmp3lame).
	AAC, Opus, MP3 bool
	// Muxers.
	MuxMP4, MuxWebM, MuxOgg, MuxMP3 bool
	// VideoFilters and AudioFilters report the libavfilter filters the
	// plans use (scale, crop, pad, transpose, fps, trim, …; abuffer,
	// atrim, aresample, aformat, …).
	VideoFilters, AudioFilters bool
}

// HasAudio reports whether c encodes a.
func (c TranscodeCaps) HasAudio(a AudioCodec) bool {
	switch a {
	case AudioAAC:
		return c.AAC
	case AudioOpus:
		return c.Opus
	case AudioMP3:
		return c.MP3
	default:
		return false
	}
}

// Names lists what c can write ("vp9", "av1", "aac", "mp4", …), for the
// startup log.
func (c TranscodeCaps) Names() []string {
	out := c.Video.Names()
	for _, a := range [...]AudioCodec{AudioAAC, AudioOpus, AudioMP3} {
		if c.HasAudio(a) {
			out = append(out, a.String())
		}
	}
	return out
}

// TranscodeOutput is the resolved output of a VideoOp: what is written,
// known before the source is fetched.
type TranscodeOutput struct {
	Container transform.VideoContainer
	// Muxer is FFmpeg's muxer name ("mp4", "webm", "ogg", "mp3").
	Muxer string
	// Video is the video codec; CodecNone for audio-only containers.
	Video Codec
	// Audio is the codec a source's audio is written in; AudioNone when
	// the request mutes it.
	Audio AudioCodec
	// ContentType and Ext describe the file.
	ContentType string
	Ext         string
}

// Key identifies the output for cache keys and ETags: the same request
// answered by images with different encoders (full: AV1 MP4, gpl: H.264
// MP4) must not share them.
func (o TranscodeOutput) Key() string {
	return o.Container.String() + ":" + o.Video.String() + ":" + o.Audio.String()
}

// unsupported is a 422 whose public message says what this image cannot
// write.
func unsupported(public string) error {
	return errs.WithPublic(errs.New(errs.KindUnsupported, "video: "+public), public)
}

// Resolve picks the muxer and codecs of op on this build: webm is VP9 (or
// AV1) with Opus; mp4 is H.264 when libx264 is linked, else AV1, or HEVC on
// request, with AAC; m4a is AAC, opus is Opus in Ogg, mp3 is LAME.
// Something this build cannot write is errs.KindUnsupported (422).
func (c TranscodeCaps) Resolve(op *transform.VideoOp) (TranscodeOutput, error) {
	if op == nil {
		return TranscodeOutput{}, errs.New(errs.KindInternal, "video: no video op")
	}
	out := TranscodeOutput{Container: op.Container}
	var muxOK bool
	switch op.Container {
	case transform.VideoContainerMP4:
		out.Muxer, out.ContentType, out.Ext, out.Audio, muxOK = "mp4", "video/mp4", ".mp4", AudioAAC, c.MuxMP4
	case transform.VideoContainerWebM:
		out.Muxer, out.ContentType, out.Ext, out.Audio, muxOK = "webm", "video/webm", ".webm", AudioOpus, c.MuxWebM
	case transform.VideoContainerM4A:
		out.Muxer, out.ContentType, out.Ext, out.Audio, muxOK = "mp4", "audio/mp4", ".m4a", AudioAAC, c.MuxMP4
	case transform.VideoContainerOpus:
		out.Muxer, out.ContentType, out.Ext, out.Audio, muxOK = "ogg", "audio/ogg", ".opus", AudioOpus, c.MuxOgg
	case transform.VideoContainerMP3:
		out.Muxer, out.ContentType, out.Ext, out.Audio, muxOK = "mp3", "audio/mpeg", ".mp3", AudioMP3, c.MuxMP3
	default:
		return TranscodeOutput{}, errs.Newf(errs.KindInternal, "video: container %s", op.Container)
	}
	if !muxOK {
		return TranscodeOutput{}, unsupported(op.Container.String() + " output is not supported by this image")
	}
	if op.Container.AudioOnly() {
		if !c.HasAudio(out.Audio) || !c.AudioFilters {
			return TranscodeOutput{}, unsupported(out.Audio.String() + " audio output is not supported by this image")
		}
		return out, nil
	}
	if op.Mute {
		out.Audio = AudioNone
	}
	if !c.VideoFilters {
		return TranscodeOutput{}, unsupported("video transformations are not supported by this image")
	}
	switch op.VideoCodec {
	case transform.VideoCodecAuto:
		switch {
		case op.Container == transform.VideoContainerWebM:
			out.Video = CodecVP9
		case c.Video.H264:
			out.Video = CodecH264
		default:
			out.Video = CodecAV1
		}
	case transform.VideoCodecH264:
		out.Video = CodecH264
	case transform.VideoCodecAV1:
		out.Video = CodecAV1
	case transform.VideoCodecVP9:
		out.Video = CodecVP9
	case transform.VideoCodecHEVC:
		out.Video = CodecHEVC
	default:
		return TranscodeOutput{}, errs.Newf(errs.KindInternal, "video: codec %s", op.VideoCodec)
	}
	if !c.Video.Has(out.Video) {
		return TranscodeOutput{}, unsupported(out.Video.String() + " video output is not supported by this image")
	}
	return out, nil
}

// TranscodeInput is what the transcoder probed from a source.
type TranscodeInput struct {
	// Container is the demuxer ("mov", "matroska", "avi").
	Container string
	// Duration is the container duration in seconds; 0 when unknown.
	Duration float64
	// StartTime is the container's first timestamp in seconds (the trim
	// and timestamp offsets are relative to it).
	StartTime float64

	// Video reports a video stream; the fields below describe it.
	Video          bool
	VideoCodec     string
	Width, Height  int // coded size
	SARNum, SARDen int // sample aspect ratio; 0 is square
	// Rotation (clockwise degrees) and HFlip are the display matrix: the
	// frame is mirrored first, then rotated.
	Rotation int
	HFlip    bool
	FPS      float64 // average frame rate; 0 when unknown

	// Audio reports an audio stream; AudioDecodable that this build has
	// its decoder.
	Audio          bool
	AudioCodec     string
	AudioDecodable bool
	SampleRate     int
	Channels       int
}

// DisplaySize is the frame size with the sample aspect ratio applied,
// before rotation.
func (in *TranscodeInput) DisplaySize() (int, int) {
	if in.SARNum > 0 && in.SARDen > 0 && in.SARNum != in.SARDen {
		w := int(math.Round(float64(in.Width) * float64(in.SARNum) / float64(in.SARDen)))
		return max(w, 1), in.Height
	}
	return in.Width, in.Height
}

// TranscodeSettings are the configured defaults and limits of video
// transformations (XI_VIDEO_TRANSFORM_*, XI_VIDEO_ENCODE_*).
type TranscodeSettings struct {
	// Encode holds the CRF, speed and preset of each codec.
	Encode EncodeSettings
	// Threads is the decoder and encoder threads of one transcode.
	Threads int
	// MaxOutputPixels caps the output frame; MaxOutputDuration the output
	// length; MaxFPS the frame rate (a faster source is reduced to it);
	// MaxOutputBytes the file. 0 is unlimited.
	MaxOutputPixels   int64
	MaxOutputDuration time.Duration
	MaxFPS            float64
	MaxOutputBytes    int64
}

// TranscodePlan is everything one transcode runs: computed from validated
// numbers, ready for internal/video/ffmpeg.
type TranscodePlan struct {
	Output TranscodeOutput
	// Start and End are the cut on the source timeline in seconds
	// (StartTime included): the demuxer seeks to Start, and reading stops
	// past End. End 0 runs to the end.
	Start, End float64
	// Duration is the expected output length in seconds; 0 when unknown.
	Duration float64
	// FastStart moves MP4's moov box to the front.
	FastStart bool
	MaxBytes  int64
	Threads   int
	// Video and Audio are nil when the output has no such stream.
	Video *VideoStreamPlan
	Audio *AudioStreamPlan
}

// VideoStreamPlan is the video stream of a transcode.
//
//nolint:revive // "video stream" is the domain term; AudioStreamPlan is its sibling
type VideoStreamPlan struct {
	Codec         Codec
	Width, Height int
	// FPS is the output frame rate (a rate-control and keyframe hint when
	// the source's timestamps are kept).
	FPS        float64
	CRF        int
	Speed      int
	Preset     string
	MaxBitrate int64
	// GOP is the keyframe interval in frames (two seconds).
	GOP int
	// Filters is the libavfilter chain from the decoder's frames to the
	// encoder's (trim, orientation, crop, fit, fps, pixel format).
	Filters string
}

// AudioStreamPlan is the audio stream of a transcode.
type AudioStreamPlan struct {
	Codec      AudioCodec
	SampleRate int
	Channels   int
	Bitrate    int64
	// Filters is the chain from the decoder's frames to the encoder's
	// (trim, resampling, sample format and layout).
	Filters string
}

// limit is a 422 limit_exceeded with a public message.
func limit(public string) error {
	return errs.WithPublic(errs.New(errs.KindLimitExceeded, "video: "+public), public)
}

// PlanTranscode plans op over the probed source in, for out (Resolve).
// Errors: errs.KindUnsupported when the source lacks a stream the output
// needs or its audio cannot be transcoded by this build;
// errs.KindLimitExceeded when the output is over a limit of s or the cut
// starts past the end.
func PlanTranscode(in *TranscodeInput, op *transform.VideoOp, out TranscodeOutput, caps TranscodeCaps, s *TranscodeSettings) (*TranscodePlan, error) {
	p := &TranscodePlan{
		Output:    out,
		FastStart: out.Muxer == "mp4",
		MaxBytes:  s.MaxOutputBytes,
		Threads:   max(s.Threads, 1),
	}
	if err := p.cut(in, op, s); err != nil {
		return nil, err
	}
	if out.Video != CodecNone {
		if !in.Video {
			return nil, unsupported("the source has no video stream")
		}
		v, err := planVideo(in, op, out.Video, p, s)
		if err != nil {
			return nil, err
		}
		p.Video = v
	}
	if out.Audio != AudioNone && in.Audio {
		if !in.AudioDecodable || !caps.HasAudio(out.Audio) || !caps.AudioFilters {
			return nil, unsupported("this image cannot transcode the audio of this video; mute it")
		}
		p.Audio = planAudio(in, op, out.Audio, p)
	}
	if out.Video == CodecNone && p.Audio == nil {
		return nil, unsupported("the source has no audio stream")
	}
	return p, nil
}

// cut sets the time range and checks the output duration.
func (p *TranscodePlan) cut(in *TranscodeInput, op *transform.VideoOp, s *TranscodeSettings) error {
	start := op.Start.Seconds()
	end := op.EndTime().Seconds()
	if in.Duration > 0 {
		if start >= in.Duration {
			return limit("start is past the end of the video")
		}
		if end == 0 || end > in.Duration {
			end = in.Duration
		}
	}
	maxDur := s.MaxOutputDuration.Seconds()
	switch {
	case end > 0:
		p.Duration = end - start
		if maxDur > 0 && p.Duration > maxDur+1e-3 {
			return limit("the output is longer than the duration limit of " + s.MaxOutputDuration.String())
		}
	case maxDur > 0:
		// Unknown duration: the cut stops at the limit.
		end = start + maxDur
	}
	p.Start = in.StartTime + start
	if op.EndTime() > 0 || (in.Duration == 0 && end > 0) {
		p.End = in.StartTime + end
	}
	return nil
}

// planVideo computes the geometry, rate and encoder settings.
func planVideo(in *TranscodeInput, op *transform.VideoOp, codec Codec, p *TranscodePlan, s *TranscodeSettings) (*VideoStreamPlan, error) {
	var f filterChain
	f.trim("trim", "setpts", p, op)

	// Anamorphic sources are first scaled to square pixels (scale keeps
	// the display aspect, so the sample aspect ratio becomes 1:1).
	w, h := in.Width, in.Height
	if dw, dh := in.DisplaySize(); dw != w || dh != h {
		w, h = even(dw), even(dh)
		f.add("scale=" + itoa(w) + ":" + itoa(h) + ":flags=bicubic")
	}
	// Display matrix, then the request's rotation and flips.
	o := orientation{flip: in.HFlip, rot: in.Rotation}.
		rotate(op.Rotate).flipH(op.FlipH).flipV(op.FlipV)
	f.add(o.filters()...)
	if o.rot%180 != 0 {
		w, h = h, w
	}
	if c := op.Crop; c != nil {
		if c.X >= w || c.Y >= h {
			return nil, limit("the crop is outside the video")
		}
		cw, ch := even(min(c.Width, w-c.X)), even(min(c.Height, h-c.Y))
		f.add("crop=" + itoa(cw) + ":" + itoa(ch) + ":" + itoa(c.X) + ":" + itoa(c.Y))
		w, h = cw, ch
	}
	ow, oh := fit(&f, w, h, op)
	if s.MaxOutputPixels > 0 && int64(ow)*int64(oh) > s.MaxOutputPixels {
		return nil, limit(itoa(ow) + "x" + itoa(oh) + " is over the output pixel limit")
	}

	fps := in.FPS
	switch {
	case op.FPS > 0:
		if s.MaxFPS > 0 && op.FPS > s.MaxFPS {
			return nil, limit("the frame rate is over the limit of " + ftoa(s.MaxFPS))
		}
		fps = op.FPS
		f.add("fps=" + ftoa(fps))
	case s.MaxFPS > 0 && fps > s.MaxFPS:
		fps = s.MaxFPS
		f.add("fps=" + ftoa(fps))
	}
	f.add("format=yuv420p")

	v := &VideoStreamPlan{
		Codec: codec, Width: ow, Height: oh, FPS: fps,
		MaxBitrate: op.MaxBitrate, Filters: f.String(),
	}
	if fps <= 0 {
		v.FPS = 25 // a rate-control hint only: the timestamps are the source's
	}
	v.GOP = min(max(int(math.Round(v.FPS*2)), 1), 300)
	def := DefaultCRF(codec)
	if c, ok := s.Encode.CRF[codec]; ok {
		def = c
	}
	switch {
	case op.CRF > 0:
		v.CRF = min(op.CRF, codec.MaxCRF())
	default:
		v.CRF = CRF(codec, op.Quality, def)
	}
	v.Speed = DefaultSpeed
	if sp, ok := s.Encode.Speed[codec]; ok {
		v.Speed = sp
	}
	v.Preset = DefaultPreset
	if pr, ok := s.Encode.Preset[codec]; ok && pr != "" {
		v.Preset = pr
	}
	return v, nil
}

// planAudio computes the audio chain and encoder settings.
func planAudio(in *TranscodeInput, op *transform.VideoOp, codec AudioCodec, p *TranscodePlan) *AudioStreamPlan {
	ch := min(max(in.Channels, 1), 2)
	a := &AudioStreamPlan{
		Codec:      codec,
		SampleRate: codec.sampleRate(in.SampleRate),
		Channels:   ch,
		Bitrate:    op.AudioBitrate,
	}
	if a.Bitrate == 0 {
		a.Bitrate = codec.defaultBitrate(ch)
	}
	layout := "stereo"
	if ch == 1 {
		layout = "mono"
	}
	var f filterChain
	f.trim("atrim", "asetpts", p, op)
	f.add("aresample=" + itoa(a.SampleRate))
	f.add("aformat=sample_fmts=" + codec.sampleFormat() + ":sample_rates=" + itoa(a.SampleRate) +
		":channel_layouts=" + layout)
	a.Filters = f.String()
	return a
}

// fit scales (and crops or pads) a w × h frame into op's box and returns
// the output size, even on both sides.
func fit(f *filterChain, w, h int, op *transform.VideoOp) (int, int) {
	bw, bh := op.Width, op.Height
	if bw > 0 {
		bw = even(bw)
	}
	if bh > 0 {
		bh = even(bh)
	}
	scale := func(sw, sh int) {
		if sw != w || sh != h {
			f.add("scale=" + itoa(sw) + ":" + itoa(sh) + ":flags=bicubic")
		}
	}
	switch {
	case bw == 0 && bh == 0:
		ow, oh := even(w), even(h)
		if ow != w || oh != h {
			f.add("crop=" + itoa(ow) + ":" + itoa(oh) + ":0:0")
		}
		return ow, oh
	case op.Fit == transform.VideoFitFill:
		ow, oh := even(bw), even(bh)
		scale(ow, oh)
		return ow, oh
	case op.Fit == transform.VideoFitCover:
		ow, oh := even(bw), even(bh)
		k := max(float64(ow)/float64(w), float64(oh)/float64(h))
		sw, sh := max(evenCeil(float64(w)*k), ow), max(evenCeil(float64(h)*k), oh)
		scale(sw, sh)
		if sw != ow || sh != oh {
			f.add("crop=" + itoa(ow) + ":" + itoa(oh) + ":" + itoa(((sw-ow)/2)&^1) + ":" + itoa(((sh-oh)/2)&^1))
		}
		return ow, oh
	case op.Fit == transform.VideoFitPad:
		ow, oh := even(bw), even(bh)
		k := min(float64(ow)/float64(w), float64(oh)/float64(h))
		sw, sh := min(even(int(math.Round(float64(w)*k))), ow), min(even(int(math.Round(float64(h)*k))), oh)
		scale(sw, sh)
		if sw != ow || sh != oh {
			b := op.Background
			color := "0x" + hex2(b.R) + hex2(b.G) + hex2(b.B)
			f.add("pad=" + itoa(ow) + ":" + itoa(oh) + ":" + itoa(((ow-sw)/2)&^1) + ":" + itoa(((oh-sh)/2)&^1) +
				":color=" + color)
		}
		return ow, oh
	default: // contain, scale-down
		var k float64
		switch {
		case bw == 0:
			k = float64(bh) / float64(h)
		case bh == 0:
			k = float64(bw) / float64(w)
		default:
			k = min(float64(bw)/float64(w), float64(bh)/float64(h))
		}
		if op.Fit == transform.VideoFitScaleDown {
			k = min(k, 1)
		}
		ow, oh := even(int(math.Round(float64(w)*k))), even(int(math.Round(float64(h)*k)))
		if k == 1 {
			ow, oh = even(w), even(h)
			if ow != w || oh != h {
				f.add("crop=" + itoa(ow) + ":" + itoa(oh) + ":0:0")
			}
			return ow, oh
		}
		scale(ow, oh)
		return ow, oh
	}
}

// orientation is a mirror followed by a clockwise rotation: the display
// matrix of a source composed with the request's rotation and flips.
type orientation struct {
	flip bool
	rot  int // 0, 90, 180, 270
}

func (o orientation) norm() orientation {
	o.rot = ((o.rot % 360) + 360) % 360 / 90 * 90 // multiples of 90 only
	return o
}

// rotate turns the result clockwise by deg.
func (o orientation) rotate(deg int) orientation {
	o.rot += deg
	return o.norm()
}

// flipH mirrors the result: H∘R(r)∘F = R(−r)∘H∘F.
func (o orientation) flipH(on bool) orientation {
	if !on {
		return o.norm()
	}
	return orientation{flip: !o.flip, rot: -o.rot}.norm()
}

// flipV mirrors the result vertically: V = R(180)∘H.
func (o orientation) flipV(on bool) orientation {
	if !on {
		return o.norm()
	}
	return orientation{flip: !o.flip, rot: 180 - o.rot}.norm()
}

// filters are the libavfilter filters applying o.
func (o orientation) filters() []string {
	o = o.norm()
	switch {
	case !o.flip && o.rot == 90:
		return []string{"transpose=clock"}
	case !o.flip && o.rot == 180:
		return []string{"hflip", "vflip"}
	case !o.flip && o.rot == 270:
		return []string{"transpose=cclock"}
	case o.flip && o.rot == 0:
		return []string{"hflip"}
	case o.flip && o.rot == 90:
		return []string{"transpose=clock_flip"}
	case o.flip && o.rot == 180:
		return []string{"vflip"}
	case o.flip && o.rot == 270:
		return []string{"transpose=cclock_flip"}
	default:
		return nil
	}
}

// filterChain builds a comma-separated libavfilter chain.
type filterChain struct{ parts []string }

func (f *filterChain) add(parts ...string) { f.parts = append(f.parts, parts...) }

func (f *filterChain) String() string {
	if len(f.parts) == 0 {
		return "null"
	}
	return strings.Join(f.parts, ",")
}

// trim adds the cut (trim or atrim) and the timestamp shift (setpts or
// asetpts) that starts the output at 0 on both streams alike.
func (f *filterChain) trim(trim, setpts string, p *TranscodePlan, op *transform.VideoOp) {
	switch {
	case op.Start > 0 && p.End > 0:
		f.add(trim + "=start=" + ftoa(p.Start) + ":end=" + ftoa(p.End))
	case op.Start > 0:
		f.add(trim + "=start=" + ftoa(p.Start))
	case p.End > 0:
		f.add(trim + "=end=" + ftoa(p.End))
	}
	switch {
	case p.Start > 0:
		f.add(setpts + "=PTS-" + ftoa(p.Start) + "/TB")
	case p.Start < 0: // a negative container start (edit lists, codec delay)
		f.add(setpts + "=PTS+" + ftoa(-p.Start) + "/TB")
	}
}

// even rounds n down to an even number, at least 2.
func even(n int) int { return max(n&^1, 2) }

// evenCeil rounds x up to an even integer, at least 2.
func evenCeil(x float64) int {
	n := int(math.Ceil(x - 1e-9))
	return max(n+n&1, 2)
}

func itoa(n int) string { return strconv.Itoa(n) }

// ftoa formats a seconds or rate value: at most microsecond precision, no
// exponent.
func ftoa(x float64) string { return strconv.FormatFloat(math.Round(x*1e6)/1e6, 'f', -1, 64) }

func hex2(b uint8) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&15]})
}
