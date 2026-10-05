package transform

import (
	"testing"
	"time"
)

func TestValidateVideoStream(t *testing.T) {
	t.Parallel()
	valid := []struct {
		name string
		op   VideoOp
	}{
		{"hls", VideoOp{Stream: &VideoStream{}}},
		{"dash_renditions", VideoOp{Stream: &VideoStream{Protocol: StreamDASH, Renditions: []int{240, 360, 720}, SegmentDuration: 4 * time.Second}}},
		{"profile", VideoOp{Stream: &VideoStream{MinHeight: 480, MaxHeight: 1080, MaxRenditions: 3}}},
		{"trim_crop_av1_muted", VideoOp{
			Start: time.Second, Duration: 10 * time.Second, Crop: &VideoCrop{Width: 640, Height: 360},
			VideoCodec: VideoCodecAV1, Mute: true, FPS: 30, KeyframeInterval: 2 * time.Second, Stream: &VideoStream{},
		}},
		{"audio_options", VideoOp{AudioCodec: AudioCodecAAC, SampleRate: 48000, Channels: 1, Volume: 0.5, Stream: &VideoStream{}}},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.name, func(t *testing.T) {
			t.Parallel()
			r := videoBase()
			*r.Video = tc.op
			if err := r.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	invalid := []struct {
		name  string
		st    VideoStream
		mut   func(o *VideoOp)
		field string
	}{
		{"protocol", VideoStream{Protocol: numStreamProtocols}, nil, "video.stream.protocol"},
		{"rendition small", VideoStream{Renditions: []int{32}}, nil, "video.stream.renditions[0]"},
		{"rendition duplicate", VideoStream{Renditions: []int{360, 360}}, nil, "video.stream.renditions[1]"},
		{"renditions many", VideoStream{Renditions: []int{100, 200, 300, 400, 500, 600, 700, 800, 900}}, nil, "video.stream.renditions"},
		{"min above max", VideoStream{MinHeight: 720, MaxHeight: 480}, nil, "video.stream.min_height"},
		{"max renditions", VideoStream{MaxRenditions: MaxRenditions + 1}, nil, "video.stream.max_renditions"},
		{"segment fraction", VideoStream{SegmentDuration: 1500 * time.Millisecond}, nil, "video.stream.segment_duration"},
		{"segment long", VideoStream{SegmentDuration: time.Minute}, nil, "video.stream.segment_duration"},
		{"webm", VideoStream{}, func(o *VideoOp) { o.Container = VideoContainerWebM }, "video.stream"},
		{"auto container", VideoStream{}, func(o *VideoOp) { o.AutoContainer = true }, "video.stream"},
		{"width", VideoStream{}, func(o *VideoOp) { o.Width = 640 }, "video.stream"},
		{"bitrate", VideoStream{}, func(o *VideoOp) { o.MaxBitrate = 1_000_000 }, "video.stream"},
		{"overlay", VideoStream{}, func(o *VideoOp) { o.Overlays = []VideoOverlay{{Opacity: 1}} }, "video.stream"},
		{"hevc", VideoStream{}, func(o *VideoOp) { o.VideoCodec = VideoCodecHEVC }, "video.stream"},
		{"sample rate", VideoStream{}, func(o *VideoOp) { o.SampleRate = 44100 }, "video.stream"},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			t.Parallel()
			r := videoBase()
			r.Video.Stream = &tc.st
			if tc.mut != nil {
				tc.mut(r.Video)
			}
			assertField(t, r.Validate(), tc.field)
		})
	}
}

// TestVideoStreamKey: the stream is part of the canonical key, so an HLS
// and a DASH manifest of the same source never share a cache entry.
func TestVideoStreamKey(t *testing.T) {
	t.Parallel()
	keys := map[string]string{}
	for name, st := range map[string]*VideoStream{
		"file": nil,
		"hls":  {},
		"dash": {Protocol: StreamDASH},
		"240":  {Renditions: []int{240}},
		"seg4": {SegmentDuration: 4 * time.Second},
	} {
		r := videoBase()
		r.Video.Stream = st
		k := r.CanonicalKey()
		if other, ok := keys[k]; ok {
			t.Fatalf("%s and %s share key %s", name, other, k)
		}
		keys[k] = name
	}
}
