package video

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// TestPlanTranscodeOptions: a crop of 0 height is the whole side, the GOP
// follows the keyframe interval, and the video fades sit before the pixel
// format (WP-122, WP-123).
func TestPlanTranscodeOptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		op    transform.VideoOp
		chain string
		gop   int
	}{
		{
			name: "crop south-east, full height", op: transform.VideoOp{Crop: &transform.VideoCrop{Width: 640, Gravity: new(transform.VideoGravitySouthEast)}},
			chain: "crop=640:720:640:0,format=yuv420p", gop: 50,
		},
		{
			name: "keyframe interval and fades", op: transform.VideoOp{KeyframeInterval: 500 * time.Millisecond, FadeIn: time.Second, FadeOut: 500 * time.Millisecond},
			chain: "fade=t=in:st=0:d=1,fade=t=out:st=1.5:d=0.5,format=yuv420p", gop: 13,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			op := tc.op
			op.Mute = true
			out, err := fullCaps().Resolve(&op)
			if err != nil {
				t.Fatal(err)
			}
			p, err := PlanTranscode(hd(), &op, out, fullCaps(), settings())
			if err != nil {
				t.Fatal(err)
			}
			if p.Video.Filters != tc.chain || p.Video.GOP != tc.gop {
				t.Errorf("chain %q gop %d, want %q %d", p.Video.Filters, p.Video.GOP, tc.chain, tc.gop)
			}
		})
	}
}

// TestPlanWaveform: a waveform picture is one audio chain ending in
// showwavespic and RGBA, at the default or the requested size.
func TestPlanWaveform(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   func(in *TranscodeInput)
		op   transform.VideoOp
		want *WaveformPlan
		err  errs.Kind
	}{
		{
			name: "defaults", op: transform.VideoOp{Container: transform.VideoContainerPNG, Waveform: &transform.VideoWaveform{}},
			want: &WaveformPlan{
				Width: 1024, Height: 256, Format: transform.VideoContainerPNG,
				Filters: "aformat=sample_fmts=s16:channel_layouts=mono,showwavespic=s=1024x256:split_channels=0:colors=0x000000,format=rgba",
			},
		},
		{
			name: "split, cut, colours",
			op: transform.VideoOp{
				Container: transform.VideoContainerWebP, Width: 600, Height: 120, Start: time.Second,
				Waveform: &transform.VideoWaveform{Color: transform.Color{R: 255, G: 128}, Background: transform.Color{B: 255, A: 255}, SplitChannels: true},
			},
			want: &WaveformPlan{
				Width: 600, Height: 120, Format: transform.VideoContainerWebP, Background: transform.Color{B: 255, A: 255},
				Filters: "atrim=start=1,asetpts=PTS-1/TB,aformat=sample_fmts=s16:channel_layouts=stereo," +
					"showwavespic=s=600x120:split_channels=1:colors=0xff8000,format=rgba",
			},
		},
		{
			name: "no audio", in: func(in *TranscodeInput) { in.Audio = false },
			op: transform.VideoOp{Container: transform.VideoContainerPNG, Waveform: &transform.VideoWaveform{}}, err: errs.KindUnsupported,
		},
		{
			name: "undecodable audio", in: func(in *TranscodeInput) { in.AudioDecodable = false },
			op: transform.VideoOp{Container: transform.VideoContainerPNG, Waveform: &transform.VideoWaveform{}}, err: errs.KindUnsupported,
		},
		{
			name: "too large", op: transform.VideoOp{Container: transform.VideoContainerPNG, Width: 8000, Height: 8000, Waveform: &transform.VideoWaveform{}},
			err: errs.KindLimitExceeded,
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
			if p.Video != nil || p.Audio != nil {
				t.Errorf("waveform plan has streams: %+v %+v", p.Video, p.Audio)
			}
			if d := cmp.Diff(tc.want, p.Waveform); d != "" {
				t.Errorf("waveform (-want +got):\n%s", d)
			}
		})
	}
}
