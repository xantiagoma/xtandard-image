package video

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// fullCaps is the full image after WP-120: every LGPL encoder and muxer.
func fullCaps() TranscodeCaps {
	return TranscodeCaps{
		Video: EncoderSet{VP9: true, AV1: true},
		AAC:   true, Opus: true, MP3: true,
		MuxMP4: true, MuxWebM: true, MuxOgg: true, MuxMP3: true,
		VideoFilters: true, AudioFilters: true,
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	gpl := fullCaps()
	gpl.Video.H264, gpl.Video.HEVC = true, true
	noAudio := fullCaps()
	noAudio.AAC, noAudio.Opus, noAudio.MP3, noAudio.MuxOgg, noAudio.MuxMP3, noAudio.AudioFilters = false, false, false, false, false, false
	tests := []struct {
		name string
		caps TranscodeCaps
		op   transform.VideoOp
		want string // TranscodeOutput.Key(), or the error kind
		ct   string
	}{
		{"mp4 full is av1", fullCaps(), transform.VideoOp{}, "mp4:av1:aac", "video/mp4"},
		{"mp4 gpl is h264", gpl, transform.VideoOp{}, "mp4:h264:aac", "video/mp4"},
		{"mp4 hevc gpl", gpl, transform.VideoOp{VideoCodec: transform.VideoCodecHEVC}, "mp4:hevc:aac", "video/mp4"},
		{"mp4 hevc full", fullCaps(), transform.VideoOp{VideoCodec: transform.VideoCodecHEVC}, "unsupported", ""},
		{"mp4 h264 full", fullCaps(), transform.VideoOp{VideoCodec: transform.VideoCodecH264}, "unsupported", ""},
		{"mp4 muted", fullCaps(), transform.VideoOp{Mute: true}, "mp4:av1:", "video/mp4"},
		{"webm", fullCaps(), transform.VideoOp{Container: transform.VideoContainerWebM}, "webm:vp9:opus", "video/webm"},
		{"webm av1", fullCaps(), transform.VideoOp{Container: transform.VideoContainerWebM, VideoCodec: transform.VideoCodecAV1}, "webm:av1:opus", "video/webm"},
		{"m4a", fullCaps(), transform.VideoOp{Container: transform.VideoContainerM4A}, "m4a::aac", "audio/mp4"},
		{"opus", fullCaps(), transform.VideoOp{Container: transform.VideoContainerOpus}, "opus::opus", "audio/ogg"},
		{"mp3", fullCaps(), transform.VideoOp{Container: transform.VideoContainerMP3}, "mp3::mp3", "audio/mpeg"},
		{"m4a without audio", noAudio, transform.VideoOp{Container: transform.VideoContainerM4A}, "unsupported", ""},
		{"mp3 without muxer", noAudio, transform.VideoOp{Container: transform.VideoContainerMP3}, "unsupported", ""},
		{"webm without audio encoders", noAudio, transform.VideoOp{Container: transform.VideoContainerWebM}, "webm:vp9:opus", "video/webm"},
		{"slim", TranscodeCaps{}, transform.VideoOp{}, "unsupported", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := tc.caps.Resolve(&tc.op)
			if err != nil {
				if got := errs.KindOf(err).String(); got != tc.want {
					t.Fatalf("error %v (%s), want %s", err, got, tc.want)
				}
				return
			}
			if out.Key() != tc.want || out.ContentType != tc.ct {
				t.Fatalf("Resolve = %s %s, want %s %s", out.Key(), out.ContentType, tc.want, tc.ct)
			}
		})
	}
}

// hd is a 1280×720 25 fps clip of 2 s with stereo 44.1 kHz audio.
func hd() *TranscodeInput {
	return &TranscodeInput{
		Container: "mov", Duration: 2,
		Video: true, VideoCodec: "h264", Width: 1280, Height: 720, FPS: 25,
		Audio: true, AudioCodec: "aac", AudioDecodable: true, SampleRate: 44100, Channels: 2,
	}
}

func settings() *TranscodeSettings {
	return &TranscodeSettings{
		Encode:            EncodeSettings{CRF: map[Codec]int{CodecAV1: 36, CodecVP9: 36}},
		Threads:           1,
		MaxOutputPixels:   3840 * 2160,
		MaxOutputDuration: time.Minute,
		MaxFPS:            60,
		MaxOutputBytes:    1 << 30,
	}
}

