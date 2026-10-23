package query_test

import (
	"errors"
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/query"
)

func TestMethodRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url  string
		want query.Request
	}{
		{"https://h/q/?url=x", query.Request{Method: "QUERY", URL: "https://h/q/", ContentType: query.ContentType, Body: "url=x"}},
		// Split at the first "?": a later one belongs to the content.
		{"/q/?url=https://e.com/a.jpg?x=1", query.Request{Method: "QUERY", URL: "/q/", ContentType: query.ContentType, Body: "url=https://e.com/a.jpg?x=1"}},
		{"/q/watermark?url=x&text=a%20b", query.Request{Method: "QUERY", URL: "/q/watermark", ContentType: query.ContentType, Body: "url=x&text=a%20b"}},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			got, err := query.MethodRequest(tt.url)
			if err != nil || got != tt.want {
				t.Errorf("MethodRequest(%q) = %+v, %v; want %+v", tt.url, got, err, tt.want)
			}
		})
	}
}

func TestMethodRequestErrors(t *testing.T) {
	t.Parallel()
	for _, url := range []string{"/q/", "/q/?", "https://h/q/", "/q/?url=x#frag", ""} {
		t.Run(url, func(t *testing.T) {
			t.Parallel()
			if _, err := query.MethodRequest(url); !errors.Is(err, xtandard.ErrInvalidRequest) {
				t.Errorf("MethodRequest(%q) error = %v, want ErrInvalidRequest", url, err)
			}
		})
	}
}
