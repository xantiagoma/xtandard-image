//go:build acceptance

package acceptance

import (
	"bytes"
	"encoding/json"
	"image/png"
	"testing"
)

// TestDesignerFiles: designer documents (WP-107) through every source
// kind. The container formats are unwrapped to the image they carry, which
// X-Xi-Container-Source names (the acceptance app has debug headers on) and
// /info reports as "container"; GIMP XCF is flattened from its layers by
// ImageMagick's XCF coder, which the base image's policy.xml must allow.
func TestDesignerFiles(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	for _, tc := range []struct {
		path, container string
		w, h            int
	}{
		{"xcf/layers.xcf", "", 160, 120},
		{"xcf/gray.xcf", "", 160, 120},
		{"ora/layers.ora", "ora; entry=mergedimage.png; content=composite", 96, 72},
		{"kra/layers.kra", "kra; entry=mergedimage.png; content=composite", 96, 72},
		{"procreate/thumbnail.procreate", "procreate; entry=QuickLook/Thumbnail.png; content=thumbnail", 160, 120},
		{"sketch/preview.sketch", "sketch; entry=previews/preview.png; content=preview", 192, 144},
		{"pdn/thumbnail.pdn", "pdn; entry=header thumbnail; content=thumbnail", 128, 96},
		{"svgz/logo.svgz", "svgz; entry=gzip; content=document", 0, 0},
	} {
		for _, src := range []string{"local:///", "http://fixtures/", "s3://fixtures/"} {
			t.Run(tc.path+" "+src, func(t *testing.T) {
				t.Parallel()
				r := get(t, app+"/unsafe/plain/"+src+tc.path+"@png", nil)
				if fellBack(r) {
					t.Fatalf("answered with the fallback image")
				}
				r.wantStatus(t, 200)
				if got := r.Header.Get("X-Xi-Container-Source"); got != tc.container {
					t.Errorf("X-Xi-Container-Source %q, want %q", got, tc.container)
				}
				c, err := png.DecodeConfig(bytes.NewReader(r.Body))
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if tc.w > 0 && (c.Width != tc.w || c.Height != tc.h) {
					t.Errorf("%dx%d, want %dx%d", c.Width, c.Height, tc.w, tc.h)
				}
			})
		}
		t.Run(tc.path+" info", func(t *testing.T) {
			t.Parallel()
			r := get(t, app+"/info/unsafe/plain/local:///"+tc.path, nil)
			r.wantStatus(t, 200)
			var info struct {
				Container *struct {
					Format  string `json:"format"`
					Entry   string `json:"entry"`
					Content string `json:"content"`
				} `json:"container"`
			}
			if err := json.Unmarshal(r.Body, &info); err != nil {
				t.Fatal(err)
			}
			switch c := info.Container; {
			case tc.container == "" && c != nil:
				t.Errorf("container %+v for %s", *c, tc.path)
			case tc.container != "" && (c == nil || c.Format+"; entry="+c.Entry+"; content="+c.Content != tc.container):
				t.Errorf("info %s, want container %q", r.Body, tc.container)
			}
		})
	}
}
