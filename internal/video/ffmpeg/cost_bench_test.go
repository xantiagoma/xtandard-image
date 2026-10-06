//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// BenchmarkTranscodeCost is the measurement behind video.EstimateCost
// (WP-121c): one whole transcode per iteration (open, probe, plan, run),
// single-threaded with the default encoder settings, over
//
//   - encode/<codec>/<height>p: the 1 s 720p30 H.264 Big Buck Bunny clip
//     to each encoder at four output sizes (the per-pixel encode factor
//     and each encoder's start-up are the slope and intercept);
//   - decode/<codec>: the same clip re-encoded by each encoder (and the
//     procedural VP9 and AV1 fixtures), decoded to a 128×72 VP9 output
//     (decode dominates);
//   - long/<codec>-<height>p: the 6.6 s 640×360 VP9 stream fixture, a
//     longer clip of another codec, to check the fit away from its data;
//   - audio/… and waveform: audio-only outputs and a waveform picture;
//   - segment/…: a 2 s HLS segment and a stream's audio track.
//
// Each sub-benchmark reports est-ms (the model's estimate in cost units,
// milliseconds) and est/measured; testdata/bench-transcode-cost.txt keeps
// a -count=10 run on the gpl builder.
func BenchmarkTranscodeCost(b *testing.B) {
	caps := ffmpeg.TranscodeCaps()
	type bench struct {
		name string
		file string
		op   *transform.VideoOp
		ok   bool
		// from re-encodes the Big Buck Bunny clip with this codec first
		// (Auto: file as it is), so every decoder reads the same content.
		from transform.VideoCodec
	}
	var cases []bench
	tiny := func() *transform.VideoOp {
		return &transform.VideoOp{Container: transform.VideoContainerWebM, Width: 128, Height: 72, Fit: transform.VideoFitFill, Mute: true}
	}
	for _, c := range []struct {
		name  string
		codec transform.VideoCodec
		ok    bool
	}{
		{"vp9", transform.VideoCodecVP9, true},
		{"av1", transform.VideoCodecAV1, true},
		{"h264", transform.VideoCodecH264, caps.Video.H264},
		{"hevc", transform.VideoCodecHEVC, caps.Video.HEVC},
	} {
		for _, h := range []int{180, 360, 720, 1080} {
			cases = append(cases, bench{
				"encode/" + c.name + "/" + strconv.Itoa(h) + "p", "bbb-h264-aac.mp4",
				&transform.VideoOp{VideoCodec: c.codec, Height: h, Mute: true}, c.ok, 0,
			})
		}
		for _, h := range []int{360, 720} {
			cases = append(cases, bench{
				"long/" + c.name + "-" + strconv.Itoa(h) + "p", streamFixture,
				&transform.VideoOp{VideoCodec: c.codec, Height: h, Mute: true}, c.ok, 0,
			})
		}
		cases = append(cases, bench{"decode/" + c.name, "bbb-h264-aac.mp4", tiny(), c.ok, c.codec})
	}
	cases = append(cases,
		bench{"decode/h264-source", "bbb-h264-aac.mp4", tiny(), true, 0},
		bench{"decode/vp9-procedural", "clip-vp9.webm", tiny(), true, 0},
		bench{"decode/av1-procedural", "clip-av1.mp4", tiny(), true, 0},
		bench{"audio/m4a-from-video", "bbb-h264-aac.mp4", &transform.VideoOp{Container: transform.VideoContainerM4A}, true, 0},
	)
	// Audio from the 8 s 48 kHz stereo Opus fixture, cut to 2 s and whole.
	for _, c := range []struct {
		name string
		c    transform.VideoContainer
		ok   bool
	}{
		{"m4a", transform.VideoContainerM4A, true},
		{"opus", transform.VideoContainerOpus, true},
		{"mp3", transform.VideoContainerMP3, caps.MP3},
		{"flac", transform.VideoContainerFLAC, caps.FLAC},
		{"wav", transform.VideoContainerWAV, caps.PCM},
		{"waveform", transform.VideoContainerPNG, caps.Waveform},
	} {
		for _, d := range []time.Duration{2 * time.Second, 0} {
			op := &transform.VideoOp{Container: c.c, Duration: d}
			if c.c == transform.VideoContainerPNG {
				op.Width, op.Height = 1200, 300
				op.Waveform = &transform.VideoWaveform{Color: transform.Color{R: 0x22, G: 0x66, B: 0xcc}}
			}
			name := "audio/" + c.name + "-8s"
			if d > 0 {
				name = "audio/" + c.name + "-2s"
			}
			cases = append(cases, bench{name, "../audio/pan-sweep-stereo.ogg", op, c.ok, 0})
		}
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			if !c.ok {
				b.Skip("encoder missing")
			}
			src := source(b, c.file)
			if c.from != transform.VideoCodecAuto {
				src = reencoded(b, src, c.from)
			}
			est := estimate(b, src, c.op)
			b.ReportAllocs()
			for b.Loop() {
				out, err := transcode(b.Context(), src, c.op, txSettings(), video.Limits{})
				if err != nil {
					b.Fatal(err)
				}
				out.Release()
			}
			reportEstimate(b, est)
		})
	}
	for _, c := range []struct {
		name  string
		codec transform.VideoCodec
		audio bool
		ok    bool
	}{
		{"segment/vp9-360p", transform.VideoCodecVP9, false, true},
		{"segment/av1-360p", transform.VideoCodecAV1, false, true},
		{"segment/h264-360p", transform.VideoCodecH264, false, caps.Video.H264},
		{"segment/aac-track", transform.VideoCodecAuto, true, true},
	} {
		b.Run(c.name, func(b *testing.B) {
			if !c.ok {
				b.Skip("encoder missing")
			}
			src := source(b, streamFixture)
			op := &transform.VideoOp{VideoCodec: c.codec, Stream: &transform.VideoStream{SegmentDuration: 2 * time.Second}}
			l := benchLayout(b, src, op)
			r := &l.Video[0]
			if c.audio {
				r = nil
			}
			est := estimatePart(b, src, op, l, r, 1)
			b.ReportAllocs()
			for b.Loop() {
				o, err := part(b.Context(), src, op, l, r, 1)
				if err != nil {
					b.Fatal(err)
				}
				o.Release()
			}
			reportEstimate(b, est)
		})
	}
}

