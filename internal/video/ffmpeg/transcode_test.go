//go:build ffmpeg && cgo

package ffmpeg_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

func txSettings() *video.TranscodeSettings {
	return &video.TranscodeSettings{
		Threads:           1,
		MaxOutputPixels:   3840 * 2160,
		MaxOutputDuration: time.Minute,
		MaxFPS:            60,
		MaxOutputBytes:    64 << 20,
	}
}

// transcode opens src, plans op and runs it.
func transcode(ctx context.Context, src *vips.Buffer, op *transform.VideoOp, s *video.TranscodeSettings, lim video.Limits) (*ffmpeg.Output, *video.TranscodePlan, error) {
	caps := ffmpeg.TranscodeCaps()
	out, err := caps.Resolve(op)
	if err != nil {
		return nil, nil, err
	}
	t, err := ffmpeg.OpenTranscode(ctx, src, lim, s.Threads)
	if err != nil {
		return nil, nil, err
	}
	defer t.Close()
	in := t.Input()
	p, err := video.PlanTranscode(&in, op, out, caps, s)
	if err != nil {
		return nil, nil, err
	}
	o, err := t.Run(ctx, p)
	return o, p, err
}

// probe is ffprobe's view of a file.
type probe struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		NbFrames  string `json:"nb_read_frames"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
	} `json:"format"`
}

// ffprobe probes data with the image's ffprobe (counting the frames).
func ffprobe(t *testing.T, data []byte) probe {
	t.Helper()
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("no ffprobe")
	}
	f := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(f, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), bin, "-v", "error", "-count_frames", "-show_entries",
		"stream=codec_type,codec_name,width,height,nb_read_frames:format=format_name,duration", "-of", "json", f)
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var p probe
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTranscodeCaps(t *testing.T) {
	t.Parallel()
	c := ffmpeg.TranscodeCaps()
	if !c.VideoFilters || !c.Video.VP9 || !c.Video.AV1 || !c.MuxMP4 || !c.MuxWebM {
		t.Fatalf("full image caps %+v", c)
	}
	t.Logf("transcode caps: %v (audio filters %v)", c.Names(), c.AudioFilters)
}

// TestTranscode: trimmed and resized outputs have the duration, size,
// codecs and frame count ffprobe reads back.
func TestTranscode(t *testing.T) {
	t.Parallel()
	mp4 := "av1"
	if ffmpeg.TranscodeCaps().Video.H264 {
		mp4 = "h264"
	}
	tests := []struct {
		name     string
		file     string
		op       transform.VideoOp
		format   string
		codec    string
		w, h     int
		duration float64
		frames   int
	}{
		{
			name: "vp9 webm to mp4, trim and resize", file: "clip-vp9.webm",
			op:     transform.VideoOp{Start: 500 * time.Millisecond, Duration: time.Second, Width: 320},
			format: "mov,mp4,m4a,3gp,3g2,mj2", codec: mp4, w: 320, h: 180, duration: 1, frames: 25,
		},
		{
			name: "av1 mp4 to webm, end and cover", file: "clip-av1.mp4",
			op: transform.VideoOp{Start: 400 * time.Millisecond, End: 1200 * time.Millisecond, Width: 200, Height: 200,
				Fit: transform.VideoFitCover, Container: transform.VideoContainerWebM},
			format: "matroska,webm", codec: "vp9", w: 200, h: 200, duration: 0.8, frames: 20,
		},
		{
			name: "h264 to webm muted, fps", file: "bbb-h264-aac.mp4",
			op:     transform.VideoOp{Width: 480, FPS: 10, Mute: true, Container: transform.VideoContainerWebM},
			format: "matroska,webm", codec: "vp9", w: 480, h: 270, duration: 1, frames: 10,
		},
		{
			name: "display matrix applied", file: "faces-av1-rotate90.mp4",
			op:     transform.VideoOp{Container: transform.VideoContainerWebM, Height: 200},
			format: "matroska,webm", codec: "vp9", w: 254, h: 200, duration: 1, frames: 10,
		},
		{
			name: "rotate and pad", file: "clip-vp8.webm",
			op: transform.VideoOp{Rotate: 90, Width: 200, Height: 200, Fit: transform.VideoFitPad,
				Container: transform.VideoContainerWebM},
			format: "matroska,webm", codec: "vp9", w: 200, h: 200, duration: 1, frames: 10,
		},
		{
			name: "odd height kept even", file: "faces-vp9.webm",
			op:     transform.VideoOp{Container: transform.VideoContainerWebM, CRF: 50},
			format: "matroska,webm", codec: "vp9", w: 640, h: 502, duration: 1, frames: 10,
		},
		{
			name: "png mov to av1 mp4", file: "clip-png.mov",
			op:     transform.VideoOp{VideoCodec: transform.VideoCodecAV1},
			format: "mov,mp4,m4a,3gp,3g2,mj2", codec: "av1", w: 96, h: 54, duration: 1, frames: 10,
		},
		{
			name: "raw avi crop", file: "clip-raw.avi",
			op: transform.VideoOp{Crop: &transform.VideoCrop{X: 10, Y: 10, Width: 40, Height: 30},
				Container: transform.VideoContainerWebM},
			format: "matroska,webm", codec: "vp9", w: 40, h: 30, duration: 1, frames: 10,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, _, err := transcode(t.Context(), source(t, tc.file), &tc.op, txSettings(), video.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer out.Release()
			p := ffprobe(t, out.Bytes())
			if p.Format.FormatName != tc.format {
				t.Errorf("format %q, want %q", p.Format.FormatName, tc.format)
			}
			if len(p.Streams) != 1 {
				t.Fatalf("%d streams, want 1 video stream: %+v", len(p.Streams), p.Streams)
			}
			s := p.Streams[0]
			if s.CodecType != "video" || s.CodecName != tc.codec || s.Width != tc.w || s.Height != tc.h {
				t.Errorf("stream %s %s %dx%d, want video %s %dx%d", s.CodecType, s.CodecName, s.Width, s.Height, tc.codec, tc.w, tc.h)
			}
			if n, _ := strconv.Atoi(s.NbFrames); n != tc.frames {
				t.Errorf("%d frames, want %d", n, tc.frames)
			}
			d, _ := strconv.ParseFloat(p.Format.Duration, 64)
			if math.Abs(d-tc.duration) > 0.08 {
				t.Errorf("duration %.3f s, want %.3f s", d, tc.duration)
			}
		})
	}
}

// TestTranscodeAudio: with WP-120's audio (decoders, encoders, filters),
// the audio is transcoded with the video or alone.
func TestTranscodeAudio(t *testing.T) {
	t.Parallel()
	caps := ffmpeg.TranscodeCaps()
	src := source(t, "bbb-h264-aac.mp4")
	tr, err := ffmpeg.OpenTranscode(t.Context(), src, video.Limits{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	in := tr.Input()
	tr.Close()
	if !in.Audio || in.AudioCodec != "aac" || in.Channels != 1 || in.SampleRate != 16000 {
		t.Fatalf("input audio %+v", in)
	}
	if !in.AudioDecodable || !caps.AudioFilters {
		// The base before WP-120 has no audio: a request that keeps it is
		// refused, a muted one works (TestTranscode).
		_, _, err := transcode(t.Context(), src, &transform.VideoOp{}, txSettings(), video.Limits{})
		if errs.KindOf(err) != errs.KindUnsupported {
			t.Fatalf("audio without decoders: %v, want unsupported", err)
		}
		t.Skip("this image has no audio decoding or filters (WP-120)")
	}
	for _, tc := range []struct {
		name   string
		op     transform.VideoOp
		want   []string // codec names, video first
		format string
		ok     bool
	}{
		{"mp4", transform.VideoOp{Width: 320, Duration: 500 * time.Millisecond}, []string{"", "aac"}, "mov,mp4,m4a,3gp,3g2,mj2", caps.AAC},
		{"webm", transform.VideoOp{Width: 320, Container: transform.VideoContainerWebM}, []string{"vp9", "opus"}, "matroska,webm", caps.Opus},
		{"m4a", transform.VideoOp{Container: transform.VideoContainerM4A, Start: 200 * time.Millisecond}, []string{"aac"}, "mov,mp4,m4a,3gp,3g2,mj2", caps.AAC},
		{"opus", transform.VideoOp{Container: transform.VideoContainerOpus}, []string{"opus"}, "ogg", caps.Opus && caps.MuxOgg},
		{"mp3", transform.VideoOp{Container: transform.VideoContainerMP3}, []string{"mp3"}, "mp3", caps.MP3 && caps.MuxMP3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !tc.ok {
				t.Skip("encoder or muxer missing")
			}
			out, _, err := transcode(t.Context(), src, &tc.op, txSettings(), video.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer out.Release()
			p := ffprobe(t, out.Bytes())
			if p.Format.FormatName != tc.format || len(p.Streams) != len(tc.want) {
				t.Fatalf("%s with %+v, want %s with %v", p.Format.FormatName, p.Streams, tc.format, tc.want)
			}
			audio := p.Streams[len(p.Streams)-1]
			if audio.CodecType != "audio" || audio.CodecName != tc.want[len(tc.want)-1] {
				t.Errorf("audio stream %+v, want %s", audio, tc.want[len(tc.want)-1])
			}
		})
	}
}

// TestTranscodeCancel: a done context or the abort flag stops the work in
// C with the context's kind, and nothing leaks.
func TestTranscodeCancel(t *testing.T) {
	t.Parallel()
	src := source(t, "clip-vp9.webm")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := transcode(ctx, src, &transform.VideoOp{Mute: true}, txSettings(), video.Limits{}); errs.KindOf(err) != errs.KindCanceled {
		t.Fatalf("canceled context: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	if _, _, err := transcode(ctx, src, &transform.VideoOp{}, txSettings(), video.Limits{}); errs.KindOf(err) != errs.KindTimeout {
		t.Fatalf("deadline: %v", err)
	}
}

// TestTranscodeLimits: input bytes, duration and pixels, and the output's
// size, duration and pixels.
func TestTranscodeLimits(t *testing.T) {
	t.Parallel()
	src := source(t, "clip-vp9.webm")
	for _, tc := range []struct {
		name string
		op   transform.VideoOp
		set  func(*video.TranscodeSettings)
		lim  video.Limits
	}{
		{"input bytes", transform.VideoOp{}, nil, video.Limits{MaxBytes: 1000}},
		{"input duration", transform.VideoOp{}, nil, video.Limits{MaxDuration: time.Second}},
		{"input pixels", transform.VideoOp{}, nil, video.Limits{MaxPixels: 640 * 360}},
		{"output bytes", transform.VideoOp{}, func(s *video.TranscodeSettings) { s.MaxOutputBytes = 4096 }, video.Limits{}},
		{"output duration", transform.VideoOp{}, func(s *video.TranscodeSettings) { s.MaxOutputDuration = time.Second }, video.Limits{}},
		{"output pixels", transform.VideoOp{Width: 2560}, func(s *video.TranscodeSettings) { s.MaxOutputPixels = 1920 * 1080 }, video.Limits{}},
		{"start past the end", transform.VideoOp{Start: 3 * time.Second}, nil, video.Limits{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := txSettings()
			if tc.set != nil {
				tc.set(s)
			}
			op := tc.op
			op.Container = transform.VideoContainerWebM
			out, _, err := transcode(t.Context(), src, &op, s, tc.lim)
			if err == nil {
				out.Release()
			}
			if errs.KindOf(err) != errs.KindLimitExceeded {
				t.Fatalf("err %v, want limit_exceeded", err)
			}
		})
	}
}

// TestTranscodeNoLeak: no handle, output, libvips byte or glibc heap
// survives many transcodes, failed ones included.
func TestTranscodeNoLeak(t *testing.T) {
	src := source(t, "clip-vp8.webm")
	run := func(i int) {
		op := &transform.VideoOp{Width: 160, Container: transform.VideoContainerWebM, Duration: 300 * time.Millisecond}
		s := txSettings()
		if i%3 == 2 {
			s.MaxOutputBytes = 512 // fails in the muxer
		}
		out, _, err := transcode(t.Context(), src, op, s, video.Limits{})
		if err == nil {
			out.Release()
		} else if errs.KindOf(err) != errs.KindLimitExceeded {
			t.Fatal(err)
		}
	}
	for i := range 10 {
		run(i)
	}
	mem, heap := vips.ReadMemStats().Mem, ffmpeg.ReadStats().HeapInUse
	defer func() { t.Logf("glibc heap in use %d → %d bytes", heap, ffmpeg.ReadStats().HeapInUse) }()
	for i := range 100 {
		run(i)
	}
	if s := ffmpeg.ReadStats(); s.Transcoders != 0 || s.Outputs != 0 {
		t.Errorf("%d transcoders and %d outputs alive after 100 transcodes", s.Transcoders, s.Outputs)
	}
	if m := vips.ReadMemStats().Mem; m != mem {
		t.Errorf("libvips tracked memory %d → %d bytes", mem, m)
	}
	if h := ffmpeg.ReadStats().HeapInUse; heap > 0 && h-heap > 2<<20 {
		t.Errorf("glibc heap in use grew by %d bytes over 100 transcodes", h-heap)
	}
}

// BenchmarkTranscode720p measures one second of 720p output per
// iteration (ms/op is milliseconds per second of output), single-threaded.
func BenchmarkTranscode720p(b *testing.B) {
	src := source(b, "bbb-h264-aac.mp4")
	caps := ffmpeg.TranscodeCaps()
	for _, c := range []struct {
		name string
		op   transform.VideoOp
		ok   bool
	}{
		{"vp9", transform.VideoOp{Container: transform.VideoContainerWebM, Mute: true}, true},
		{"av1", transform.VideoOp{VideoCodec: transform.VideoCodecAV1, Mute: true}, true},
		{"h264", transform.VideoOp{VideoCodec: transform.VideoCodecH264, Mute: true}, caps.Video.H264},
		{"vp9-360p", transform.VideoOp{Container: transform.VideoContainerWebM, Mute: true, Width: 640}, true},
	} {
		b.Run(c.name, func(b *testing.B) {
			if !c.ok {
				b.Skip("encoder missing")
			}
			b.ReportAllocs()
			for b.Loop() {
				out, _, err := transcode(b.Context(), src, &c.op, txSettings(), video.Limits{})
				if err != nil {
					b.Fatal(err)
				}
				out.Release()
			}
		})
	}
}
