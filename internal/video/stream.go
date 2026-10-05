package video

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Adaptive streaming (WP-126, ADR 0012 §6): the cgo-free half of HLS and
// DASH. PlanStream lays a stream out from the probe alone (renditions of
// the ABR ladder no larger than the source, their codecs and the segment
// grid), so manifests are written before anything is encoded. A video
// segment is its own transcode (PlanSegment): the frames of
// [n·d, (n+1)·d) on a constant frame-rate grid, a keyframe first, encoded
// into a CMAF fragment whose decode time is its place in the stream, so
// segments encoded on their own join without gaps. The audio is encoded
// once for the whole stream (PlanAudioTrack) and cut into fragments on
// the same grid, so the encoder's priming exists once, at the start, and
// segment boundaries carry no gaps or clicks.

// Rung is one rendition of an ABR ladder.
type Rung struct {
	// Height is the short side of the frame in pixels: 720 is 1280×720 for
	// a 16:9 landscape source and 720×1280 for a portrait one.
	Height int
	// Bitrate is the rendition's peak video bit rate in bits per second;
	// 0 is RungBitrate(Height).
	Bitrate int64
}

// standardBitrates are the peak video bit rates of the usual rungs
// (H.264-class; AV1 and VP9 spend less for the same quality under the
// same cap).
var standardBitrates = map[int]int64{
	144: 200_000, 240: 400_000, 360: 800_000, 480: 1_400_000, 540: 2_000_000,
	720: 2_800_000, 1080: 5_000_000, 1440: 10_000_000, 2160: 16_000_000,
}

// DefaultLadder is XI_VIDEO_TRANSFORM_ABR_LADDER's default: 1080p, 720p,
// 480p, 360p and 240p with standardBitrates.
func DefaultLadder() []Rung {
	return []Rung{{1080, 5_000_000}, {720, 2_800_000}, {480, 1_400_000}, {360, 800_000}, {240, 400_000}}
}

// RungBitrate is the bit rate of a rung of height h: the standard one, or
// 720p's scaled by the pixel ratio to the power 0.75.
func RungBitrate(h int) int64 {
	if b, ok := standardBitrates[h]; ok {
		return b
	}
	b := float64(standardBitrates[720]) * math.Pow(float64(h)*float64(h)/(720*720), 0.75)
	return max(int64(math.Round(b/1000))*1000, transform.MinVideoBitrate)
}

// StreamSettings are the configured stream defaults (XI_VIDEO_TRANSFORM_*).
type StreamSettings struct {
	// Ladder is the renditions a stream offers unless its URL names some
	// (XI_VIDEO_TRANSFORM_ABR_LADDER); empty is DefaultLadder.
	Ladder []Rung
	// SegmentDuration is the segment length
	// (XI_VIDEO_TRANSFORM_SEGMENT_DURATION, whole seconds); 0 is
	// DefaultSegmentDuration.
	SegmentDuration time.Duration
}

// DefaultSegmentDuration is the segment length when nothing sets one.
const DefaultSegmentDuration = 6 * time.Second

// Stream audio: AAC-LC at 48 kHz, whose encoder primes 1024 samples.
const (
	streamSampleRate = 48_000
	aacPriming       = 1024
	// clock90k is the MPEG clock of HLS and of the video track.
	clock90k = 90_000
)

// ResolveStream picks the codecs of a stream on this build: the video is
// H.264 when libx264 is linked, else AV1 (or the VP9 or AV1 the request
// names); the audio is AAC; both in CMAF (fragmented MP4).
// errs.KindUnsupported (422) when the build cannot write it.
func (c TranscodeCaps) ResolveStream(op *transform.VideoOp) (TranscodeOutput, error) {
	if op == nil || op.Stream == nil {
		return TranscodeOutput{}, errs.New(errs.KindInternal, "video: not a stream")
	}
	if !c.MuxMP4 || !c.VideoFilters {
		return TranscodeOutput{}, unsupported("adaptive streaming is not supported by this image")
	}
	out := TranscodeOutput{Container: transform.VideoContainerMP4, Muxer: "mp4", Audio: AudioAAC}
	switch op.Stream.Protocol {
	case transform.StreamDASH:
		out.ContentType, out.Ext = "application/dash+xml", ".mpd"
	default:
		out.ContentType, out.Ext = "application/vnd.apple.mpegurl", ".m3u8"
	}
	switch op.VideoCodec {
	case transform.VideoCodecAuto:
		out.Video = CodecAV1
		if c.Video.H264 {
			out.Video = CodecH264
		}
	case transform.VideoCodecH264:
		out.Video = CodecH264
	case transform.VideoCodecAV1:
		out.Video = CodecAV1
	case transform.VideoCodecVP9:
		out.Video = CodecVP9
	default:
		return TranscodeOutput{}, unsupported(op.VideoCodec.String() + " streams are not supported")
	}
	if !c.Video.Has(out.Video) {
		return TranscodeOutput{}, unsupported(out.Video.String() + " video output is not supported by this image")
	}
	if op.Mute {
		out.Audio = AudioNone
	} else if !c.AAC || !c.AudioFilters {
		return TranscodeOutput{}, unsupported("aac audio output is not supported by this image; mute it")
	}
	if err := c.resolveEffects(op); err != nil {
		return TranscodeOutput{}, err
	}
	return out, nil
}