// reportEstimate reports the model's estimate next to the measured time.
func reportEstimate(b *testing.B, est float64) {
	b.Helper()
	b.ReportMetric(est, "est-ms")
	if n := b.N; n > 0 {
		measured := float64(b.Elapsed().Nanoseconds()) / float64(n) / 1e6
		b.ReportMetric(est/measured, "est/measured")
	}
}

// estimate plans op over src as transcode does and returns its cost.
func estimate(b *testing.B, src *vips.Buffer, op *transform.VideoOp) float64 {
	b.Helper()
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.Resolve(op)
	if err != nil {
		b.Fatal(err)
	}
	t, err := ffmpeg.OpenTranscode(b.Context(), src, video.Limits{}, 1)
	if err != nil {
		b.Fatal(err)
	}
	defer t.Close()
	in := t.Input()
	p, err := video.PlanTranscode(&in, op, out, caps, txSettings())
	if err != nil {
		b.Fatal(err)
	}
	return video.EstimateCost(&in, p)
}

// estimatePart is estimate for a stream part (part's plan).
func estimatePart(b *testing.B, src *vips.Buffer, op *transform.VideoOp, l *video.StreamLayout, r *video.Rendition, n int) float64 {
	b.Helper()
	t, err := ffmpeg.OpenTranscode(b.Context(), src, video.Limits{}, 1)
	if err != nil {
		b.Fatal(err)
	}
	defer t.Close()
	in := t.Input()
	var p *video.TranscodePlan
	if r == nil {
		p, err = video.PlanAudioTrack(&in, op, l, txSettings())
	} else {
		p, err = video.PlanSegment(&in, op, l, r, n, txSettings())
	}
	if err != nil {
		b.Fatal(err)
	}
	return video.EstimateCost(&in, p)
}

// reencoded is src transcoded to codec at its size with the default
// settings, in C memory.
func reencoded(b *testing.B, src *vips.Buffer, codec transform.VideoCodec) *vips.Buffer {
	b.Helper()
	out, err := transcode(b.Context(), src, &transform.VideoOp{VideoCodec: codec, Mute: true}, txSettings(), video.Limits{})
	if err != nil {
		b.Fatal(err)
	}
	defer out.Release()
	buf, err := vips.NewBuffer(len(out.Bytes()))
	if err != nil {
		b.Fatal(err)
	}
	copy(buf.Bytes(), out.Bytes())
	b.Cleanup(buf.Release)
	return buf
}
