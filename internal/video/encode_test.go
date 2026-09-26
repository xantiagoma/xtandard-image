package video_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/video"
)

// TestResolve is the output contract of FORMATS §5.1 per image flavour.
func TestResolve(t *testing.T) {
	t.Parallel()
	type res struct {
		Codec     video.Codec
		Container video.Container
		OK        bool
	}
	slim := video.EncoderSet{}
	full := video.EncoderSet{VP9: true, AV1: true}
	gpl := video.EncoderSet{VP9: true, AV1: true, H264: true, HEVC: true}
	tests := []struct {
		name string
		set  video.EncoderSet
		out  video.Output
		want res
	}{
		{"slim webm", slim, video.OutputWebM, res{video.CodecVP9, video.ContainerWebM, false}},
		{"slim mp4", slim, video.OutputMP4, res{video.CodecNone, video.ContainerNone, false}},
		{"full webm", full, video.OutputWebM, res{video.CodecVP9, video.ContainerWebM, true}},
		{"full mp4 is av1", full, video.OutputMP4, res{video.CodecAV1, video.ContainerMP4, true}},
		{"full hevc", full, video.OutputHEVC, res{video.CodecHEVC, video.ContainerMP4, false}},
		{"gpl mp4 is h264", gpl, video.OutputMP4, res{video.CodecH264, video.ContainerMP4, true}},
		{"gpl hevc", gpl, video.OutputHEVC, res{video.CodecHEVC, video.ContainerMP4, true}},
		{"gpl webm", gpl, video.OutputWebM, res{video.CodecVP9, video.ContainerWebM, true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, k, ok := tc.set.Resolve(tc.out)
			if diff := cmp.Diff(tc.want, res{c, k, ok}); diff != "" {
				t.Errorf("Resolve (-want +got):\n%s", diff)
			}
		})
	}
	if got := gpl.Names(); !cmp.Equal(got, []string{"vp9", "av1", "h264", "hevc"}) {
		t.Errorf("names %v", got)
	}
}

func TestCRF(t *testing.T) {
	t.Parallel()
	tests := []struct {
		codec        video.Codec
		quality, def int
		want         int
		name         string
	}{
		{video.CodecVP9, 0, 36, 36, "unset keeps the default"},
		{video.CodecVP9, 80, 36, 36, "reference quality"},
		{video.CodecVP9, 100, 36, 9, "best is a quarter"},
		{video.CodecVP9, 90, 36, 23, "between"},
		{video.CodecVP9, 1, 36, 63, "worst"},
		{video.CodecH264, 1, 25, 51, "h264 worst"},
		{video.CodecH264, 100, 25, 6, "h264 best"},
		{video.CodecAV1, 50, 36, 46, "below the reference"},
		{video.CodecHEVC, 150, 99, 13, "clamped inputs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := video.CRF(tc.codec, tc.quality, tc.def); got != tc.want {
				t.Errorf("CRF(%s, q%d, %d) = %d, want %d", tc.codec, tc.quality, tc.def, got, tc.want)
			}
		})
	}
}

func TestFrameDelay(t *testing.T) {
	t.Parallel()
	for in, want := range map[int]int{0: 100, 10: 100, 11: 11, 40: 40, 1000: 1000} {
		if got := video.FrameDelay(in); got != want {
			t.Errorf("FrameDelay(%d) = %d, want %d", in, got, want)
		}
	}
}