// Key identifies a stream's output in cache keys: the stream's codecs.
// Its form differs from a file's ("s:" first), so a stream and a file of
// the same request never share an entry.
func (o TranscodeOutput) streamKey() string { return "s:" + o.Key() }

// StreamKey is TranscodeOutput.Key for a stream (ResolveStream's output).
func (o TranscodeOutput) StreamKey() string { return o.streamKey() }

// Rendition is one video rendition of a stream.
type Rendition struct {
	// Name is the rendition's path segment: "v0" (the largest), "v1", …
	Name string
	// Width and Height are the frame size (even).
	Width, Height int
	// FPSNum / FPSDen is the constant frame rate.
	FPSNum, FPSDen int
	// Bitrate is the peak video bit rate (the encoder's cap).
	Bitrate int64
	Codec   Codec
	// Level is the codec level the encoder signals: H.264 level_idc (×10),
	// AV1 seq_level_idx, VP9 level (×10).
	Level int
	// Codecs is the RFC 6381 codec string ("avc1.64001f", "av01.0.05M.08",
	// "vp09.00.31.08").
	Codecs string
}

// FPS is the frame rate.
func (r *Rendition) FPS() float64 { return float64(r.FPSNum) / float64(r.FPSDen) }

// AudioRendition is a stream's audio track.
type AudioRendition struct {
	Codec      AudioCodec
	SampleRate int
	Channels   int
	Bitrate    int64
	// Codecs is "mp4a.40.2" (AAC-LC).
	Codecs string
	// Priming is the encoder's priming in samples.
	Priming int
}

// StreamLayout is a stream as the manifests describe it: everything known
// from the probe before any segment is encoded.
type StreamLayout struct {
	Protocol transform.StreamProtocol
	Output   TranscodeOutput
	// Start is where the stream begins on the source's timeline in seconds
	// (the container's start time and the request's cut included).
	Start float64
	// Duration is the stream's length in seconds.
	Duration float64
	// SegmentSeconds is the segment length (every segment but the last);
	// Segments is their number.
	SegmentSeconds int
	Segments       int
	// Video holds the renditions, largest first.
	Video []Rendition
	// Audio is nil for a muted stream or a source without audio.
	Audio *AudioRendition
	// Offset90k is how much later than the source's time every video and
	// audio sample is presented, in 1/90000 s (streamOffset).
	Offset90k int64
}

// SegmentStart and SegmentDuration are segment n's span on the stream's
// timeline, in seconds.
func (l *StreamLayout) SegmentStart(n int) float64 { return float64(n * l.SegmentSeconds) }

// SegmentDuration is segment n's length in seconds (the last one is
// shorter).
func (l *StreamLayout) SegmentDuration(n int) float64 {
	return max(min(float64((n+1)*l.SegmentSeconds), l.Duration)-l.SegmentStart(n), 0)
}

// Bandwidth is the peak bit rate of rendition r with the audio, for the
// manifests' BANDWIDTH and bandwidth: the video cap with a quarter of
// headroom (encoder buffering and container overhead) plus the audio.
func (l *StreamLayout) Bandwidth(r *Rendition) int64 {
	b := r.Bitrate + r.Bitrate/4
	if l.Audio != nil {
		b += l.Audio.Bitrate + l.Audio.Bitrate/10
	}
	return b
}

// AudioBandwidth is the audio track's peak bit rate (0 without audio).
func (l *StreamLayout) AudioBandwidth() int64 {
	if l.Audio == nil {
		return 0
	}
	return l.Audio.Bitrate + l.Audio.Bitrate/10
}

