//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// streamFixture is 6.6 s of 640x360 VP9 at 25 fps with a stereo tone in
// Opus, a keyframe every second (not on the 2 s segment grid).
const streamFixture = "stream-vp9-opus.webm"

// layout probes src and lays op out as a stream.
func layout(t *testing.T, src *vips.Buffer, op *transform.VideoOp) *video.StreamLayout {
	t.Helper()
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.ResolveStream(op)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := ffmpeg.OpenTranscode(t.Context(), src, video.Limits{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	in := tx.Input()
	l, err := video.PlanStream(&in, op, out, caps, txSettings(), &video.StreamSettings{})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// part transcodes one segment of rendition r (nil: the audio track).
func part(ctx context.Context, src *vips.Buffer, op *transform.VideoOp, l *video.StreamLayout, r *video.Rendition, n int) (*ffmpeg.Output, error) {
	tx, err := ffmpeg.OpenTranscode(ctx, src, video.Limits{}, 1)
	if err != nil {
		return nil, err
	}
	defer tx.Close()
	in := tx.Input()
	var p *video.TranscodePlan
	if r == nil {
		p, err = video.PlanAudioTrack(&in, op, l, txSettings())
	} else {
		p, err = video.PlanSegment(&in, op, l, r, n, txSettings())
	}
	if err != nil {
		return nil, err
	}
	return tx.Run(ctx, p)
}

// mustParse parses a fragmented MP4 output and releases it.
func mustParse(t *testing.T, o *ffmpeg.Output) *fmp4.File {
	t.Helper()
	b := bytes.Clone(o.Bytes())
	o.Release()
	f, err := fmp4.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestStreamSegments: every video segment of every rendition, encoded on
// its own, has the rendition's init segment and codec string, one
// fragment that starts with a keyframe where the previous one ended (no
// gap, no overlap), and the concatenation of init and segments decodes as
// one video with the source's frame count and duration on a constant
// frame grid.
func TestStreamSegments(t *testing.T) {
	t.Parallel()
	caps := ffmpeg.TranscodeCaps()
	codecs := []transform.VideoCodec{transform.VideoCodecAuto, transform.VideoCodecVP9}
	if caps.Video.H264 {
		codecs = append(codecs, transform.VideoCodecAV1)
	}
	for _, vc := range codecs {
		t.Run(vc.String(), func(t *testing.T) {
			t.Parallel()
			src := source(t, streamFixture)
			op := &transform.VideoOp{VideoCodec: vc, Stream: &transform.VideoStream{SegmentDuration: 2 * time.Second}}
			l := layout(t, src, op)
			off := int64(1920) // the AAC priming
			if l.Video[0].Codec == video.CodecH264 {
				off += 2 * 3600 // two frames of B-frame delay
			}
			if l.Segments != 4 || len(l.Video) != 2 || l.Audio == nil || l.Offset90k != off {
				t.Fatalf("layout %+v", l)
			}
			if r := l.Video; r[0].Width != 640 || r[0].Height != 360 || r[1].Width != 426 || r[1].Height != 240 {
				t.Fatalf("renditions %+v", r)
			}
			for i := range l.Video {
				r := &l.Video[i]
				t.Run(r.Name, func(t *testing.T) {
					t.Parallel()
					checkRendition(t, src, op, l, r)
				})
			}
		})
	}
}

func checkRendition(t *testing.T, src *vips.Buffer, op *transform.VideoOp, l *video.StreamLayout, r *video.Rendition) {
	var init []byte
	var all bytes.Buffer
	var next uint64
	frames := 0
	for n := range l.Segments {
		o, err := part(t.Context(), src, op, l, r, n)
		if err != nil {
			t.Fatalf("segment %d: %v", n, err)
		}
		f := mustParse(t, o)
		if len(f.Fragments) != 1 || f.Timescale != 90000 {
			t.Fatalf("segment %d: %d fragments, timescale %d", n, len(f.Fragments), f.Timescale)
		}
		fr := f.Fragments[0]
		if n == 0 {
			if fr.DecodeTime > uint64(l.Offset90k) {
				t.Fatalf("segment 0 decodes from %d, after the stream's offset %d", fr.DecodeTime, l.Offset90k)
			}
			next = fr.DecodeTime
			init = f.Init
			all.Write(init)
			codec, err := fmp4.CodecString(init)
			if err != nil || codec != r.Codecs {
				t.Fatalf("init codec %q (%v), manifest says %q", codec, err, r.Codecs)
			}
		} else if !bytes.Equal(f.Init, init) {
			t.Fatalf("segment %d has another init segment", n)
		}
		if fr.DecodeTime != next {
			t.Fatalf("segment %d starts at %d, the previous one ended at %d", n, fr.DecodeTime, next)
		}
		want := int(math.Round(l.SegmentDuration(n) * r.FPS()))
		if fr.Samples != want || fr.Duration != uint64(want)*90000*uint64(r.FPSDen)/uint64(r.FPSNum) {
			t.Fatalf("segment %d: %d frames lasting %d, want %d", n, fr.Samples, fr.Duration, want)
		}
		next = fr.DecodeTime + fr.Duration
		frames += fr.Samples
		all.Write(fr.Data)
	}
	if frames != 165 {
		t.Fatalf("%d frames, want 165", frames)
	}
	pk := packets(t, all.Bytes())
	if len(pk) != 165 {
		t.Fatalf("ffprobe reads %d packets, want 165", len(pk))
	}
	for i, p := range pk {
		want := float64(l.Offset90k)/90000 + float64(i)/25
		if math.Abs(p.pts-want) > 1e-4 {
			t.Fatalf("frame %d at %.5f s, want %.5f", i, p.pts, want)
		}
		if key := i%50 == 0; key && !p.key {
			t.Fatalf("frame %d (a segment's first) is not a keyframe", i)
		}
	}
}

// packet is one packet ffprobe reads, in presentation order.
type packet struct {
	pts float64
	key bool
}

// packets lists the packets of data in presentation order.
func packets(t *testing.T, data []byte) []packet {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	f := filepath.Join(t.TempDir(), "stream.mp4")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-show_entries", "packet=pts_time,flags",
		"-of", "json", f).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var out struct {
		Packets []struct {
			PTS   string `json:"pts_time"`
			Flags string `json:"flags"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	ps := make([]packet, 0, len(out.Packets))
	for _, p := range out.Packets {
		v, err := strconv.ParseFloat(p.PTS, 64)
		if err != nil {
			t.Fatalf("pts %q", p.PTS)
		}
		ps = append(ps, packet{pts: v, key: len(p.Flags) > 0 && p.Flags[0] == 'K'})
	}
	slices.SortFunc(ps, func(a, b packet) int { return cmp.Compare(a.pts, b.pts) })
	return ps
}

// TestStreamAudioTrack: the audio is encoded once and cut on the segment
// grid: one fragment per segment, each starting where the previous ended,
// segment k's first packet the first one at or after k·d of the source,
// and the whole track decodes to the stream's length.
func TestStreamAudioTrack(t *testing.T) {
	t.Parallel()
	src := source(t, streamFixture)
	op := &transform.VideoOp{Stream: &transform.VideoStream{SegmentDuration: 2 * time.Second}}
	l := layout(t, src, op)
	o, err := part(t.Context(), src, op, l, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := mustParse(t, o)
	if f.Timescale != 48000 || len(f.Fragments) != l.Segments {
		t.Fatalf("timescale %d, %d fragments, want 48000 and %d", f.Timescale, len(f.Fragments), l.Segments)
	}
	codec, err := fmp4.CodecString(f.Init)
	if err != nil || codec != l.Audio.Codecs {
		t.Fatalf("audio codec %q (%v), manifest says %q", codec, err, l.Audio.Codecs)
	}
	off := l.Offset90k * 48000 / 90000
	next := uint64(off - 1024) // the priming packet
	var all bytes.Buffer
	all.Write(f.Init)
	for k, fr := range f.Fragments {
		if fr.DecodeTime != next {
			t.Fatalf("fragment %d starts at %d, the previous one ended at %d", k, fr.DecodeTime, next)
		}
		// Every packet but the priming one is 1024 samples of the source
		// starting at DecodeTime - the offset.
		if src := int64(fr.DecodeTime) - off; k > 0 && (src < int64(k*2*48000) || src >= int64(k*2*48000+1024)) {
			t.Fatalf("fragment %d starts at source sample %d, want the first packet at or after %d", k, src, k*2*48000)
		}
		next = fr.DecodeTime + fr.Duration
		all.Write(fr.Data)
	}
	if got, want := float64(next-uint64(off))/48000, l.Duration; math.Abs(got-want) > 1024.0/48000 {
		t.Fatalf("audio of %.4f s, want %.4f", got, want)
	}
	p := ffprobe(t, all.Bytes())
	if len(p.Streams) != 1 || p.Streams[0].CodecName != "aac" {
		t.Fatalf("ffprobe %+v", p)
	}
}

// TestStreamSegmentWindow: a segment never transcodes much more than its
// window: the last segment of a long trim is as fast as the first, and a
// segment past the end is refused before anything runs.
func TestStreamSegmentLimits(t *testing.T) {
	t.Parallel()
	src := source(t, streamFixture)
	op := &transform.VideoOp{Stream: &transform.VideoStream{SegmentDuration: 2 * time.Second}}
	l := layout(t, src, op)
	if _, err := part(t.Context(), src, op, l, &l.Video[0], l.Segments); errs.KindOf(err) != errs.KindNotFound {
		t.Fatalf("segment past the end: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := part(ctx, src, op, l, &l.Video[0], 0); errs.KindOf(err) != errs.KindCanceled {
		t.Fatalf("canceled segment: %v", err)
	}
}
