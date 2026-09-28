//go:build cgo

package app

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// TestMetafiles requests the WMF and EMF fixtures through the service
// (WP-108). With the gpl image's converter (XI_FORMATS_METAFILE_ENABLED
// auto finds it) they become SVG documents rendered at the requested size,
// X-Xi-Container-Source and /info name the metafile; without it they are
// 422 with a message naming the gpl image.
func TestMetafiles(t *testing.T) {
	converter := metafileConverter()
	a := start(t, designerConfig(t, t.TempDir()), hooks{})
	base := "http://" + a.public.String()
	for _, f := range []struct{ path, kind string }{
		{"wmf/shapes.wmf", "wmf"},
		{"emf/shapes.emf", "emf"},
	} {
		t.Run(f.path, func(t *testing.T) {
			if !converter {
				r := get(t, base+"/insecure/plain/"+f.path+"@png")
				if r.status != http.StatusUnprocessableEntity || !strings.Contains(string(r.body), "gpl image") {
					t.Errorf("without the converter: %d %s, want 422 naming the gpl image", r.status, r.body)
				}
				return
			}
			r := get(t, base+"/insecure/rs:fit:100:100/plain/"+f.path+"@png")
			img := decodePNG(t, r)
			want := f.kind + "; entry=xi-metafile2svg; content=converted"
			if got := r.header.Get("X-Xi-Container-Source"); got != want {
				t.Errorf("X-Xi-Container-Source %q, want %q", got, want)
			}
			// The scene is 200×150 (4:3), rendered as vectors at the size asked.
			if b := img.Bounds(); b.Dx() != 100 || b.Dy() != 75 {
				t.Errorf("%dx%d, want 100x75", b.Dx(), b.Dy())
			}
			// The ellipse is orange and the background blue.
			if c := img.At(50, 38); !orange(c) {
				t.Errorf("centre pixel %v, want the orange ellipse", c)
			}
			r = get(t, base+"/info/unsafe/plain/"+f.path)
			if r.status != http.StatusOK {
				t.Fatalf("info: %d %s", r.status, r.body)
			}
			var info struct {
				Format    string `json:"format"`
				Container *struct {
					Format, Entry, Content string
				} `json:"container"`
			}
			if err := json.Unmarshal(r.body, &info); err != nil {
				t.Fatal(err)
			}
			if info.Format != "svg" || info.Container == nil || info.Container.Format != f.kind ||
				info.Container.Content != "converted" {
				t.Errorf("info: %s", r.body)
			}
		})
	}
}

// metafileConverter reports whether the gpl image's WMF/EMF converter is
// installed (XI_FORMATS_METAFILE_ENABLED=auto then turns conversion on).
func metafileConverter() bool {
	_, err := exec.LookPath("xi-metafile2svg")
	return err == nil
}

func orange(c interface{ RGBA() (r, g, b, a uint32) }) bool {
	r, g, b, _ := c.RGBA()
	return r>>8 > 200 && g>>8 > 110 && g>>8 < 190 && b>>8 < 80
}
