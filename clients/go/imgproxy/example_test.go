package imgproxy_test

import (
	"errors"
	"fmt"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
	"github.com/xantiagoma/xtandard-image/clients/go/processing"
	"github.com/xantiagoma/xtandard-image/clients/go/video"
)

func ExampleNew() {
	key, err := xtandard.ParseSigningKey("736563726574", "68656c6c6f", xtandard.DefaultSignatureSize)
	if err != nil {
		panic(err)
	}
	b, err := imgproxy.New(imgproxy.Config{BaseURL: "https://img.example.com", Signing: &key})
	if err != nil {
		panic(err)
	}
	url, _ := b.URL("s3://photos/cat.jpg", imgproxy.Params{
		Options:   []xtandard.ProcessingOption{xtandard.Opt("rs", "fill", 1200, 630), xtandard.Opt("g", "sm")},
		Extension: "jpg",
	})
	fmt.Println(url)
	// Output: https://img.example.com/aKddQgLPiNLFog5MXF3xOYc7Cmz_hUnL4By67usdJC0/rs:fill:1200:630/g:sm/czM6Ly9waG90b3MvY2F0LmpwZw.jpg
}

func ExampleBuilder_URL() {
	b, _ := imgproxy.New(imgproxy.Config{})
	// Typed options (package processing) and untyped ones mix freely.
	url, err := b.URL("https://example.com/cat.jpg", imgproxy.Params{
		Options: []xtandard.ProcessingOption{
			processing.Resize{Type: processing.ResizeTypeFill, Width: new(300), Height: new(400)},
			processing.Quality{Quality: 80},
		},
		Pipelines: [][]xtandard.ProcessingOption{{xtandard.Opt("bl", 2)}},
		Extension: "webp",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(url)
	// Output: /insecure/rs:fill:300:400/q:80/-/bl:2/aHR0cHM6Ly9leGFtcGxlLmNvbS9jYXQuanBn.webp
}

func ExampleBuilder_Image() {
	b, _ := imgproxy.New(imgproxy.Config{SourceEncoding: xtandard.SourceEncodingPlain})
	url, err := b.Image("s3://photos/cat.jpg").Resize("fit", 300, 300).Blur(2).Extension("png").Build()
	if err != nil {
		panic(err)
	}
	fmt.Println(url)
	_, err = b.Image("s3://photos/cat.jpg").Option("nope", 1).Build()
	fmt.Println(errors.Is(err, xtandard.ErrUnknownOption))
	// Output:
	// /insecure/rs:fit:300:300/bl:2/plain/s3://photos/cat.jpg@png
	// true
}

func ExampleBuilder_VideoURL() {
	b, _ := imgproxy.New(imgproxy.Config{})
	url, _ := b.VideoURL("s3://media/talk.mov", imgproxy.VideoParams{
		Options:   []xtandard.VideoOption{video.Start{Seconds: 12}, video.Duration{Seconds: 8}},
		Extension: "webm",
	})
	fmt.Println(url)
	// Output: /video/insecure/vs:12/vd:8/czM6Ly9tZWRpYS90YWxrLm1vdg.webm
}

func ExampleBuilder_StreamURL() {
	b, _ := imgproxy.New(imgproxy.Config{})
	url, _ := b.StreamURL("s3://media/talk.mov", imgproxy.StreamParams{
		Protocol:        xtandard.StreamProtocolHLS,
		Renditions:      []int{360, 720},
		SegmentDuration: 4,
	})
	fmt.Println(url)
	// Output: /video/insecure/st:hls:360:720/sgd:4/czM6Ly9tZWRpYS90YWxrLm1vdg.m3u8
}

func ExampleBuilder_InfoURL() {
	b, _ := imgproxy.New(imgproxy.Config{})
	url, _ := b.Info("s3://photos/cat.jpg").Size(true).Format(true).Dimensions(true).Build()
	fmt.Println(url)
	// Output: /info/insecure/s:1/f:1/d:1/czM6Ly9waG90b3MvY2F0LmpwZw
}
