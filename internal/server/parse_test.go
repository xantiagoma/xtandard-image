package server

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

func TestParseImagePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path    string
		want    imageRequest
		wantErr errs.Kind
	}{
		{path: "/insecure/rs:fit:300:200/plain/http://origin/a.jpg", want: imageRequest{Width: 300, Height: 200, Source: "http://origin/a.jpg"}},
		{path: "/unsafe/rs:fit:0:50/plain/https://origin:8443/x/y.png?v=1", want: imageRequest{Height: 50, Source: "https://origin:8443/x/y.png?v=1"}},
		{path: "/insecure/w:64/plain/http://origin/a.jpg", want: imageRequest{Width: 64, Source: "http://origin/a.jpg"}},
		{path: "/insecure/w:16384/plain/http://o/a", want: imageRequest{Width: 16384, Source: "http://o/a"}},
		{path: "insecure/w:1/plain/http://o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure", wantErr: errs.KindInvalidURL},
		{path: "/insecure/w:1", wantErr: errs.KindInvalidURL},
		{path: "/sig/w:1/plain/http://o/a", wantErr: errs.KindUnauthorized},
		{path: "/insecure/w:16385/plain/http://o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure/w:-1/plain/http://o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure/w:abc/plain/http://o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure/rs:fit:10/plain/http://o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure/rs:fill:10:10/plain/http://o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure/w:1/aHR0cDovL28vYQ", wantErr: errs.KindInvalidURL},
		{path: "/insecure/w:1/plain/ftp://o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure/w:1/plain/http:/o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure/w:1/plain/http://user@o/a", wantErr: errs.KindInvalidURL},
		{path: "/insecure/w:1/plain/file:///etc/passwd", wantErr: errs.KindInvalidURL},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			got, err := parseImagePath(tc.path)
			if tc.wantErr != errs.KindUnknown {
				if k := errs.KindOf(err); k != tc.wantErr {
					t.Fatalf("error kind = %v (%v), want %v", k, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("(-want +got):\n%s", diff)
			}
		})
	}
}

func FuzzParseImagePath(f *testing.F) {
	for _, s := range []string{
		"/insecure/rs:fit:300:200/plain/http://origin/a.jpg",
		"/unsafe/w:64/plain/https://o/a?b=c",
		"/insecure/rs:fit::/plain/",
		"//w:1/plain/http://",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		req, err := parseImagePath(p)
		if err != nil {
			if k := errs.KindOf(err); k != errs.KindInvalidURL && k != errs.KindUnauthorized {
				t.Fatalf("unexpected error kind %v for %q", k, p)
			}
			return
		}
		if req.Width < 0 || req.Width > MaxDimension || req.Height < 0 || req.Height > MaxDimension {
			t.Fatalf("dimension out of range: %+v", req)
		}
		if !strings.HasPrefix(req.Source, "http://") && !strings.HasPrefix(req.Source, "https://") {
			t.Fatalf("non-http source %q from %q", req.Source, p)
		}
	})
}
