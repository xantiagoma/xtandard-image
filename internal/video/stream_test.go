package video

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// gplCaps is fullCaps with libx264 and libx265.
func gplCaps() TranscodeCaps {
	c := fullCaps()
	c.Video.H264, c.Video.HEVC = true, true
	return c
}

// long is a 1920×1080 29.97 fps clip of 61.5 s with stereo 44.1 kHz audio
// that starts at 0.1 s.
func long() *TranscodeInput {
	in := hd()
	in.Width, in.Height, in.FPS, in.Duration, in.StartTime = 1920, 1080, 30000.0/1001, 61.5, 0.1
	return in
}

func streamOf(t *testing.T, in *TranscodeInput, op *transform.VideoOp, caps TranscodeCaps, ss *StreamSettings) *StreamLayout {
	t.Helper()
	out, err := caps.ResolveStream(op)
	if err != nil {
		t.Fatal(err)
	}
	l, err := PlanStream(in, op, out, caps, settings(), ss)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// names lists the renditions as WxH@bitrate.
func names(l *StreamLayout) []string {
	s := make([]string, 0, len(l.Video))
	for _, r := range l.Video {
		s = append(s, itoa(r.Width)+"x"+itoa(r.Height)+"@"+itoa(int(r.Bitrate/1000))+"k "+r.Codecs)
	}
	return s
}

// TestPlanStreamLadder: the renditions are the ladder's (or the URL's)
// rungs within the stream's bounds, largest first, never above the
// source's size, with bandwidths in decreasing order.
func TestPlanStreamLadder(t *testing.T) {
	t.Parallel()
	portrait := hd()
	portrait.Rotation = 90 // shown 720×1280
	small := hd()
	small.Width, small.Height = 300, 200
	tests := []struct {
		name string
		in   *TranscodeInput
		st   transform.VideoStream
		caps TranscodeCaps
		ss   StreamSettings
		want []string
	}{
		{
			name: "default ladder, 720p source, gpl", in: hd(), caps: gplCaps(),
			want: []string{"1280x720@2800k avc1.64001f", "854x480@1400k avc1.64001e", "640x360@800k avc1.64001e", "426x240@400k avc1.64001e"},
		},
		{
			name: "default ladder, 1080p 29.97, full", in: long(), caps: fullCaps(),
			want: []string{
				"1920x1080@5000k av01.0.08M.08", "1280x720@2800k av01.0.05M.08", "854x480@1400k av01.0.04M.08",
				"640x360@800k av01.0.01M.08", "426x240@400k av01.0.00M.08",
			},
		},
		{
			name: "portrait keeps the short side", in: portrait, caps: gplCaps(), st: transform.VideoStream{Renditions: []int{360, 720}},
			want: []string{"720x1280@2800k avc1.64001f", "360x640@800k avc1.64001e"},
		},
		{
			name: "url renditions, unknown rung interpolated", in: hd(), caps: fullCaps(),
			st:   transform.VideoStream{Renditions: []int{240, 1080, 600}},
			want: []string{"1066x600@2130k av01.0.04M.08", "426x240@400k av01.0.00M.08"},
		},
		{
			name: "configured ladder and bit rates", in: hd(), caps: fullCaps(),
			ss:   StreamSettings{Ladder: []Rung{{Height: 360, Bitrate: 600_000}, {Height: 720}}},
			want: []string{"1280x720@2800k av01.0.05M.08", "640x360@600k av01.0.01M.08"},
		},
		{
			name: "profile bounds", in: long(), caps: fullCaps(), st: transform.VideoStream{MinHeight: 360, MaxHeight: 720, MaxRenditions: 2},
			want: []string{"1280x720@2800k av01.0.05M.08", "854x480@1400k av01.0.04M.08"},
		},
		{
			name: "source below every rung", in: small, caps: fullCaps(),
			want: []string{"300x200@400k av01.0.00M.08"},
		},
		{
			name: "vp9", in: hd(), caps: fullCaps(), st: transform.VideoStream{Renditions: []int{720, 360}},
			want: []string{"1280x720@2800k vp09.00.31.08", "640x360@800k vp09.00.21.08"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := tc.st
			op := &transform.VideoOp{Stream: &st}
			if strings.HasPrefix(tc.name, "vp9") {
				op.VideoCodec = transform.VideoCodecVP9
			}
			l := streamOf(t, tc.in, op, tc.caps, &tc.ss)
			if diff := cmp.Diff(tc.want, names(l)); diff != "" {
				t.Fatalf("renditions (-want +got):\n%s", diff)
			}
			for i := range l.Video {
				r := &l.Video[i]
				if r.Name != "v"+itoa(i) {
					t.Errorf("rendition %d is named %q", i, r.Name)
				}
				if i > 0 && l.Bandwidth(r) >= l.Bandwidth(&l.Video[i-1]) {
					t.Errorf("bandwidth of %s ≥ %s's", r.Name, l.Video[i-1].Name)
				}
			}
		})
	}
}

// TestPlanStreamTimeline: the segment grid, the frame rate as a fraction
// and the stream's offset come from the probe alone.
func TestPlanStreamTimeline(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		in               *TranscodeInput
		op               transform.VideoOp
		caps             TranscodeCaps
		segs, seconds    int
		duration, start  float64
		num, den         int
		offset           int64
		audio            bool
		lastSegment      float64
		channels, bitrat int64
	}{
		{
			name: "29.97 fps h264 with audio", in: long(), caps: gplCaps(), segs: 11, seconds: 6, duration: 61.5, start: 0.1,
			num: 30000, den: 1001, offset: 1920 + 6015, audio: true, lastSegment: 1.5,
		},
		{
			name: "trimmed, muted av1, 4 s", in: long(), caps: fullCaps(),
			op:   transform.VideoOp{Start: 10 * time.Second, Duration: 12 * time.Second, Mute: true, Stream: &transform.VideoStream{SegmentDuration: 4 * time.Second}},
			segs: 3, seconds: 4, duration: 12, start: 10.1, num: 30000, den: 1001, lastSegment: 4,
		},
		{
			name: "requested fps capped", in: hd(), caps: fullCaps(),
			op:   transform.VideoOp{FPS: 12.5, Stream: &transform.VideoStream{SegmentDuration: time.Second}},
			segs: 2, seconds: 1, duration: 2, num: 25, den: 2, offset: 1920, audio: true, lastSegment: 1,
		},
		{
			name: "a sliver at the end is no segment", in: func() *TranscodeInput { in := hd(); in.Duration = 2.01; return in }(), caps: fullCaps(),
			op:   transform.VideoOp{Stream: &transform.VideoStream{SegmentDuration: time.Second}},
			segs: 2, seconds: 1, duration: 2.01, num: 25, den: 1, offset: 1920, audio: true, lastSegment: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			op := tc.op
			if op.Stream == nil {
				op.Stream = &transform.VideoStream{}
			}
			l := streamOf(t, tc.in, &op, tc.caps, &StreamSettings{})
			r := l.Video[0]
			if l.Segments != tc.segs || l.SegmentSeconds != tc.seconds || l.Duration != tc.duration ||
				l.Start != tc.start || r.FPSNum != tc.num || r.FPSDen != tc.den || l.Offset90k != tc.offset ||
				(l.Audio != nil) != tc.audio || l.SegmentDuration(l.Segments-1) != tc.lastSegment {
				t.Fatalf("layout %+v, rendition %+v, last segment %v", l, r, l.SegmentDuration(l.Segments-1))
			}
			if l.Offset90k%15 != 0 {
				t.Fatalf("offset %d is not exact at 48 kHz", l.Offset90k)
			}
		})
	}
}