func TestPlanTranscodeVideo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		in     func(in *TranscodeInput)
		op     transform.VideoOp
		w, h   int
		fps    float64
		chain  string
		start  float64
		end    float64
		length float64
	}{
		{
			name: "whole", w: 1280, h: 720, fps: 25, length: 2,
			chain: "format=yuv420p",
		},
		{
			name: "trim and width", op: transform.VideoOp{Start: 500 * time.Millisecond, Duration: time.Second, Width: 640},
			w: 640, h: 360, fps: 25, start: 0.5, end: 1.5, length: 1,
			chain: "trim=start=0.5:end=1.5,setpts=PTS-0.5/TB,scale=640:360:flags=bicubic,format=yuv420p",
		},
		{
			name: "end past the source is the source's end", op: transform.VideoOp{Start: time.Second, End: 10 * time.Second},
			w: 1280, h: 720, fps: 25, start: 1, end: 2, length: 1,
			chain: "trim=start=1:end=2,setpts=PTS-1/TB,format=yuv420p",
		},
		{
			name: "height only, odd", op: transform.VideoOp{Height: 101},
			w: 178, h: 100, fps: 25, length: 2,
			chain: "scale=178:100:flags=bicubic,format=yuv420p",
		},
		{
			name: "scale-down never enlarges", op: transform.VideoOp{Width: 4000, Fit: transform.VideoFitScaleDown},
			w: 1280, h: 720, fps: 25, length: 2,
			chain: "format=yuv420p",
		},
		{
			name: "contain enlarges", op: transform.VideoOp{Width: 1920, Height: 1920},
			w: 1920, h: 1080, fps: 25, length: 2,
			chain: "scale=1920:1080:flags=bicubic,format=yuv420p",
		},
		{
			name: "cover", op: transform.VideoOp{Width: 400, Height: 400, Fit: transform.VideoFitCover},
			w: 400, h: 400, fps: 25, length: 2,
			chain: "scale=712:400:flags=bicubic,crop=400:400:156:0,format=yuv420p",
		},
		{
			name: "pad", op: transform.VideoOp{Width: 400, Height: 400, Fit: transform.VideoFitPad, Background: transform.Color{R: 255, B: 16}},
			w: 400, h: 400, fps: 25, length: 2,
			chain: "scale=400:224:flags=bicubic,pad=400:400:0:88:color=0xff0010,format=yuv420p",
		},
		{
			name: "fill", op: transform.VideoOp{Width: 300, Height: 301, Fit: transform.VideoFitFill},
			w: 300, h: 300, fps: 25, length: 2,
			chain: "scale=300:300:flags=bicubic,format=yuv420p",
		},
		{
			name: "rotate and crop in the rotated frame", op: transform.VideoOp{Rotate: 90, Crop: &transform.VideoCrop{X: 100, Y: 200, Width: 1000, Height: 333}},
			w: 620, h: 332, fps: 25, length: 2,
			chain: "transpose=clock,crop=620:332:100:200,format=yuv420p",
		},
		{
			name: "display matrix is applied", in: func(in *TranscodeInput) { in.Rotation = 90 },
			w: 720, h: 1280, fps: 25, length: 2,
			chain: "transpose=clock,format=yuv420p",
		},
		{
			name: "display matrix undone by the request", in: func(in *TranscodeInput) { in.Rotation = 90 }, op: transform.VideoOp{Rotate: 270},
			w: 1280, h: 720, fps: 25, length: 2,
			chain: "format=yuv420p",
		},
		{
			name: "mirrored source flipped back", in: func(in *TranscodeInput) { in.HFlip = true }, op: transform.VideoOp{FlipH: true},
			w: 1280, h: 720, fps: 25, length: 2,
			chain: "format=yuv420p",
		},
		{
			name: "flip v", op: transform.VideoOp{FlipV: true},
			w: 1280, h: 720, fps: 25, length: 2,
			chain: "vflip,format=yuv420p",
		},
		{
			name: "flip both is 180", op: transform.VideoOp{FlipV: true, FlipH: true},
			w: 1280, h: 720, fps: 25, length: 2,
			chain: "hflip,vflip,format=yuv420p",
		},
		{
			name: "anamorphic", in: func(in *TranscodeInput) { in.Width, in.SARNum, in.SARDen = 960, 4, 3 },
			w: 1280, h: 720, fps: 25, length: 2,
			chain: "scale=1280:720:flags=bicubic,setsar=1,format=yuv420p",
		},
		{
			name: "odd source", in: func(in *TranscodeInput) { in.Height = 503 },
			w: 1280, h: 502, fps: 25, length: 2,
			chain: "crop=1280:502:0:0,format=yuv420p",
		},
		{
			name: "fps", op: transform.VideoOp{FPS: 12.5},
			w: 1280, h: 720, fps: 12.5, length: 2,
			chain: "fps=12.5,format=yuv420p",
		},
		{
			name: "source over the fps cap", in: func(in *TranscodeInput) { in.FPS = 120 },
			w: 1280, h: 720, fps: 60, length: 2,
			chain: "fps=60,format=yuv420p",
		},
		{
			name: "container start time", in: func(in *TranscodeInput) { in.StartTime = 1.4 },
			op: transform.VideoOp{Start: time.Second},
			w:  1280, h: 720, fps: 25, start: 2.4, length: 1,
			chain: "trim=start=2.4,setpts=PTS-2.4/TB,format=yuv420p",
		},
		{
			name: "negative container start", in: func(in *TranscodeInput) { in.StartTime = -0.021 },
			w: 1280, h: 720, fps: 25, start: -0.021, length: 2,
			chain: "setpts=PTS+0.021/TB,format=yuv420p",
		},
		{
			name: "unknown duration stops at the limit", in: func(in *TranscodeInput) { in.Duration = 0 },
			w: 1280, h: 720, fps: 25, end: 60,
			chain: "trim=end=60,format=yuv420p",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := hd()
			if tc.in != nil {
				tc.in(in)
			}
			op := tc.op
			op.Mute = true
			out, err := fullCaps().Resolve(&op)
			if err != nil {
				t.Fatal(err)
			}
			p, err := PlanTranscode(in, &op, out, fullCaps(), settings())
			if err != nil {
				t.Fatal(err)
			}
			if p.Audio != nil {
				t.Error("muted plan has audio")
			}
			v := p.Video
			got := []any{v.Width, v.Height, v.FPS, v.Filters, p.Start, p.End, p.Duration}
			want := []any{tc.w, tc.h, tc.fps, tc.chain, tc.start, tc.end, tc.length}
			if d := cmp.Diff(want, got); d != "" {
				t.Errorf("plan (-want +got):\n%s", d)
			}
		})
	}
}

