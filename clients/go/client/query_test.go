package client_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/client"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
)

// sent is what the recorder saw of one request.
type sent struct {
	Method, URI, ContentType, Body string
}

// recorder is a stand-in server that records every request, body included.
func recorder(t *testing.T) (*httptest.Server, chan sent) {
	t.Helper()
	got := make(chan sent, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- sent{r.Method, r.RequestURI, r.Header.Get("Content-Type"), string(body)}
		_, _ = io.WriteString(w, "img")
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

const (
	q    = "/q/?url=https://e.com/a.jpg&w=300&xi_sig=abc"
	form = "application/x-www-form-urlencoded"
)

func TestQueryMethod(t *testing.T) {
	t.Parallel()
	type call struct {
		url  string
		opts []client.RequestOption
	}
	tests := []struct {
		name  string
		cfg   client.Config
		calls []call
		want  []sent
	}{
		{
			name:  "off by default",
			calls: []call{{url: q}},
			want:  []sent{{Method: http.MethodGet, URI: q}},
		},
		{
			name: "QueryMethod sends URLs under the mount as QUERY",
			cfg:  client.Config{QueryMethod: true},
			calls: []call{
				{url: q},
				{url: "/q/video?url=s3://m/a.mov&vd=5"},
				{url: "/insecure/rs:fit:300:300/plain/https://e.com/a.jpg"},
				{url: "/query?url=x"},
				{url: q, opts: []client.RequestOption{client.Head()}},
				{url: q, opts: []client.RequestOption{client.Get()}},
			},
			want: []sent{
				{Method: "QUERY", URI: "/q/", ContentType: form, Body: "url=https://e.com/a.jpg&w=300&xi_sig=abc"},
				{Method: "QUERY", URI: "/q/video", ContentType: form, Body: "url=s3://m/a.mov&vd=5"},
				{Method: http.MethodGet, URI: "/insecure/rs:fit:300:300/plain/https://e.com/a.jpg"},
				{Method: http.MethodGet, URI: "/query?url=x"},
				{Method: http.MethodHead, URI: q},
				{Method: http.MethodGet, URI: q},
			},
		},
		{
			name: "Query on one call, with a custom mount",
			cfg:  client.Config{QueryMount: "/img/q/"},
			calls: []call{
				{url: "/img/q/resize?url=x&width=1", opts: []client.RequestOption{client.Query()}},
				{url: "/img/q/resize?url=x&width=1"},
			},
			want: []sent{
				{Method: "QUERY", URI: "/img/q/resize", ContentType: form, Body: "url=x&width=1"},
				{Method: http.MethodGet, URI: "/img/q/resize?url=x&width=1"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, got := recorder(t)
			cfg := tt.cfg
			cfg.URLs = imgproxy.Config{BaseURL: srv.URL}
			c, err := client.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var all []sent
			for _, cl := range tt.calls {
				res, err := c.Media(t.Context(), cl.url, cl.opts...)
				if err != nil {
					t.Fatalf("Media(%s): %v", cl.url, err)
				}
				_ = res.Body.Close()
				all = append(all, <-got)
			}
			if !slices.Equal(all, tt.want) {
				t.Errorf("requests:\n got %+v\nwant %+v", all, tt.want)
			}
		})
	}
}

func TestQueryMethodWithoutAQuery(t *testing.T) {
	t.Parallel()
	srv, got := recorder(t)
	c, err := client.New(client.Config{URLs: imgproxy.Config{BaseURL: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Media(t.Context(), "/q/", client.Query())
	if err == nil {
		_ = res.Body.Close()
	}
	if !errors.Is(err, xtandard.ErrInvalidRequest) {
		t.Errorf("Media(/q/, Query()) = %v, want ErrInvalidRequest", err)
	}
	if len(got) != 0 {
		t.Errorf("sent %d requests", len(got))
	}
}