func TestPlanStreamErrors(t *testing.T) {
	t.Parallel()
	noVideo := hd()
	noVideo.Video = false
	unknown := hd()
	unknown.Duration = 0
	noDecoder := hd()
	noDecoder.AudioDecodable = false
	tests := []struct {
		name string
		in   *TranscodeInput
		op   transform.VideoOp
		caps TranscodeCaps
		kind errs.Kind
	}{
		{"no video", noVideo, transform.VideoOp{}, fullCaps(), errs.KindUnsupported},
		{"unknown duration", unknown, transform.VideoOp{}, fullCaps(), errs.KindUnsupported},
		{"audio without decoder", noDecoder, transform.VideoOp{}, fullCaps(), errs.KindUnsupported},
		{"start past the end", hd(), transform.VideoOp{Start: 3 * time.Second}, fullCaps(), errs.KindLimitExceeded},
		{"fps over the limit", hd(), transform.VideoOp{FPS: 120}, fullCaps(), errs.KindLimitExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			op := tc.op
			op.Stream = &transform.VideoStream{}
			out, err := tc.caps.ResolveStream(&op)
			if err == nil {
				_, err = PlanStream(tc.in, &op, out, tc.caps, settings(), &StreamSettings{})
			}
			if errs.KindOf(err) != tc.kind {
				t.Fatalf("err %v, want %s", err, tc.kind)
			}
		})
	}
	// What a build cannot write is refused before anything is fetched.
	for _, tc := range []struct {
		name string
		op   transform.VideoOp
		caps TranscodeCaps
	}{
		{"h264 on full", transform.VideoOp{VideoCodec: transform.VideoCodecH264}, fullCaps()},
		{"no aac", transform.VideoOp{}, func() TranscodeCaps { c := fullCaps(); c.AAC = false; return c }()},
		{"no mp4 muxer", transform.VideoOp{Mute: true}, func() TranscodeCaps { c := fullCaps(); c.MuxMP4 = false; return c }()},
	} {
		op := tc.op
		op.Stream = &transform.VideoStream{}
		if _, err := tc.caps.ResolveStream(&op); errs.KindOf(err) != errs.KindUnsupported {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	// MPEG-TS segments are H.264: the full image (no libx264) and a build
	// without the mpegts muxer refuse them.
	for _, tc := range []struct {
		name string
		op   transform.VideoOp
		caps TranscodeCaps
	}{
		{"ts on full", transform.VideoOp{}, fullCaps()},
		{"ts without the muxer", transform.VideoOp{}, func() TranscodeCaps { c := gplCaps(); c.MuxMPEGTS = false; return c }()},
		{"ts vp9", transform.VideoOp{VideoCodec: transform.VideoCodecVP9}, gplCaps()},
	} {
		op := tc.op
		op.Stream = &transform.VideoStream{Format: transform.SegmentTS}
		if _, err := tc.caps.ResolveStream(&op); errs.KindOf(err) != errs.KindUnsupported {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

// TestPlanSegment: a segment's chain cuts its window on the source's
// timeline, keeps the stream's clock, scales to the rendition and puts its
// frames on the stream's grid; its encoder has one keyframe per segment
// (or per KeyframeInterval) and the H.264 level of the manifest.
func TestPlanSegment(t *testing.T) {
	t.Parallel()
	op := &transform.VideoOp{Crop: &transform.VideoCrop{Width: 1440}, Stream: &transform.VideoStream{Renditions: []int{720}}}
	l := streamOf(t, long(), op, gplCaps(), &StreamSettings{})
	p, err := PlanSegment(long(), op, l, &l.Video[0], 3, settings())
	if err != nil {
		t.Fatal(err)
	}
	want := "trim=start=18.1:end=24.1,setpts=PTS-0.1/TB,crop=1440:1080:0:0,scale=960:720:flags=bicubic," +
		"fps=fps=30000/1001:start_time=18.018:trim=end=24,format=yuv420p"
	want = strings.Replace(want, ":trim", ",trim", 1)
	v := p.Video
	if v.Filters != want || v.Width != 960 || v.Height != 720 || v.GOP != 180 || v.Level != 31 || v.MaxBitrate != 2_800_000 {
		t.Fatalf("segment plan %+v\nwant chain %s", v, want)
	}
	if p.Start != 18.1 || p.End != 24.1 || p.Fragment == nil || !p.Fragment.Discont || p.Fragment.Offset90k != l.Offset90k || p.Audio != nil {
		t.Fatalf("plan %+v fragment %+v", p, p.Fragment)
	}
	last, err := PlanSegment(long(), op, l, &l.Video[0], l.Segments-1, settings())
	if err != nil || last.End != 61.6 {
		t.Fatalf("last segment %+v %v", last, err)
	}
	if _, err := PlanSegment(long(), op, l, &l.Video[0], l.Segments, settings()); errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("segment past the end: %v", err)
	}
	ki := *op
	ki.KeyframeInterval = 2 * time.Second
	p, err = PlanSegment(long(), &ki, l, &l.Video[0], 0, settings())
	if err != nil || p.Video.GOP != 60 {
		t.Fatalf("keyframe interval: %+v %v", p.Video, err)
	}
	a, err := PlanAudioTrack(long(), op, l, settings())
	if err != nil {
		t.Fatal(err)
	}
	if a.Audio == nil || a.Video != nil || a.Audio.SampleRate != 48000 || a.Audio.Codec != AudioAAC ||
		a.Fragment.Seconds != 6 || a.Fragment.Priming != 1024 || !strings.HasPrefix(a.Audio.Filters, "atrim=end=61.6,asetpts=PTS-0.1/TB,") {
		t.Fatalf("audio track %+v %+v", a.Audio, a.Fragment)
	}
}

// TestPlanSegmentTS: an MPEG-TS segment is H.264 in the mpegts muxer with
// the stream's offset and the audio to copy in; its cache key differs
// from the CMAF stream's, and its bandwidth counts the TS overhead.
func TestPlanSegmentTS(t *testing.T) {
	t.Parallel()
	op := &transform.VideoOp{Stream: &transform.VideoStream{Format: transform.SegmentTS, Renditions: []int{720, 360}}}
	l := streamOf(t, long(), op, gplCaps(), &StreamSettings{})
	if l.Format != transform.SegmentTS || l.Output.Muxer != "mpegts" || l.Output.Video != CodecH264 || l.Audio == nil {
		t.Fatalf("layout %+v", l)
	}
	cmaf := *op
	cmaf.Stream = &transform.VideoStream{Renditions: []int{720, 360}}
	lc := streamOf(t, long(), &cmaf, gplCaps(), &StreamSettings{})
	if l.Output.StreamKey() == lc.Output.StreamKey() || lc.Output.Muxer != "mp4" {
		t.Fatalf("stream keys %q and %q", l.Output.StreamKey(), lc.Output.StreamKey())
	}
	if b, bc := l.Bandwidth(&l.Video[0]), lc.Bandwidth(&lc.Video[0]); b != bc+bc/10 {
		t.Fatalf("ts bandwidth %d, cmaf %d", b, bc)
	}
	p, err := PlanSegment(long(), op, l, &l.Video[1], 2, settings())
	if err != nil {
		t.Fatal(err)
	}
	want := &TranscodePlan{
		Output: l.Output, Start: 12.1, End: 18.1, Duration: 61.5, MaxBytes: p.MaxBytes, Threads: 1,
		TS: &TSPlan{Offset90k: l.Offset90k, Audio: &CopyAudioPlan{SampleRate: 48000, Channels: 2}},
	}
	if p.Fragment != nil || p.Video == nil || p.Video.Codec != CodecH264 || p.Video.Level != l.Video[1].Level || p.Audio != nil {
		t.Fatalf("plan %+v video %+v", p, p.Video)
	}
	p.Video = nil
	if d := cmp.Diff(want, p); d != "" {
		t.Fatalf("plan (-want +got):\n%s", d)
	}
	muted := *op
	muted.Mute = true
	lm := streamOf(t, long(), &muted, gplCaps(), &StreamSettings{})
	if p, err := PlanSegment(long(), &muted, lm, &lm.Video[0], 0, settings()); err != nil || p.TS == nil || p.TS.Audio != nil {
		t.Fatalf("muted ts segment %+v %v", p, err)
	}
}

// TestStreamChainsAreNumeric: the stream chains keep the filter-name and

// number alphabet of every other chain (TestFilterChainsAreNumeric).
func TestStreamChainsAreNumeric(t *testing.T) {
	t.Parallel()
	const allowed = "abcdefghijklmnopqrstuvwxyz_0123456789=:,.-+/PTSB"
	op := &transform.VideoOp{
		Start: 1234567 * time.Microsecond, End: 59987654 * time.Microsecond, Rotate: 90, FlipV: true, FPS: 23.976,
		Crop: &transform.VideoCrop{X: 3, Y: 5, Width: 999, Height: 777}, FadeIn: time.Second, FadeOut: 333 * time.Millisecond,
		Volume: 0.5, Loudnorm: &transform.VideoLoudnorm{Integrated: -16, TruePeak: -1, Range: 9},
		Stream: &transform.VideoStream{SegmentDuration: 5 * time.Second},
	}
	l := streamOf(t, long(), op, gplCaps(), &StreamSettings{})
	chains := make([]string, 0, 3*len(l.Video)+1)
	for i := range l.Video {
		for _, n := range []int{0, l.Segments / 2, l.Segments - 1} {
			p, err := PlanSegment(long(), op, l, &l.Video[i], n, settings())
			if err != nil {
				t.Fatal(err)
			}
			chains = append(chains, p.Video.Filters)
		}
	}
	a, err := PlanAudioTrack(long(), op, l, settings())
	if err != nil {
		t.Fatal(err)
	}
	chains = append(chains, a.Audio.Filters)
	// Chunked audio (WP-131), with and without loudnorm (one chunk).
	for _, ln := range []*transform.VideoLoudnorm{nil, op.Loudnorm} {
		cop := *op
		cop.Loudnorm = ln
		lc := streamOf(t, exactLong(), &cop, gplCaps(), &StreamSettings{AudioChunk: 10 * time.Second})
		for c := range lc.AudioChunks() {
			p, err := PlanAudioChunk(exactLong(), &cop, lc, c, settings())
			if err != nil {
				t.Fatal(err)
			}
			chains = append(chains, p.Audio.Filters)
		}
	}
	for _, chain := range chains {
		if i := strings.IndexFunc(chain, func(r rune) bool { return !strings.ContainsRune(allowed, r) }); i >= 0 {
			t.Errorf("chain %q has %q", chain, chain[i])
		}
	}
}

func TestRational(t *testing.T) {
	t.Parallel()
	for fps, want := range map[float64][2]int{
		25: {25, 1}, 30000.0 / 1001: {30000, 1001}, 24000.0 / 1001: {24000, 1001}, 59.94: {60000, 1001},
		12.5: {25, 2}, 60: {60, 1}, 1: {1, 1}, 0.5: {1, 2},
	} {
		if n, d := rational(fps); n != want[0] || d != want[1] {
			t.Errorf("rational(%v) = %d/%d, want %d/%d", fps, n, d, want[0], want[1])
		}
	}
}
