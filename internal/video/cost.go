package video

import "math"

// Cost model of video transformations (WP-121c, ADR 0012 §4): how much
// work a planned transcode is, so internal/media admits it against a
// weighted capacity instead of one worker slot per job.
//
// The unit is one millisecond of one core on the reference build: the gpl
// base builder (linux/arm64 under Docker on an Apple M-series host), one
// codec thread, the default encoder settings of XI_VIDEO_ENCODE_* (libvpx
// and libaom speed 8, x264 and x265 preset veryfast). The factors are
// fitted to the medians of BenchmarkTranscodeCost (internal/video/ffmpeg,
// -count=10: testdata/bench-transcode-cost-fit.txt is the run they were
// fitted to, bench-transcode-cost.txt a run of this model reporting
// est-ms and est/measured):
//
//	cost = base
//	     + decoded seconds × source fps × source pixels × decode(source codec)
//	     + output seconds × output fps × output pixels × encode(output codec)
//	     + audio base(codec) + output seconds × audio(codec) × channels/2 × rate/48 kHz
//
// The decoded seconds are the cut's, plus the keyframe pre-roll of a cut
// that does not start at the beginning and the second of packets read
// past a cut's end. Audio-only outputs and waveforms never decode the
// video. On real footage (Big Buck Bunny, 180p to 1080p outputs) the
// estimate is 0.78–0.96 of the measured time for every encoder; on the
// procedural fixtures, which are much cheaper to decode and encode, it is
// up to 1.93 of it (2 s HLS segments); audio within 0.85–1.15. (The
// check run was taken on a busier host: 0.52–1.75, every point within 2×
// but x265 at 360p, 0.43, which a quiet re-run measured at 0.79–0.89.) Only the
// relative weights matter for admission: the absolute scale makes the
// capacity and the cost metrics read as milliseconds of work.
//
// Measured, the encoders rank HEVC (x265) > AV1 (libaom) > H.264 (x264)
// ≈ VP9 (libvpx) at their default settings: per pixel of a frame, x265
// veryfast costs 4.7× libvpx at speed 8. Non-default speeds and presets
// are not modelled.

// encodeNsPerPixel is the cost of scaling, converting and encoding one
// output frame, per pixel, at the codec's default settings.
var encodeNsPerPixel = map[Codec]float64{
	CodecVP9:  7.5,
	CodecAV1:  21,
	CodecH264: 8,
	CodecHEVC: 35,
}

// decodeNsPerPixel is the cost of decoding one source frame, per pixel,
// by FFmpeg's codec name (measured on the same footage re-encoded by each
// encoder); a codec not listed costs decodeDefault.
var decodeNsPerPixel = map[string]float64{
	"h264": 2.0,
	"hevc": 2.2,
	"av1":  2.4,
	"vp9":  2.0,
	"vp8":  2.0,
}

// audioMsPerSecond is the cost of one second of stereo 48 kHz audio
// (decoding the source's, resampling and encoding); audioBaseMs the
// encoder's start-up (FFmpeg's AAC encoder builds its tables).
var (
	audioMsPerSecond = map[AudioCodec]float64{
		AudioAAC:  20.7,
		AudioOpus: 7.0,
		AudioMP3:  7.9,
		AudioFLAC: 2.2,
		AudioPCM:  1.0,
	}
	audioBaseMs = map[AudioCodec]float64{AudioAAC: 18}
)

const (
	// baseMs is the fixed part of every transcode: opening the demuxer,
	// the filter graphs, the encoders and the muxer.
	baseMs = 2.0
	// decodeDefault is an unlisted codec (intra-only and older ones are
	// heavier per pixel than the measured inter codecs).
	decodeDefault      = 3.0
	waveformMsPerSec   = 1.1 // decode one second of audio into showwavespic
	waveformMsPerMpx   = 2.0 // draw and encode the picture, per megapixel
	assumedFPS         = 30.0
	assumedDurationSec = 60.0 // a cut of unknown length and no limit
	// prerollSec is the source decoded before a cut's start: the seek
	// lands on the keyframe before it.
	prerollSec = 1.0
	// segmentTailSec is what a cut reads past its end (XI_TX_END_SLACK in
	// the shim): the decoder and B-frame reordering may hold frames.
	segmentTailSec = 1.0
)

// EstimateCost is the work of plan p over the probed source in, in cost
// units (milliseconds of one core on the reference build, see above). It
// is pure, finite and at least 1.
func EstimateCost(in *TranscodeInput, p *TranscodePlan) float64 {
	window := p.Duration
	if p.End > 0 {
		window = p.End - p.Start
	}
	if window <= 0 || math.IsNaN(window) {
		window = assumedDurationSec
	}
	ms := baseMs
	switch {
	case p.Waveform != nil:
		px := float64(p.Waveform.Width) * float64(p.Waveform.Height)
		ms += window*waveformMsPerSec + px/1e6*waveformMsPerMpx
	case p.Video != nil:
		v := p.Video
		decoded := window
		if p.Start > in.StartTime+1e-3 {
			decoded += prerollSec
		}
		if p.End > 0 {
			decoded += segmentTailSec
		}
		fps := in.FPS
		if fps <= 0 {
			fps = assumedFPS
		}
		dec, ok := decodeNsPerPixel[in.VideoCodec]
		if !ok {
			dec = decodeDefault
		}
		ms += decoded * fps * float64(in.Width) * float64(in.Height) * dec / 1e6
		outFPS := v.FPS
		if outFPS <= 0 {
			outFPS = fps
		}
		ms += window * outFPS * float64(v.Width) * float64(v.Height) * encodeNsPerPixel[v.Codec] / 1e6
	}
	if a := p.Audio; a != nil {
		ch, rate := float64(max(a.Channels, 1)), float64(a.SampleRate)
		if rate <= 0 {
			rate = 48000
		}
		ms += audioBaseMs[a.Codec] + window*audioMsPerSecond[a.Codec]*ch/2*rate/48000
	}
	if math.IsNaN(ms) || ms > math.MaxInt32 {
		return math.MaxInt32
	}
	return max(ms, 1)
}