// Rendition returns the video rendition named name.
func (l *StreamLayout) Rendition(name string) (*Rendition, bool) {
	for i := range l.Video {
		if l.Video[i].Name == name {
			return &l.Video[i], true
		}
	}
	return nil, false
}

// PlanStream lays out a stream of op over the probed source in, for out
// (ResolveStream). Errors: errs.KindUnsupported when the source has no
// video or an unknown duration, or its audio cannot be transcoded;
// errs.KindLimitExceeded when the cut starts past the end, a rendition is
// over the output pixel limit or the frame rate over the limit.
func PlanStream(in *TranscodeInput, op *transform.VideoOp, out TranscodeOutput, caps TranscodeCaps, s *TranscodeSettings, ss *StreamSettings) (*StreamLayout, error) {
	st := op.Stream
	if st == nil {
		return nil, errs.New(errs.KindInternal, "video: not a stream")
	}
	if !in.Video {
		return nil, unsupported("the source has no video stream")
	}
	if in.Duration <= 0 {
		return nil, unsupported("streaming needs a source of known duration")
	}
	l := &StreamLayout{Protocol: st.Protocol, Output: out}
	start := op.Start.Seconds()
	end := op.EndTime().Seconds()
	if start >= in.Duration {
		return nil, limit("start is past the end of the video")
	}
	if end == 0 || end > in.Duration {
		end = in.Duration
	}
	l.Start = in.StartTime + start
	l.Duration = end - start

	num, den, err := streamRate(in, op, s)
	if err != nil {
		return nil, err
	}
	seg := st.SegmentDuration
	if seg == 0 {
		seg = ss.SegmentDuration
	}
	if seg <= 0 {
		seg = DefaultSegmentDuration
	}
	l.SegmentSeconds = max(int(seg/time.Second), 1)
	// The last segment exists when it holds at least half a frame.
	half := 0.5 * float64(den) / float64(num)
	l.Segments = max(int(math.Ceil((l.Duration-half)/float64(l.SegmentSeconds))), 1)

	var f filterChain
	w, h, err := orientCrop(&f, in, op)
	if err != nil {
		return nil, err
	}
	for i, r := range ladder(st, ss, min(w, h)) {
		k := float64(r.Height) / float64(min(w, h))
		rw, rh := evenRound(float64(w)*k), evenRound(float64(h)*k)
		if k >= 1 {
			rw, rh = even(w), even(h)
		}
		if s.MaxOutputPixels > 0 && int64(rw)*int64(rh) > s.MaxOutputPixels {
			return nil, limit(itoa(rw) + "x" + itoa(rh) + " is over the output pixel limit")
		}
		rd := Rendition{
			Name: "v" + itoa(i), Width: rw, Height: rh, FPSNum: num, FPSDen: den,
			Bitrate: r.Bitrate, Codec: out.Video,
		}
		rd.Level, rd.Codecs = CodecString(&rd)
		l.Video = append(l.Video, rd)
	}
	if out.Audio != AudioNone && in.Audio {
		if !in.AudioDecodable || !caps.HasAudio(AudioAAC) || !caps.AudioFilters {
			return nil, unsupported("this image cannot transcode the audio of this video; mute it")
		}
		ch := min(max(in.Channels, 1), 2)
		if op.Channels > 0 {
			ch = op.Channels
		}
		a := &AudioRendition{
			Codec: AudioAAC, SampleRate: streamSampleRate, Channels: ch, Bitrate: op.AudioBitrate,
			Codecs: "mp4a.40.2", Priming: aacPriming,
		}
		if a.Bitrate == 0 {
			a.Bitrate = AudioAAC.defaultBitrate(ch)
		}
		l.Audio = a
	}
	l.Offset90k = streamOffset(l, out.Video, num, den)
	return l, nil
}

// streamOffset is how much later than the source every sample of a stream
// is presented, in 1/90000 s: at least the audio encoder's priming (the
// priming packet must not start before 0) and the video encoder's
// reordering delay (H.264 B-frames: no decode time before 0), so that
// composition offsets stay non-negative, which every MP4 reader agrees
// on. A multiple of 15 ticks, so it is exact at 48 kHz too.
func streamOffset(l *StreamLayout, codec Codec, num, den int) int64 {
	var off int64
	if codec == CodecH264 { // x264 delays by 2 frames with B-pyramids, 1 without
		off += 2 * ceilDiv(clock90k*int64(den), int64(num))
	}
	if a := l.Audio; a != nil {
		off += ceilDiv(int64(a.Priming)*clock90k, int64(a.SampleRate))
	}
	return ceilDiv(off, 15) * 15
}

