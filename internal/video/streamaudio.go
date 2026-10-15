package video

import (
	"math"
	"strconv"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Chunked stream audio (WP-131, ADR 0012 §6). A stream's AAC track is
// encoded in chunks of whole segments (StreamSettings.AudioChunk), each
// its own transcode, so a long source never needs one long audio job, and
// the chunks join as if the track had been encoded at once:
//
//   - Every chunk puts its packets on the one packet grid of the track:
//     packet j covers output samples [g0 + (j-1)·1024, g0 + j·1024) of the
//     stream (48 kHz, 0 at its start; packet 0 is the encoder's priming),
//     because the encoder's input starts on that grid (an atrim on the
//     48 kHz timeline) and the AAC encoder stamps each packet with the
//     timestamp of its first input sample minus the priming.
//   - Its input starts audioPreRoll packets before its first one, so the
//     encoder's overlapping windows, its psychoacoustic state and the
//     source decoder are warm when the first kept packet is encoded; the
//     shim drops the packets before the chunk's first segment
//     (FragmentPlan.First), and those after its last one (FragmentPlan.End)
//     that the post-roll and the flush produce.
//   - The resampler (sources not at 48 kHz) starts on a source sample whose
//     distance from the track's first one is a multiple of the rate ratio's
//     numerator, so its output samples fall on the same instants in every
//     chunk (no sub-sample shift at the joins).
//
// This needs timestamps that name source samples exactly: an audio time
// base of 1/(k·rate) (MP4, MOV, Ogg, FLAC, WAV, MP3). Sources with coarser
// timestamps (Matroska and WebM count milliseconds) cannot place a chunk's
// first sample exactly, and loudnorm adapts its gain over the whole track,
// so those streams keep one audio transcode for the whole track (the
// WP-126 behaviour).

// DefaultAudioChunk is XI_VIDEO_TRANSFORM_AUDIO_CHUNK's default.
const DefaultAudioChunk = 30 * time.Second

const (
	// aacFrame is AAC-LC's frame: samples per packet.
	aacFrame = 1024
	// audioPreRoll is how many packets of audio a chunk encodes before its
	// first kept one (about 0.34 s at 48 kHz).
	audioPreRoll = 16
	// audioPostRoll is how many packets it encodes after its last kept one
	// (the encoder looks ahead one frame to choose its window shapes).
	audioPostRoll = 4
	// resampleMargin is how many output samples the resampler runs before
	// (and after) the samples a chunk keeps: more than half its filter.
	resampleMargin = 256
	// decodePreRoll is how much source audio is read before a chunk's
	// first sample, for the source decoder's own overlap.
	decodePreRoll = 0.2
)

// AudioExact reports whether the source's audio timestamps name its
// samples exactly (a time base of 1/(k·rate)): chunked stream audio needs
// it.
func (in *TranscodeInput) AudioExact() bool {
	return in.Audio && in.SampleRate > 0 && in.AudioTBNum == 1 && in.AudioTBDen > 0 && in.AudioTBDen%in.SampleRate == 0
}

// audioChunkSegments is how many segments one audio transcode of a stream
// covers: the configured chunk in whole segments (at least one), or all of
// them when the audio cannot be chunked (see above) or chunks are off.
func audioChunkSegments(in *TranscodeInput, op *transform.VideoOp, segSeconds, segments int, chunk float64) int {
	if chunk <= 0 || op.Loudnorm != nil || !in.AudioExact() {
		return segments
	}
	return min(max(int(chunk)/segSeconds, 1), segments)
}

// AudioChunks is the number of audio transcodes of the stream.
func (l *StreamLayout) AudioChunks() int {
	if l.Audio == nil {
		return 0
	}
	k := l.Audio.ChunkSegments
	return (l.Segments + k - 1) / k
}

// AudioChunk is the chunk that holds audio segment n.
func (l *StreamLayout) AudioChunk(n int) int {
	if l.Audio == nil {
		return 0
	}
	return n / l.Audio.ChunkSegments
}

// AudioChunkSegments are the first segment of chunk c and the one after
// its last.
func (l *StreamLayout) AudioChunkSegments(c int) (first, end int) {
	k := l.Audio.ChunkSegments
	return c * k, min((c+1)*k, l.Segments)
}

// PlanAudioChunk plans chunk c of the audio of layout l (AAC-LC 48 kHz,
// fragmented on the segment grid): one fragment per segment of the chunk,
// the packets on the track's grid. With one chunk for the whole track
// (PlanAudioTrack's case) it is the whole stream encoded once.
func PlanAudioChunk(in *TranscodeInput, op *transform.VideoOp, l *StreamLayout, c int, s *TranscodeSettings) (*TranscodePlan, error) {
	a := l.Audio
	if a == nil {
		return nil, errs.New(errs.KindNotFound, "video: the stream has no audio")
	}
	if c < 0 || c >= l.AudioChunks() {
		return nil, errs.Newf(errs.KindNotFound, "video: audio chunk %d of %d", c, l.AudioChunks())
	}
	if !in.AudioExact() {
		return PlanAudioTrack(in, op, l, s) // one chunk (audioChunkSegments)
	}
	first, end := l.AudioChunkSegments(c)
	last := end == l.Segments
	out := l.Output
	out.Muxer = "mp4" // CMAF, also for MPEG-TS streams, whose segments copy it in
	frag := &FragmentPlan{
		Discont: true, Seconds: l.SegmentSeconds, Priming: a.Priming, Offset90k: l.Offset90k,
		First: first, End: end,
	}
	if last {
		frag.End, frag.Final = 0, l.Segments // the flush's packets are the track's last
	}

	// The track's grid and the resampler's phase, in samples.
	rate := in.SampleRate
	u := int64(in.AudioTBDen / rate) // source ticks per source sample
	g := gcd(rate, a.SampleRate)
	stepIn, stepOut := int64(rate/g), int64(a.SampleRate/g)
	origin := int64(math.Round(l.Start * float64(rate))) // the stream's first source sample
	ref, g0 := origin, int64(0)                          // the first sample of the track and its output index
	if start := int64(math.Round(in.AudioStart * float64(rate))); start > origin {
		ref = start
		g0 = roundDiv((start-origin)*int64(a.SampleRate), int64(rate))
	}
	segLen := int64(l.SegmentSeconds) * int64(a.SampleRate)
	// packet is the index (minus one) of the first packet at or after
	// output sample t on the grid.
	packet := func(t int64) int64 { return max(ceilDiv(max(t-g0, 0), aacFrame), 0) }

	// Output samples [s0, s1) are encoded, on the grid.
	s0 := g0
	if first > 0 {
		s0 = g0 + max(packet(int64(first)*segLen)-audioPreRoll, 0)*aacFrame
	}
	s1 := int64(math.Round(l.Duration * float64(a.SampleRate)))
	if !last {
		s1 = min(g0+(packet(int64(end)*segLen)+audioPostRoll)*aacFrame, s1)
	}
	// Source samples [i0, i1), on the resampler's phase grid from ref,
	// with a margin on both sides.
	k0 := floorDiv(s0-g0-resampleMargin, stepOut)
	k1 := ceilDiv(max(s1-g0+resampleMargin, 0), stepOut)
	i0, i1 := ref+k0*stepIn, ref+k1*stepIn

	p := &TranscodePlan{
		Output:   out,
		Start:    max(float64(i0)/float64(rate)-decodePreRoll, 0),
		End:      float64(i1) / float64(rate),
		Duration: float64(s1-s0) / float64(a.SampleRate),
		MaxBytes: s.MaxOutputBytes,
		Threads:  max(s.Threads, 1),
		Fragment: frag,
	}
	layout := "stereo"
	if a.Channels == 1 {
		layout = "mono"
	}
	var f filterChain
	// The chunk's source samples, then the stream's clock in source ticks
	// (integers: no rounding of a sample's time).
	f.add("atrim=start_pts="+i64toa(i0*u)+":end_pts="+i64toa(i1*u), "asetpts=PTS-"+i64toa(origin*u))
	if op.Volume > 0 && op.Volume != 1 {
		f.add("volume=" + ftoa(op.Volume))
	}
	if ln := op.Loudnorm; ln != nil { // one chunk: audioChunkSegments
		f.add("loudnorm=i=" + ftoa(ln.Integrated) + ":tp=" + ftoa(ln.TruePeak) + ":lra=" + ftoa(ln.Range))
	}
	if op.FadeIn > 0 {
		f.add("afade=t=in:st=0:d=" + ftoa(op.FadeIn.Seconds()))
	}
	if op.FadeOut > 0 {
		f.add("afade=t=out:st=" + ftoa(max(l.Duration-op.FadeOut.Seconds(), 0)) + ":d=" + ftoa(op.FadeOut.Seconds()))
	}
	// 48 kHz, then exactly the chunk's output samples (1/48000 ticks).
	f.add("aresample="+itoa(a.SampleRate), "atrim=start_pts="+i64toa(s0)+":end_pts="+i64toa(s1))
	f.add("aformat=sample_fmts=" + AudioAAC.sampleFormat() + ":sample_rates=" + itoa(a.SampleRate) + ":channel_layouts=" + layout)
	p.Audio = &AudioStreamPlan{Codec: AudioAAC, SampleRate: a.SampleRate, Channels: a.Channels, Bitrate: a.Bitrate, Filters: f.String()}
	return p, nil
}

func i64toa(n int64) string { return strconv.FormatInt(n, 10) }

// floorDiv is ⌊a/b⌋ for b > 0.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// roundDiv is a/b rounded half away from zero, as FFmpeg's ROUNDED_DIV
// (b > 0).
func roundDiv(a, b int64) int64 {
	if a >= 0 {
		return (a + b/2) / b
	}
	return (a - b/2) / b
}
