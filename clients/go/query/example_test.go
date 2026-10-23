package query_test

import (
	"fmt"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/query"
)

func ExampleBuilder_URL() {
	q, _ := query.New(query.Config{BaseURL: "https://img.example.com"})
	url, _ := q.URL("s3://photos/cat.jpg", query.Params{
		Params:  []query.Param{query.P(xtandard.QueryKeyW, 300), query.P(xtandard.QueryKeyF, "webp")},
		Options: []xtandard.ProcessingOption{xtandard.Opt("bl", 2)},
	})
	fmt.Println(url)
	// Output: https://img.example.com/q/?url=s3://photos/cat.jpg&w=300&f=webp&o=bl:2
}

func ExampleBuilder_Video() {
	q, _ := query.New(query.Config{})
	url, _ := q.Video("s3://media/talk.mov").Resize("fill", 640, 360).Start(1).Extension("webm").Build()
	fmt.Println(url)
	// Output: /q/video?url=s3://media/talk.mov&rs=fill,640,360&vs=1&format=webm
}

func ExampleBuilder_ImaginaryURL() {
	key, _ := xtandard.ParseSigningKey("736563726574", "68656c6c6f", xtandard.DefaultSignatureSize)
	q, _ := query.New(query.Config{Signing: &key})
	url, _ := q.ImaginaryURL(xtandard.ImaginaryEndpointResize, "s3://photos/cat.jpg",
		query.P(xtandard.ImaginaryParamWidth, 300), query.P(xtandard.ImaginaryParamHeight, 200))
	fmt.Println(url)
	// Output: /q/resize?url=s3://photos/cat.jpg&width=300&height=200&xi_sig=3maa5jzKMQ2qhuNRWAxw3msmG88vFanE-OZ9YLxZlr0
}

func ExampleMethodRequest() {
	q, _ := query.New(query.Config{BaseURL: "https://img.example.com"})
	url, _ := q.URL("s3://photos/cat.jpg", query.Params{Params: []query.Param{query.P(xtandard.QueryKeyW, 300)}})
	r, _ := query.MethodRequest(url)
	fmt.Println(r.Method, r.URL)
	fmt.Println("Content-Type:", r.ContentType)
	fmt.Println(r.Body)
	// Output:
	// QUERY https://img.example.com/q/
	// Content-Type: application/x-www-form-urlencoded
	// url=s3://photos/cat.jpg&w=300
}