// evenRound rounds x to the nearest even integer, at least 2 (854 for
// 480p's 853.3).
func evenRound(x float64) int { return max(2*int(math.Round(x/2)), 2) }

// ceilDiv is ⌈a/b⌉ for a ≥ 0, b > 0.
func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

// streamRate is the stream's constant frame rate as a fraction: the
// request's, else the source's (25 when unknown), capped by the
// configured maximum.
func streamRate(in *TranscodeInput, op *transform.VideoOp, s *TranscodeSettings) (num, den int, err error) {
	fps := in.FPS
	switch {
	case op.FPS > 0:
		if s.MaxFPS > 0 && op.FPS > s.MaxFPS {
			return 0, 0, limit("the frame rate is over the limit of " + ftoa(s.MaxFPS))
		}
		fps = op.FPS
	case fps <= 0:
		fps = 25
	case s.MaxFPS > 0 && fps > s.MaxFPS:
		fps = s.MaxFPS
	}
	num, den = rational(fps)
	return num, den, nil
}

// rational writes a frame rate as a fraction: NTSC rates as n·1000/1001,
// others in thousandths.
func rational(fps float64) (num, den int) {
	if n := math.Round(fps * 1.001); n >= 1 && math.Abs(fps-n*1000/1001) < 1e-3 && math.Abs(fps-n) > 1e-3 {
		return int(n) * 1000, 1001
	}
	if math.Abs(fps-math.Round(fps)) < 1e-6 {
		return int(math.Round(fps)), 1
	}
	n := int(math.Round(fps * 1000))
	g := gcd(n, 1000)
	return n / g, 1000 / g
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// ladder returns the rungs of a stream, largest first: the URL's or the
// configured ones, within the stream's bounds, none above the source's
// short side; a source smaller than every rung gets one rung of its own
// size.
func ladder(st *transform.VideoStream, ss *StreamSettings, short int) []Rung {
	base := ss.Ladder
	if len(base) == 0 {
		base = DefaultLadder()
	}
	var rungs []Rung
	if len(st.Renditions) > 0 {
		for _, h := range st.Renditions {
			r := Rung{Height: h}
			if i := slices.IndexFunc(base, func(b Rung) bool { return b.Height == h }); i >= 0 {
				r.Bitrate = base[i].Bitrate
			}
			rungs = append(rungs, r)
		}
	} else {
		rungs = slices.Clone(base)
	}
	for i := range rungs {
		if rungs[i].Bitrate == 0 {
			rungs[i].Bitrate = RungBitrate(rungs[i].Height)
		}
	}
	slices.SortFunc(rungs, func(a, b Rung) int { return b.Height - a.Height })
	rungs = slices.CompactFunc(rungs, func(a, b Rung) bool { return a.Height == b.Height })
	smallest := rungs[len(rungs)-1]
	rungs = slices.DeleteFunc(rungs, func(r Rung) bool {
		return (st.MinHeight > 0 && r.Height < st.MinHeight) || (st.MaxHeight > 0 && r.Height > st.MaxHeight)
	})
	if st.MaxRenditions > 0 && len(rungs) > st.MaxRenditions {
		rungs = rungs[:st.MaxRenditions]
	}
	fits := slices.DeleteFunc(slices.Clone(rungs), func(r Rung) bool { return r.Height > short })
	if len(fits) == 0 {
		// Never upscale: one rendition at the source's size, with the
		// smallest rung's bit rate scaled down to it when the source is
		// smaller still.
		b := smallest.Bitrate
		if len(rungs) > 0 {
			b = rungs[len(rungs)-1].Bitrate
		}
		return []Rung{{Height: even(short), Bitrate: min(b, RungBitrate(even(short)))}}
	}
	return fits
}

// PlanSegment plans segment n of rendition r of layout l: the frames of
// [n·d, (n+1)·d) of the stream on its frame-rate grid, oriented, cropped
// and scaled to the rendition, encoded on their own with a keyframe first
// into a CMAF fragment placed on the stream's timeline.
func PlanSegment(in *TranscodeInput, op *transform.VideoOp, l *StreamLayout, r *Rendition, n int, s *TranscodeSettings) (*TranscodePlan, error) {
	if n < 0 || n >= l.Segments {
		return nil, errs.Newf(errs.KindNotFound, "video: segment %d of %d", n, l.Segments)
	}
	t0, t1 := l.SegmentStart(n), l.SegmentStart(n)+l.SegmentDuration(n)
	p := &TranscodePlan{
		Output:   l.Output,
		Start:    l.Start + t0,
		End:      l.Start + t1,
		Duration: l.Duration,
		MaxBytes: s.MaxOutputBytes,
		Threads:  max(s.Threads, 1),
		Fragment: &FragmentPlan{Discont: true, Offset90k: l.Offset90k},
	}
	var f filterChain
	// The segment's frames on the source's timeline, then the stream's
	// clock: 0 is the stream's start, for every segment alike.
	f.add("trim=start=" + ftoa(p.Start) + ":end=" + ftoa(p.End))
	shift(&f, "setpts", l.Start)
	w, h, err := orientCrop(&f, in, op)
	if err != nil {
		return nil, err
	}
	if w != r.Width || h != r.Height {
		f.add("scale=" + itoa(r.Width) + ":" + itoa(r.Height) + ":flags=bicubic")
	}
	// The constant-rate grid k·den/num of the whole stream: this segment's
	// first frame is the first grid point at or after its start (the
	// source frame there, or the next one held back to it), its last the
	// last before its end.
	k0 := int64(math.Ceil(t0*float64(r.FPSNum)/float64(r.FPSDen) - 1e-9))
	g := float64(k0) * float64(r.FPSDen) / float64(r.FPSNum)
	f.add("fps=fps=" + itoa(r.FPSNum) + "/" + itoa(r.FPSDen) + ":start_time=" + ftoa(g))
	f.add("trim=end=" + ftoa(t1))
	if op.FadeIn > 0 {
		f.add("fade=t=in:st=0:d=" + ftoa(op.FadeIn.Seconds()))
	}
	if op.FadeOut > 0 {
		f.add("fade=t=out:st=" + ftoa(max(l.Duration-op.FadeOut.Seconds(), 0)) + ":d=" + ftoa(op.FadeOut.Seconds()))
	}
	f.add("format=yuv420p")
	v := &VideoStreamPlan{
		Codec: r.Codec, Width: r.Width, Height: r.Height, FPS: r.FPS(),
		MaxBitrate: r.Bitrate, Filters: f.String(),
	}
	// One keyframe per segment (its first frame), or every
	// KeyframeInterval within it.
	gop := float64(l.SegmentSeconds)
	if op.KeyframeInterval > 0 {
		gop = op.KeyframeInterval.Seconds()
	}
	v.GOP = min(max(int(math.Ceil(v.FPS*gop-1e-9)), 1), maxGOP)
	if r.Codec == CodecH264 {
		v.Level = r.Level
	}
	encoderSettings(v, op, s)
	p.Video = v
	return p, nil
}

// PlanAudioTrack plans the audio of layout l: the whole stream encoded
// once into AAC, fragmented on the segment grid.
func PlanAudioTrack(in *TranscodeInput, op *transform.VideoOp, l *StreamLayout, s *TranscodeSettings) (*TranscodePlan, error) {
	if l.Audio == nil {
		return nil, errs.New(errs.KindNotFound, "video: the stream has no audio")
	}
	p := &TranscodePlan{
		Output:   l.Output,
		Start:    l.Start,
		End:      l.Start + l.Duration,
		Duration: l.Duration,
		MaxBytes: s.MaxOutputBytes,
		Threads:  max(s.Threads, 1),
		Fragment: &FragmentPlan{Discont: true, Seconds: l.SegmentSeconds, Priming: l.Audio.Priming, Offset90k: l.Offset90k},
	}
	// planAudio cuts [p.Start, p.End) (from op's start) and puts the
	// stream's start at 0, as for a file.
	a := *op
	a.SampleRate, a.Channels = l.Audio.SampleRate, l.Audio.Channels
	a.AudioBitrate = l.Audio.Bitrate
	plan, err := planAudio(in, &a, AudioAAC, p)
	if err != nil {
		return nil, err
	}
	p.Audio = plan
	return p, nil
}

// shift adds the timestamp shift (setpts or asetpts) that puts start at 0.
func shift(f *filterChain, setpts string, start float64) {
	switch {
	case start > 0:
		f.add(setpts + "=PTS-" + ftoa(start) + "/TB")
	case start < 0:
		f.add(setpts + "=PTS+" + ftoa(-start) + "/TB")
	}
}

// CodecString is the level and the RFC 6381 codec string of r: High
// profile H.264 at the lowest level (3.0 at least) that holds its frame
// size, macroblock rate and bit rate, which the encoder is told to
// signal; AV1 Main and VP9 profile 0 at the level their encoders derive
// from the frame size and rate. Tests compare them with the init
// segments' configuration records.
func CodecString(r *Rendition) (int, string) {
	fps := r.FPS()
	switch r.Codec {
	case CodecH264:
		lv := h264Level(r.Width, r.Height, fps, r.Bitrate)
		return lv, fmt.Sprintf("avc1.6400%02x", lv)
	case CodecAV1:
		lv := av1Level(r.Width, r.Height, fps)
		return lv, fmt.Sprintf("av01.0.%02dM.08", lv)
	case CodecVP9:
		lv := vp9Level(r.Width, r.Height, fps)
		return lv, fmt.Sprintf("vp09.00.%02d.08", lv)
	default:
		return 0, ""
	}
}

// h264Levels is ITU-T H.264 Table A-1: level_idc, MaxMBPS, MaxFS (in
// macroblocks) and MaxBR (kbit/s, Main; High allows 1.25×).
var h264Levels = [...][4]int64{
	{30, 40_500, 1_620, 10_000}, {31, 108_000, 3_600, 14_000}, {32, 216_000, 5_120, 20_000},
	{40, 245_760, 8_192, 20_000}, {41, 245_760, 8_192, 50_000}, {42, 522_240, 8_704, 50_000},
	{50, 589_824, 22_080, 135_000}, {51, 983_040, 36_864, 240_000}, {52, 2_073_600, 36_864, 240_000},
	{60, 4_177_920, 139_264, 240_000}, {61, 8_355_840, 139_264, 480_000}, {62, 16_711_680, 139_264, 800_000},
}

func h264Level(w, h int, fps float64, bitrate int64) int {
	mbw, mbh := int64((w+15)/16), int64((h+15)/16)
	fs := mbw * mbh
	mbps := int64(math.Ceil(float64(fs) * fps))
	for _, l := range h264Levels {
		side := int64(math.Sqrt(float64(l[2]) * 8))
		if fs <= l[2] && mbps <= l[1] && mbw <= side && mbh <= side && bitrate <= l[3]*1250 {
			return int(l[0])
		}
	}
	return 62
}

// av1Levels is AV1 Annex A.3: seq_level_idx, MaxPicSize, MaxHSize,
// MaxVSize and MaxDisplayRate.
var av1Levels = [...][5]int64{
	{0, 147_456, 2_048, 1_152, 4_423_680}, {1, 278_784, 2_816, 1_584, 8_363_520},
	{4, 665_856, 4_352, 2_448, 19_975_680}, {5, 1_065_024, 5_504, 3_096, 31_950_720},
	{8, 2_359_296, 6_144, 3_456, 70_778_880}, {9, 2_359_296, 6_144, 3_456, 141_557_760},
	{12, 8_912_896, 8_192, 4_352, 267_386_880}, {13, 8_912_896, 8_192, 4_352, 534_773_760},
	{14, 8_912_896, 8_192, 4_352, 1_069_547_520}, {16, 35_651_584, 16_384, 8_704, 1_069_547_520},
	{17, 35_651_584, 16_384, 8_704, 2_139_095_040}, {18, 35_651_584, 16_384, 8_704, 4_278_190_080},
}

func av1Level(w, h int, fps float64) int {
	pic := int64(w) * int64(h)
	rate := int64(math.Ceil(float64(pic) * fps))
	for _, l := range av1Levels {
		if pic <= l[1] && int64(w) <= l[2] && int64(h) <= l[3] && rate <= l[4] {
			return int(l[0])
		}
	}
	return 31
}

// vp9Levels is the VP9 level table as FFmpeg's vpcc.c applies it: level
// (×10) and max picture size. The MP4 muxer passes vpcc.c no frame rate,
// so the luma sample rate never decides: the first level whose picture
// size holds the frame is written.
var vp9Levels = [...][2]int64{
	{10, 36_864}, {11, 73_728}, {20, 122_880}, {21, 245_760}, {30, 552_960}, {31, 983_040},
	{40, 2_228_224}, {50, 8_912_896}, {60, 35_651_584},
}

func vp9Level(w, h int, _ float64) int {
	pic := int64(w) * int64(h)
	for _, l := range vp9Levels {
		if pic <= l[1] {
			return int(l[0])
		}
	}
	return 0
}
