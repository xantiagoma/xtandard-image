package client_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/client"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
	"github.com/xantiagoma/xtandard-image/clients/go/info"
)

func Example() {
	// A stand-in for an xtandard-image server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info/insecure/s:1/f:1/czM6Ly9waG90b3MvY2F0LmpwZw":
			_, _ = io.WriteString(w, `{"format":"jpeg","size":48213}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"kind":"not_found","message":"source image not found"}}`)
		}
	}))
	defer srv.Close()

	c, err := client.New(client.Config{URLs: imgproxy.Config{BaseURL: srv.URL}})
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	in, err := c.Info(ctx, "s3://photos/cat.jpg", imgproxy.InfoParams{
		Options: []xtandard.InfoOption{info.Size{Size: new(true)}, info.Format{Format: new(true)}},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(*in.Format, *in.Size)

	res, err := c.Image(ctx, "s3://photos/missing.jpg", imgproxy.Params{})
	if err == nil {
		defer res.Body.Close() // stream res.Body: the image
	}
	if he, ok := errors.AsType[*client.HTTPError](err); ok {
		fmt.Println(he.Status, he.Kind, errors.Is(err, xtandard.ErrorKindNotFound))
	}
	// Output:
	// jpeg 48213
	// 404 not_found true
}
