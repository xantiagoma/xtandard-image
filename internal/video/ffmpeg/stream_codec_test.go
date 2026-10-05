//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/media/fmp4"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// TestStreamCodecStrings: the codec string a manifest names before any
// segment exists is the one the encoder writes into the init segment's
// configuration record, for every codec of this build at the usual sizes
// and rates (H.264 is told its level; AV1 and VP9 derive theirs).
func TestStreamCodecStrings(t *testing.T) {
	t.Parallel()
	caps := ffmpeg.TranscodeCaps()
	codecs := []video.Codec{video.CodecAV1, video.CodecVP9}
	if caps.Video.H264 {
		codecs = append(codecs, video.CodecH264)
	}
	src := source(t, streamFixture)
	for _, codec := range codecs {
		for _, sz := range []struct{ w, h, num, den int }{
			{426, 240, 25, 1}, {1280, 720, 30000, 1001}, {1920, 1080, 30, 1}, {1920, 1080, 60, 1}, {720, 1280, 25, 1},
		} {
			name := codec.String() + "/" + strconv.Itoa(sz.w) + "x" + strconv.Itoa(sz.h) + "@" + strconv.Itoa(sz.num/sz.den)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				vc := map[video.Codec]transform.VideoCodec{
					video.CodecAV1: transform.VideoCodecAV1, video.CodecVP9: transform.VideoCodecVP9, video.CodecH264: transform.VideoCodecH264,
				}[codec]
				op := &transform.VideoOp{Mute: true, VideoCodec: vc, Stream: &transform.VideoStream{SegmentDuration: time.Second}}
				l := layout(t, src, op)
				r := video.Rendition{
					Name: "v0", Width: sz.w, Height: sz.h, FPSNum: sz.num, FPSDen: sz.den,
					Bitrate: video.RungBitrate(min(sz.w, sz.h)), Codec: codec,
				}
				r.Level, r.Codecs = video.CodecString(&r)
				l.Video = []video.Rendition{r}
				o, err := part(t.Context(), src, op, l, &l.Video[0], 1)
				if err != nil {
					t.Fatal(err)
				}
				f := mustParse(t, o)
				if got, err := fmp4.CodecString(f.Init); err != nil || got != r.Codecs {
					t.Fatalf("init segment says %q (%v), the manifest %q", got, err, r.Codecs)
				}
			})
		}
	}
}

// TestStreamNoLeak: no handle, output or libvips byte survives many
// segment and audio-track transcodes, cancelled ones included.
func TestStreamNoLeak(t *testing.T) {
	src := source(t, streamFixture)
	op := &transform.VideoOp{Stream: &transform.VideoStream{SegmentDuration: 2 * time.Second}}
	l := layout(t, src, op)
	run := func(i int) {
		ctx := t.Context()
		if i%7 == 6 {
			c, cancel := context.WithCancel(ctx)
			cancel()
			ctx = c
		}
		var r *video.Rendition
		if i%5 != 4 {
			r = &l.Video[i%len(l.Video)]
		}
		o, err := part(ctx, src, op, l, r, i%l.Segments)
		if err == nil {
			o.Release()
		} else if errs.KindOf(err) != errs.KindCanceled {
			t.Fatal(err)
		}
	}
	for i := range 5 {
		run(i)
	}
	mem := vips.ReadMemStats().Mem
	for i := range 40 {
		run(i)
	}
	if s := ffmpeg.ReadStats(); s.Transcoders != 0 || s.Outputs != 0 {
		t.Errorf("%d transcoders and %d outputs alive after 40 stream transcodes", s.Transcoders, s.Outputs)
	}
	if m := vips.ReadMemStats().Mem; m != mem {
		t.Errorf("libvips tracked memory %d → %d bytes", mem, m)
	}
}

// BenchmarkStreamSegment measures what a player waits for: one 2 s
// segment of the 360p rendition (the first segment's latency and any
// other's cost alike: each is its own transcode), and the audio track of
// the whole 6.6 s stream, which every audio segment comes from.
func BenchmarkStreamSegment(b *testing.B) {
	src := source(b, streamFixture)
	caps := ffmpeg.TranscodeCaps()
	for _, c := range []struct {
		name  string
		codec transform.VideoCodec
		audio bool
		ok    bool
	}{
		{"vp9-360p", transform.VideoCodecVP9, false, true},
		{"av1-360p", transform.VideoCodecAV1, false, true},
		{"h264-360p", transform.VideoCodecH264, false, caps.Video.H264},
		{"aac-track", transform.VideoCodecAuto, true, true},
	} {
		b.Run(c.name, func(b *testing.B) {
			if !c.ok {
				b.Skip("encoder missing")
			}
			op := &transform.VideoOp{VideoCodec: c.codec, Stream: &transform.VideoStream{SegmentDuration: 2 * time.Second}}
			l := benchLayout(b, src, op)
			r := &l.Video[0]
			if c.audio {
				r = nil
			}
			b.ReportAllocs()
			for b.Loop() {
				o, err := part(b.Context(), src, op, l, r, 1)
				if err != nil {
					b.Fatal(err)
				}
				o.Release()
			}
		})
	}
}

// benchLayout is layout for benchmarks.
func benchLayout(b *testing.B, src *vips.Buffer, op *transform.VideoOp) *video.StreamLayout {
	b.Helper()
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.ResolveStream(op)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := ffmpeg.OpenTranscode(b.Context(), src, video.Limits{}, 1)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Close()
	in := tx.Input()
	l, err := video.PlanStream(&in, op, out, caps, txSettings(), &video.StreamSettings{})
	if err != nil {
		b.Fatal(err)
	}
	return l
}
