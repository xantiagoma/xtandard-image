package transform

import (
	"errors"
	"testing"
	"time"
)

func videoBase() Request {
	r := Request{Kind: KindVideo, Source: src("clip.mp4"), Video: &VideoOp{}}
	r.ApplyDefaults(StandardDefaults())
	return r
}

func TestValidateVideo(t *testing.T) {
	t.Parallel()
	valid := []struct {
		name string
		op   VideoOp
	}{
		{"zero", VideoOp{}},
		{"trim_resize_webm", VideoOp{
			Start: time.Second, Duration: 2 * time.Second, Width: 640, Fit: VideoFitScaleDown,
			Container: VideoContainerWebM, VideoCodec: VideoCodecVP9, AudioCodec: AudioCodecOpus,
		}},
		{"end", VideoOp{Start: time.Second, End: 3 * time.Second}},
		{"cover_crop_rotate", VideoOp{
			Width: 320, Height: 320, Fit: VideoFitCover, Crop: &VideoCrop{X: 10, Y: 10, Width: 100, Height: 50},
			Rotate: 270, FlipV: true, FPS: 30, CRF: 30, MaxBitrate: 500_000,
		}},
		{"m4a", VideoOp{Container: VideoContainerM4A, AudioCodec: AudioCodecAAC, AudioBitrate: 128_000}},
		{"opus", VideoOp{Container: VideoContainerOpus}},
		{"mp3", VideoOp{Container: VideoContainerMP3, AudioCodec: AudioCodecMP3}},
		{"mp4_av1_muted", VideoOp{VideoCodec: VideoCodecAV1, Mute: true, Quality: 100}},
		{"webm_av1", VideoOp{Container: VideoContainerWebM, VideoCodec: VideoCodecAV1}},
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
		mut   func(o *VideoOp)
		field string
	}{
		{"start negative", func(o *VideoOp) { o.Start = -1 }, "video.start"},
		{"duration huge", func(o *VideoOp) { o.Duration = MaxVideoTime + 1 }, "video.duration"},
		{"end and duration", func(o *VideoOp) { o.End, o.Duration = 2*time.Second, time.Second }, "video.end"},
		{"end before start", func(o *VideoOp) { o.Start, o.End = 2*time.Second, time.Second }, "video.end"},
		{"width huge", func(o *VideoOp) { o.Width = MaxVideoDimension + 1 }, "video.width"},
		{"height negative", func(o *VideoOp) { o.Height = -2 }, "video.height"},
		{"fit unknown", func(o *VideoOp) { o.Fit = numVideoFits }, "video.fit"},
		{"cover without height", func(o *VideoOp) { o.Fit, o.Width = VideoFitCover, 100 }, "video.fit"},
		{"pad without box", func(o *VideoOp) { o.Fit = VideoFitPad }, "video.fit"},
		{"crop tiny", func(o *VideoOp) { o.Crop = &VideoCrop{Width: 1, Height: 10} }, "video.crop.width"},
		{"crop negative", func(o *VideoOp) { o.Crop = &VideoCrop{X: -1, Width: 10, Height: 10} }, "video.crop.x"},
		{"rotate 45", func(o *VideoOp) { o.Rotate = 45 }, "video.rotate"},
		{"fps tiny", func(o *VideoOp) { o.FPS = 0.5 }, "video.fps"},
		{"fps huge", func(o *VideoOp) { o.FPS = MaxVideoFPS + 1 }, "video.fps"},
		{"container unknown", func(o *VideoOp) { o.Container = numVideoContainers }, "video.container"},
		{"vp9 in mp4", func(o *VideoOp) { o.VideoCodec = VideoCodecVP9 }, "video.video_codec"},
		{"h264 in webm", func(o *VideoOp) { o.Container, o.VideoCodec = VideoContainerWebM, VideoCodecH264 }, "video.video_codec"},
		{"video in m4a", func(o *VideoOp) { o.Container, o.VideoCodec = VideoContainerM4A, VideoCodecH264 }, "video.video_codec"},
		{"opus in mp4", func(o *VideoOp) { o.AudioCodec = AudioCodecOpus }, "video.audio_codec"},
		{"aac in webm", func(o *VideoOp) { o.Container, o.AudioCodec = VideoContainerWebM, AudioCodecAAC }, "video.audio_codec"},
		{"muted mp3", func(o *VideoOp) { o.Container, o.Mute = VideoContainerMP3, true }, "video.mute"},
		{"quality", func(o *VideoOp) { o.Quality = 101 }, "video.quality"},
		{"crf", func(o *VideoOp) { o.CRF = 64 }, "video.crf"},
		{"bitrate low", func(o *VideoOp) { o.MaxBitrate = 1000 }, "video.max_bitrate"},
		{"audio bitrate high", func(o *VideoOp) { o.AudioBitrate = 1 << 30 }, "video.audio_bitrate"},
		{"overlays", func(o *VideoOp) { o.Overlays = []VideoOverlay{{Source: src("logo.png")}} }, "video.overlays"},
	}
	for _, tc := range invalid {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			t.Parallel()
			r := videoBase()
			tc.mut(r.Video)
			assertField(t, r.Validate(), tc.field)
		})
	}

	t.Run("video without kind", func(t *testing.T) {
		t.Parallel()
		r := validBase()
		r.Video = &VideoOp{}
		assertField(t, r.Validate(), "video")
	})
	t.Run("kind without video", func(t *testing.T) {
		t.Parallel()
		r := videoBase()
		r.Video = nil
		assertField(t, r.Validate(), "video")
	})
}

// assertField fails unless err holds a ValidationError for field.
func assertField(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want one for %s", field)
	}
	all := []error{err}
	if list, ok := errors.AsType[interface{ Unwrap() []error }](err); ok {
		all = list.Unwrap()
	}
	for _, e := range all {
		if ve, ok := errors.AsType[*ValidationError](e); ok && ve.Field == field {
			return
		}
	}
	t.Fatalf("error %v has no ValidationError for %s", err, field)
}

func TestVideoOpEndTime(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   VideoOp
		want time.Duration
	}{
		{VideoOp{}, 0},
		{VideoOp{Start: time.Second, End: 4 * time.Second}, 4 * time.Second},
		{VideoOp{Start: time.Second, Duration: 2 * time.Second}, 3 * time.Second},
	} {
		if got := tc.op.EndTime(); got != tc.want {
			t.Errorf("%+v: EndTime = %s, want %s", tc.op, got, tc.want)
		}
	}
}

func TestVideoCanonicalKeyDistinct(t *testing.T) {
	t.Parallel()
	a, b := videoBase(), videoBase()
	b.Video.Container = VideoContainerWebM
	if a.CanonicalKey() == b.CanonicalKey() {
		t.Fatal("mp4 and webm share a canonical key")
	}
	c := videoBase()
	c.Video.Container = VideoContainerMP4 // the zero value: same key as unset
	if a.CanonicalKey() != c.CanonicalKey() {
		t.Fatal("explicit mp4 changes the key")
	}
	img := validBase()
	if img.CanonicalKey() == a.CanonicalKey() {
		t.Fatal("an image request and a video request share a key")
	}
}
