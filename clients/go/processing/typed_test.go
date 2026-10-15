package processing_test

import (
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
	"github.com/xantiagoma/xtandard-image/clients/go/info"
	"github.com/xantiagoma/xtandard-image/clients/go/processing"
	"github.com/xantiagoma/xtandard-image/clients/go/video"
)

// The typed structs write the same segments as the untyped options.
func TestTypedOptions(t *testing.T) {
	t.Parallel()
	b, err := imgproxy.New(imgproxy.Config{SourceEncoding: xtandard.SourceEncodingPlain})
	if err != nil {
		t.Fatal(err)
	}
	const src = "/plain/s3://b/a.jpg"
	tests := []struct {
		opt  xtandard.ProcessingOption
		want string
	}{
		{processing.Resize{Type: processing.ResizeTypeFill, Width: new(300), Height: new(400), Enlarge: new(false)}, "/insecure/rs:fill:300:400:0"},
		{processing.Resize{Type: processing.ResizeTypeFit, Height: new(300)}, "/insecure/rs:fit::300"},
		{processing.Resize{Type: processing.ResizeTypeFillDown}, "/insecure/rs:fill-down"},
		{processing.Width{Width: 0}, "/insecure/w:0"},
		{processing.Quality{Quality: 80}, "/insecure/q:80"},
		{processing.Gravity{Type: processing.GravityTypeNowe, XOffset: new(10.0)}, "/insecure/g:nowe:10"},
		{processing.GravitySm{}, "/insecure/g:sm"},
		{processing.GravityFp{X: 0.5, Y: 0.25}, "/insecure/g:fp:0.5:0.25"},
		{processing.GravityObj{Classes: []string{"face", "cat"}}, "/insecure/g:obj:face:cat"},
		{processing.GravityObjw{Items: []processing.GravityObjwItem{{Class: "face", Weight: 2}, {Class: "all", Weight: 0.5}}}, "/insecure/g:objw:face:2:all:0.5"},
		{processing.FormatQuality{Items: []processing.FormatQualityItem{{Format: processing.FormatQualityFormatJPEG, Quality: 70}, {Format: processing.FormatQualityFormatWebP, Quality: 60}}}, "/insecure/fq:jpeg:70:webp:60"},
		{processing.BackgroundRGB{R: 255, G: 0, B: 0}, "/insecure/bg:255:0:0"},
		{processing.Background{Color: "ff0000"}, "/insecure/bg:ff0000"},
		{processing.Blur{Sigma: 2.5}, "/insecure/bl:2.5"},
		{processing.Preset{Names: []string{"a", "b"}}, "/insecure/pr:a:b"},
		{processing.Adjust{Contrast: new(1.1)}, "/insecure/a::1.1"},
		{xtandard.Opt("min-width", 100), "/insecure/mw:100"},
	}
	for _, tt := range tests {
		got, err := b.URL("s3://b/a.jpg", imgproxy.Params{Options: []xtandard.ProcessingOption{tt.opt}})
		if err != nil {
			t.Errorf("%#v: %v", tt.opt, err)
			continue
		}
		if want := tt.want + src; got != want {
			t.Errorf("%#v:\n got %s\nwant %s", tt.opt, got, want)
		}
	}
}

func TestTypedInfoAndVideo(t *testing.T) {
	t.Parallel()
	b, err := imgproxy.New(imgproxy.Config{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.InfoURL("s3://b/a.jpg", imgproxy.InfoParams{Options: []xtandard.InfoOption{
		info.Size{Size: new(true)}, info.Blurhash{XComponents: 4, YComponents: 3}, nil,
	}})
	if want := "/info/insecure/s:1/bh:4:3/czM6Ly9iL2EuanBn"; err != nil || got != want {
		t.Errorf("InfoURL = %s, %v; want %s", got, err, want)
	}
	got, err = b.VideoURL("s3://m/a.mov", imgproxy.VideoParams{
		Options:   []xtandard.VideoOption{video.Start{Seconds: 12}, video.Duration{Seconds: 8}, video.Mute{Mute: new(true)}},
		Extension: "webm",
	})
	if want := "/video/insecure/vs:12/vd:8/mu:1/czM6Ly9tL2EubW92.webm"; err != nil || got != want {
		t.Errorf("VideoURL = %s, %v; want %s", got, err, want)
	}
}
