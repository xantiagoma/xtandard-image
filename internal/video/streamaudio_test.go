package video

import (
	"strings"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// exactLong is long() with sample-exact audio timestamps (MP4's 1/44100).
func exactLong() *TranscodeInput {
	in := long()
	in.AudioTBNum, in.AudioTBDen = 1, 44100
	return in
}

// TestAudioChunkLayout: a stream's audio is chunked in whole segments of
// the configured length when its timestamps are sample-exact, and encoded
// at once otherwise (Matroska's milliseconds), with loudnorm, or with
// chunks off.
func TestAudioChunkLayout(t *testing.T) {
	t.Parallel()
	ms := exactLong()
	ms.AudioTBDen = 1000
	for _, tc := range []struct {
		name   string
		in     *TranscodeInput
		op     transform.VideoOp
		chunk  time.Duration
		k, num int
	}{
		{"30 s of 6 s segments", exactLong(), transform.VideoOp{}, 30 * time.Second, 5, 3},
		{"chunk shorter than a segment", exactLong(), transform.VideoOp{}, 4 * time.Second, 1, 11},
		{"chunk longer than the stream", exactLong(), transform.VideoOp{}, time.Hour, 11, 1},
		{"chunks off", exactLong(), transform.VideoOp{}, 0, 11, 1},
		{"millisecond timestamps", ms, transform.VideoOp{}, 30 * time.Second, 11, 1},
		{"loudnorm", exactLong(), transform.VideoOp{Loudnorm: &transform.VideoLoudnorm{Integrated: -16, TruePeak: -1, Range: 9}}, 30 * time.Second, 11, 1},
	} {
		op := tc.op
		op.Stream = &transform.VideoStream{}
		l := streamOf(t, tc.in, &op, gplCaps(), &StreamSettings{AudioChunk: tc.chunk})
		if l.Audio.ChunkSegments != tc.k || l.AudioChunks() != tc.num || l.Segments != 11 {
			t.Errorf("%s: %d segments a chunk, %d chunks, want %d and %d", tc.name, l.Audio.ChunkSegments, l.AudioChunks(), tc.k, tc.num)
		}
	}
}

// TestPlanAudioChunk: a chunk's chain cuts its source samples on the
// resampler's phase grid (multiples of 147 source samples from the
// stream's first, 160 output samples at 44.1 → 48 kHz) with a margin,
// keeps the stream's clock in exact ticks, and keeps output samples from
// 16 packets before its first segment's first packet (on the 1024-sample
// grid) to 4 packets after its last; the shim keeps its segments only.
func TestPlanAudioChunk(t *testing.T) {
	t.Parallel()
	op := &transform.VideoOp{Stream: &transform.VideoStream{}}
	l := streamOf(t, exactLong(), op, gplCaps(), &StreamSettings{AudioChunk: 30 * time.Second})
	p, err := PlanAudioChunk(exactLong(), op, l, 1, settings())
	if err != nil {
		t.Fatal(err)
	}
	// Segments 5 to 9 (30 s to 60 s): the first packet at or after
	// 30 s × 48000 is 1407 (×1024), the pre-roll starts 16 before; the
	// end is 4 packets after the first one at or after 60 s (2813).
	want := "atrim=start_pts=1312710:end_pts=2654967,asetpts=PTS-4410,aresample=48000," +
		"atrim=start_pts=1424384:end_pts=2884608,aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo"
	if p.Audio.Filters != want {
		t.Fatalf("chain\n%s\nwant\n%s", p.Audio.Filters, want)
	}
	if f := p.Fragment; f.First != 5 || f.End != 10 || f.Final != 0 || !f.Discont || f.Seconds != 6 || p.Output.Muxer != "mp4" {
		t.Fatalf("fragment %+v", f)
	}
	if p.Start < 29.56 || p.Start > 29.57 || p.End < 60.2 || p.End > 60.21 || p.Video != nil {
		t.Fatalf("window %v–%v", p.Start, p.End)
	}
	// The cost is the chunk's, not the track's.
	whole := streamOf(t, exactLong(), op, gplCaps(), &StreamSettings{})
	track, err := PlanAudioChunk(exactLong(), op, whole, 0, settings())
	if err != nil {
		t.Fatal(err)
	}
	if c, w := EstimateCost(exactLong(), p), EstimateCost(exactLong(), track); c > w*0.6 || c < w*0.4 {
		t.Errorf("chunk of 30 s costs %.0f, the 61.4 s track %.0f", c, w)
	}
	// The last chunk runs to the stream's end and gives the final packet
	// to the last segment.
	last, err := PlanAudioChunk(exactLong(), op, l, 2, settings())
	if err != nil {
		t.Fatal(err)
	}
	if f := last.Fragment; f.First != 10 || f.End != 0 || f.Final != 11 || !strings.Contains(last.Audio.Filters, ":end_pts=2952000,") {
		t.Fatalf("last chunk %+v %s", f, last.Audio.Filters)
	}
	// Chunk 0 starts at the stream's start, its resampler a margin before.
	first, err := PlanAudioChunk(exactLong(), op, l, 0, settings())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.Audio.Filters, "atrim=start_pts=4116:") || !strings.Contains(first.Audio.Filters, "atrim=start_pts=0:") {
		t.Fatalf("first chunk %s", first.Audio.Filters)
	}
	for _, c := range []int{-1, 3} {
		if _, err := PlanAudioChunk(exactLong(), op, l, c, settings()); errs.KindOf(err) != errs.KindNotFound {
			t.Errorf("chunk %d: %v", c, err)
		}
	}
	// Without exact timestamps: the whole track, as PlanAudioTrack.
	ms := exactLong()
	ms.AudioTBDen = 1000
	lm := streamOf(t, ms, op, gplCaps(), &StreamSettings{AudioChunk: 30 * time.Second})
	pm, err := PlanAudioChunk(ms, op, lm, 0, settings())
	if err != nil || pm.Fragment.First != 0 || pm.Fragment.Final != lm.Segments || !strings.HasPrefix(pm.Audio.Filters, "atrim=end=61.6,") {
		t.Fatalf("whole track %+v %v", pm, err)
	}
}
