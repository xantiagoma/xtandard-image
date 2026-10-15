//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/media/fmp4"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// tsSegment transcodes MPEG-TS segment n of rendition r with the audio of
// fragment n of the audio track a (nil: muted).
func tsSegment(t *testing.T, src *vips.Buffer, op *transform.VideoOp, l *video.StreamLayout, r *video.Rendition, n int, a *fmp4.File) []byte {
	t.Helper()
	tx, err := ffmpeg.OpenTranscode(t.Context(), src, video.Limits{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	in := tx.Input()
	p, err := video.PlanSegment(&in, op, l, r, n, txSettings())
	if err != nil {
		t.Fatal(err)
	}
	if p.TS == nil || (p.TS.Audio != nil) != (a != nil) {
		t.Fatalf("plan %+v", p.TS)
	}
	if a != nil {
		samples, err := a.Samples(n)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range samples {
			p.TS.Audio.Packets = append(p.TS.Audio.Packets, video.AudioPacket{Data: s.Data, PTS: int64(s.DecodeTime), Duration: int64(s.Duration)}) //nolint:gosec // G115: 48 kHz ticks
		}
	}
	o, err := tx.Run(t.Context(), p)
	if err != nil {
		t.Fatalf("segment %d: %v", n, err)
	}
	defer o.Release()
	return bytes.Clone(o.Bytes())
}

// tsPacket is one packet of a transport stream as ffprobe reads it.
type tsPacket struct {
	audio    bool
	pts, dur float64
	key      bool
}

// tsPackets lists the packets of a transport stream in file order.
func tsPackets(t *testing.T, data []byte) []tsPacket {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	f := filepath.Join(t.TempDir(), "stream.ts")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := exec.CommandContext(t.Context(), bin, "-v", "error", "-show_entries",
		"packet=codec_type,pts_time,duration_time,flags", "-of", "json", f).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var out struct {
		Packets []struct {
			Type  string `json:"codec_type"`
			PTS   string `json:"pts_time"`
			Dur   string `json:"duration_time"`
			Flags string `json:"flags"`
		} `json:"packets"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	ps := make([]tsPacket, 0, len(out.Packets))
	for _, p := range out.Packets {
		pts, err := strconv.ParseFloat(p.PTS, 64)
		if err != nil {
			t.Fatalf("pts %q", p.PTS)
		}
		dur, _ := strconv.ParseFloat(p.Dur, 64)
		ps = append(ps, tsPacket{audio: p.Type == "audio", pts: pts, dur: dur, key: len(p.Flags) > 0 && p.Flags[0] == 'K'})
	}
	return ps
}

// checkTSContinuity walks the 188-byte packets of the joined segments:
// every segment has its PAT and PMT before its first elementary stream
// packet, and a continuity counter that does not follow on from the
// previous packet of its PID carries the discontinuity indicator (each
// segment is muxed on its own).
func checkTSContinuity(t *testing.T, segments [][]byte) {
	t.Helper()
	const pmtPID = 0x1000 // FFmpeg's first PMT PID
	last := map[int]int{}
	for n, seg := range segments {
		if len(seg)%188 != 0 || len(seg) == 0 {
			t.Fatalf("segment %d: %d bytes, not whole TS packets", n, len(seg))
		}
		tables := map[int]bool{}
		for off := 0; off < len(seg); off += 188 {
			p := seg[off : off+188]
			if p[0] != 0x47 {
				t.Fatalf("segment %d: no sync byte at %d", n, off)
			}
			pid := int(p[1]&0x1f)<<8 | int(p[2])
			if pid == 0 || pid == pmtPID {
				tables[pid] = true
			} else if pid >= 0x100 && pid < pmtPID && (!tables[0] || !tables[pmtPID]) {
				t.Fatalf("segment %d: PID %d before its PAT and PMT", n, pid)
			}

			afc, cc := p[3]>>4&3, int(p[3]&0xf)
			if afc&1 == 0 || pid == 0x1fff {
				continue // no payload: the counter does not move
			}
			disc := afc&2 != 0 && p[4] > 0 && p[5]&0x80 != 0
			if prev, ok := last[pid]; ok && cc != (prev+1)&0xf && !disc {
				t.Fatalf("segment %d, PID %d: continuity counter %d after %d without a discontinuity indicator", n, pid, cc, prev)
			}
			last[pid] = cc
		}
	}
}

// TestStreamTS: MPEG-TS segments (WP-131), each muxed on its own with the
// audio of its span copied from the audio track, join into one transport
// stream: the source's frame count on the stream's frame grid with a
// keyframe first in every segment, audio packets that follow each other
// without a gap or an overlap over the stream's length, the first video
// frame with the source's first sample (A/V sync), and continuity
// counters that only jump where the discontinuity indicator says so.
func TestStreamTS(t *testing.T) {
	t.Parallel()
	caps := ffmpeg.TranscodeCaps()
	if !caps.Video.H264 || !caps.MuxMPEGTS {
		t.Skip("mpeg-ts segments need libx264 (gpl)")
	}
	for _, muted := range []bool{false, true} {
		t.Run("muted="+strconv.FormatBool(muted), func(t *testing.T) {
			t.Parallel()
			src := source(t, streamFixture)
			op := &transform.VideoOp{Mute: muted, Stream: &transform.VideoStream{Format: transform.SegmentTS, SegmentDuration: 2 * time.Second}}
			l := layout(t, src, op)
			if l.Segments != 4 || l.Output.Muxer != "mpegts" || (l.Audio == nil) != muted {
				t.Fatalf("layout %+v", l)
			}
			var track *fmp4.File
			if !muted {
				o, err := part(t.Context(), src, op, l, nil, 0)
				if err != nil {
					t.Fatal(err)
				}
				track = mustParse(t, o)
			}
			for i := range l.Video {
				r := &l.Video[i]
				t.Run(r.Name, func(t *testing.T) {
					t.Parallel()
					segs := make([][]byte, l.Segments)
					for n := range l.Segments {
						segs[n] = tsSegment(t, src, op, l, r, n, track)
						if ps := tsPackets(t, segs[n]); len(ps) == 0 || !slices.ContainsFunc(ps, func(p tsPacket) bool { return !p.audio && p.key }) {
							t.Fatalf("segment %d has no keyframe", n)
						}
					}
					checkTSContinuity(t, segs)
					checkTSTimeline(t, l, bytes.Join(segs, nil), muted)
					decodeClean(t, bytes.Join(segs, nil), "ts")
				})
			}
		})
	}
}

// decodeClean decodes data (a file of extension ext) with the ffmpeg
// program and fails on anything it reports at the warning level or above
// (corrupt packets, timestamp jumps, continuity errors).
func decodeClean(t *testing.T, data []byte, ext string) {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	f := filepath.Join(t.TempDir(), "joined."+ext)
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), bin, "-nostdin", "-v", "warning", "-i", f, "-f", "null", "-").CombinedOutput()
	if err != nil || len(bytes.TrimSpace(out)) > 0 {
		t.Fatalf("ffmpeg decode: %v\n%s", err, out)
	}
}

// checkTSTimeline checks the joined segments' packets (see TestStreamTS).

func checkTSTimeline(t *testing.T, l *video.StreamLayout, joined []byte, muted bool) {
	t.Helper()
	off := float64(l.Offset90k) / 90000
	var vpts []float64
	var audio []tsPacket
	for _, p := range tsPackets(t, joined) {
		if p.audio {
			audio = append(audio, p)
			continue
		}
		vpts = append(vpts, p.pts)
	}
	slices.Sort(vpts)
	if len(vpts) != 165 {
		t.Fatalf("%d video frames, want 165", len(vpts))
	}
	for i, pts := range vpts {
		if want := off + float64(i)/25; math.Abs(pts-want) > 1.5e-5 {
			t.Fatalf("frame %d at %.5f s, want %.5f", i, pts, want)
		}
	}
	if muted {
		if len(audio) != 0 {
			t.Fatalf("%d audio packets in a muted stream", len(audio))
		}
		return
	}
	// The priming packet plays before the stream's start; the next one,
	// the source's first sample, with the first video frame.
	if len(audio) < 2 || math.Abs(audio[1].pts-off) > 1.5e-5 || math.Abs(audio[0].pts-(off-1024.0/48000)) > 1.5e-5 {
		t.Fatalf("audio starts at %v, the first frame at %.5f", audio[:min(2, len(audio))], off)
	}
	for i := 1; i < len(audio); i++ {
		if gap := audio[i].pts - (audio[i-1].pts + 1024.0/48000); math.Abs(gap) > 1.5e-5 {
			t.Fatalf("audio packet %d at %.6f s: %.6f s after the previous one ended: %+v", i, audio[i].pts, gap, audio[:min(len(audio), i+3)])
		}
	}
	end := audio[len(audio)-1].pts + 1024.0/48000 - off
	if end < l.Duration-1e-3 || end > l.Duration+1024.0/48000+1e-3 {
		t.Fatalf("audio ends at %.4f s of the stream, want %.4f", end, l.Duration)
	}
}