func TestPlanTranscodeEncoder(t *testing.T) {
	t.Parallel()
	in := hd()
	for _, tc := range []struct {
		name string
		op   transform.VideoOp
		crf  int
		gop  int
	}{
		{"default crf", transform.VideoOp{Mute: true}, 36, 50},
		{"quality maps", transform.VideoOp{Mute: true, Quality: 100}, 9, 50},
		{"crf wins", transform.VideoOp{Mute: true, Quality: 100, CRF: 40}, 40, 50},
		{"fps sets the gop", transform.VideoOp{Mute: true, FPS: 10}, 36, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := fullCaps().Resolve(&tc.op)
			if err != nil {
				t.Fatal(err)
			}
			p, err := PlanTranscode(in, &tc.op, out, fullCaps(), settings())
			if err != nil {
				t.Fatal(err)
			}
			if p.Video.CRF != tc.crf || p.Video.GOP != tc.gop || p.Video.Speed != DefaultSpeed || !p.FastStart {
				t.Errorf("crf %d gop %d speed %d faststart %v, want %d %d %d true",
					p.Video.CRF, p.Video.GOP, p.Video.Speed, p.FastStart, tc.crf, tc.gop, DefaultSpeed)
			}
		})
	}
}

func TestPlanTranscodeAudio(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		in    func(in *TranscodeInput)
		op    transform.VideoOp
		want  *AudioStreamPlan
		err   errs.Kind
		video bool
	}{
		{
			name: "mp4 keeps 44.1 kHz", op: transform.VideoOp{Start: time.Second}, video: true,
			want: &AudioStreamPlan{Codec: AudioAAC, SampleRate: 44100, Channels: 2, Bitrate: 128_000,
				Filters: "atrim=start=1,asetpts=PTS-1/TB,aresample=44100,aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo"},
		},
		{
			name: "webm resamples to 48 kHz", op: transform.VideoOp{Container: transform.VideoContainerWebM}, video: true,
			want: &AudioStreamPlan{Codec: AudioOpus, SampleRate: 48000, Channels: 2, Bitrate: 96_000,
				Filters: "aresample=48000,aformat=sample_fmts=flt:sample_rates=48000:channel_layouts=stereo"},
		},
		{
			name: "mono m4a", in: func(in *TranscodeInput) { in.Channels, in.SampleRate = 1, 16000 },
			op: transform.VideoOp{Container: transform.VideoContainerM4A, AudioBitrate: 64_000},
			want: &AudioStreamPlan{Codec: AudioAAC, SampleRate: 16000, Channels: 1, Bitrate: 64_000,
				Filters: "aresample=16000,aformat=sample_fmts=fltp:sample_rates=16000:channel_layouts=mono"},
		},
		{
			name: "5.1 mp3 downmixes", in: func(in *TranscodeInput) { in.Channels, in.SampleRate = 6, 96000 },
			op: transform.VideoOp{Container: transform.VideoContainerMP3, End: 1500 * time.Millisecond},
			want: &AudioStreamPlan{Codec: AudioMP3, SampleRate: 44100, Channels: 2, Bitrate: 128_000,
				Filters: "atrim=end=1.5,aresample=44100,aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo"},
		},
		{
			name: "no audio stream in a video", in: func(in *TranscodeInput) { in.Audio = false }, video: true,
		},
		{
			name: "no audio stream for m4a", in: func(in *TranscodeInput) { in.Audio = false },
			op: transform.VideoOp{Container: transform.VideoContainerM4A}, err: errs.KindUnsupported,
		},
		{
			name: "undecodable audio", in: func(in *TranscodeInput) { in.AudioDecodable = false },
			err: errs.KindUnsupported,
		},
		{
			name: "undecodable audio muted", in: func(in *TranscodeInput) { in.AudioDecodable = false },
			op: transform.VideoOp{Mute: true}, video: true,
		},
		{
			name: "audio-only source in a video", in: func(in *TranscodeInput) { in.Video = false },
			err: errs.KindUnsupported,
		},
		{
			name: "audio-only source in m4a", in: func(in *TranscodeInput) { in.Video = false },
			op: transform.VideoOp{Container: transform.VideoContainerM4A},
			want: &AudioStreamPlan{Codec: AudioAAC, SampleRate: 44100, Channels: 2, Bitrate: 128_000,
				Filters: "aresample=44100,aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := hd()
			if tc.in != nil {
				tc.in(in)
			}
			out, err := fullCaps().Resolve(&tc.op)
			if err != nil {
				t.Fatal(err)
			}
			p, err := PlanTranscode(in, &tc.op, out, fullCaps(), settings())
			if tc.err != errs.KindUnknown {
				if errs.KindOf(err) != tc.err {
					t.Fatalf("err %v, want %s", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(tc.want, p.Audio); d != "" {
				t.Errorf("audio (-want +got):\n%s", d)
			}
			if (p.Video != nil) != tc.video {
				t.Errorf("video stream %v, want %v", p.Video != nil, tc.video)
			}
		})
	}
}

func TestPlanTranscodeLimits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		op   transform.VideoOp
		set  func(s *TranscodeSettings)
	}{
		{"start past the end", transform.VideoOp{Start: 2 * time.Second}, nil},
		{"too long", transform.VideoOp{}, func(s *TranscodeSettings) { s.MaxOutputDuration = time.Second }},
		{"too many pixels", transform.VideoOp{Width: 3840}, func(s *TranscodeSettings) { s.MaxOutputPixels = 1920 * 1080 }},
		{"fps over the cap", transform.VideoOp{FPS: 120}, nil},
		{"crop outside", transform.VideoOp{Crop: &transform.VideoCrop{X: 1280, Width: 10, Height: 10}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := settings()
			if tc.set != nil {
				tc.set(s)
			}
			op := tc.op
			op.Mute = true
			out, err := fullCaps().Resolve(&op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = PlanTranscode(hd(), &op, out, fullCaps(), s)
			if errs.KindOf(err) != errs.KindLimitExceeded {
				t.Fatalf("err %v, want limit_exceeded", err)
			}
			if e, ok := errors.AsType[*errs.Error](err); !ok || e.Public == "" {
				t.Errorf("no public message: %v", err)
			}
		})
	}
}

// TestFilterChainsAreNumeric: whatever the request, a chain holds only
// the fixed filter vocabulary and numbers (no request text reaches
// FFmpeg's filter syntax).
func TestFilterChainsAreNumeric(t *testing.T) {
	t.Parallel()
	const allowed = "abcdefghijklmnopqrstuvwxyz_0123456789=:,.-+/PTSB"
	ops := []transform.VideoOp{
		{Start: 1234567 * time.Microsecond, End: 1987654 * time.Microsecond, Width: 333, Height: 222,
			Fit: transform.VideoFitPad, Background: transform.Color{R: 1, G: 2, B: 3}, Rotate: 270, FlipH: true, FPS: 29.97},
		{Crop: &transform.VideoCrop{X: 1, Y: 3, Width: 99, Height: 77}, Fit: transform.VideoFitCover, Width: 64, Height: 48},
	}
	for _, op := range ops {
		out, err := fullCaps().Resolve(&op)
		if err != nil {
			t.Fatal(err)
		}
		p, err := PlanTranscode(hd(), &op, out, fullCaps(), settings())
		if err != nil {
			t.Fatal(err)
		}
		for _, chain := range []string{p.Video.Filters, p.Audio.Filters} {
			if i := strings.IndexFunc(chain, func(r rune) bool { return !strings.ContainsRune(allowed, r) }); i >= 0 {
				t.Errorf("chain %q has %q", chain, chain[i])
			}
		}
	}
}
