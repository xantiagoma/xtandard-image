package server

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

var updateThumborMeta = flag.Bool("update", false, "rewrite testdata/thumbor_meta/*.json")

// thumborMetaRequest is what the thumbor dialect produces for
// /unsafe/meta/...: an info request with the thumbor schema and the
// processing pipeline.
func thumborMetaRequest(p *transform.Pipeline) func(*transform.Request) {
	return func(r *transform.Request) {
		r.Kind = transform.KindInfo
		r.Pipelines = []transform.Pipeline{*p}
		r.Info = &transform.InfoOptions{Schema: transform.InfoSchemaThumbor, Size: true, Format: true, Dimensions: true}
	}
}

// thumborFill is thumbor's default WxH: fill the box (crop), upscaling.
func thumborFill(w, h int) transform.Resize {
	return transform.Resize{Type: transform.ResizeFill, Width: transform.Dim{Px: w}, Height: transform.Dim{Px: h}, Enlarge: true}
}

// thumborCrop is thumbor's manual crop AxB:CxD.
func thumborCrop(l, t, r, b int) *transform.Crop {
	return &transform.Crop{
		Width: transform.Dim{Px: r - l}, Height: transform.Dim{Px: b - t},
		Gravity: transform.Gravity{Type: transform.GravityNorthWest, XOffset: float64(l), YOffset: float64(t)},
	}
}

func focal(x1, y1, x2, y2 int) transform.Gravity {
	g, _ := transform.FocusFromPixels(x1, y1, x2, y2)
	return g
}

func jpegInfo(w, h, orientation int) *SourceInfo {
	return &SourceInfo{
		Meta:   SourceMeta{Format: transform.FormatJPEG, Width: w, Height: h, Frames: 1, Orientation: orientation},
		Format: "jpeg", MIMEType: "image/jpeg", Pages: 1,
	}
}

// TestThumborMeta renders the thumbor meta response for thumbor-style
// requests and compares it with testdata/thumbor_meta/<name>.json (key
// order included). Run with -update to rewrite the goldens.
func TestThumborMeta(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		p    transform.Pipeline
		si   *SourceInfo
	}{
		// /unsafe/meta/300x200/a.jpg on 1000x800: resize to cover, crop the
		// centre; the default focal point is the centre alignment.
		{"fill", transform.Pipeline{Resize: thumborFill(300, 200)}, jpegInfo(1000, 800, 1)},
		// Stored 4000x3000 with EXIF orientation 6: displayed 3000x4000.
		{"fill-exif6", transform.Pipeline{Resize: thumborFill(300, 200)}, jpegInfo(4000, 3000, 6)},
		// /unsafe/meta/fit-in/300x300/a.jpg: resize only.
		{"fit-in", transform.Pipeline{Resize: transform.Resize{Type: transform.ResizeFit, Width: transform.Dim{Px: 300}, Height: transform.Dim{Px: 300}}}, jpegInfo(1000, 800, 1)},
		// /unsafe/meta/400x100/filters:focal(146x206:279x360)/a.jpg: the
		// crop follows the explicit focal point.
		{"focal", transform.Pipeline{Resize: thumborFill(400, 100), Gravity: focal(146, 206, 279, 360)}, jpegInfo(1000, 800, 1)},
		// The same with EXIF 6: the focal rectangle is in displayed pixels.
		{"focal-exif6", transform.Pipeline{Resize: thumborFill(400, 100), Gravity: focal(146, 206, 279, 360)}, jpegInfo(800, 1000, 6)},
		// /unsafe/meta/100x50:900x750/400x100/filters:focal(...)/a.jpg:
		// manual crop, then the focal point relative to it.
		{"crop-focal", transform.Pipeline{Crop: thumborCrop(100, 50, 900, 750), Resize: thumborFill(400, 100), Gravity: focal(146, 206, 279, 360)}, jpegInfo(1000, 800, 1)},
		// The same stored rotated (EXIF 6 and 8): the manual crop and the
		// focal point are in displayed pixels, so the body is the same.
		{"crop-focal-exif6", transform.Pipeline{Crop: thumborCrop(100, 50, 900, 750), Resize: thumborFill(400, 100), Gravity: focal(146, 206, 279, 360)}, jpegInfo(800, 1000, 6)},
		{"crop-focal-exif8", transform.Pipeline{Crop: thumborCrop(100, 50, 900, 750), Resize: thumborFill(400, 100), Gravity: focal(146, 206, 279, 360)}, jpegInfo(800, 1000, 8)},
		// A focal point (a single pixel) outside the manual crop is
		// dropped: alignment.
		{"crop-focal-outside", transform.Pipeline{Crop: thumborCrop(300, 300, 900, 750), Resize: thumborFill(400, 100), Gravity: focal(120, 250, 120, 250)}, jpegInfo(1000, 800, 1)},
		// /unsafe/meta/-300x-200/right/top/a.jpg: flips and alignment.
		{"flip-align", transform.Pipeline{Resize: thumborFill(300, 200), Flip: transform.Flip{H: true, V: true}, Gravity: transform.Gravity{Type: transform.GravityNorthEast}}, jpegInfo(1000, 800, 1)},
		// A shrink-on-load JPEG with a manual crop in source pixels.
		{"crop-shrink-on-load", transform.Pipeline{Crop: thumborCrop(400, 300, 3600, 2700), Resize: thumborFill(200, 150)}, jpegInfo(4000, 3000, 1)},
		// An animated GIF reports its frames.
		{"animated", transform.Pipeline{Resize: thumborFill(50, 50)}, &SourceInfo{
			Meta:   SourceMeta{Format: transform.FormatGIF, Width: 400, Height: 300, Frames: 6, IsAnimated: true, HasAlpha: true},
			Format: "gif", MIMEType: "image/gif", Pages: 6,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newInfoHarness(t, &fakeInspector{info: tc.si}, func(h *harness) {
				h.router.mutate = thumborMetaRequest(&tc.p)
				h.cfg.Info.PlanLimits = plan.Limits{MaxSrcPixels: 50_000_000, MaxResultDimension: 8192, MaxAnimationFrames: 256}
			})
			rec := h.do(t, http.MethodGet, infoPath, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
			var got bytes.Buffer
			if err := json.Indent(&got, rec.Body.Bytes(), "", "  "); err != nil {
				t.Fatalf("invalid JSON %q: %v", rec.Body.String(), err)
			}
			got.WriteByte('\n')
			path := filepath.Join("testdata", "thumbor_meta", tc.name+".json")
			if *updateThumborMeta {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got.Bytes(), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden: %v (run with -update)", err)
			}
			if diff := cmp.Diff(string(want), got.String()); diff != "" {
				t.Errorf("body (-golden +got):\n%s", diff)
			}
			h.checkReleased(t)
		})
	}
}

// TestThumborMetaResponse: the thumbor meta response follows the /info
// caching rules (ETag from the canonical key, 304, Cache-Control), and its
// ETag differs from the imgproxy schema's for the same source.
func TestThumborMetaResponse(t *testing.T) {
	t.Parallel()
	p := transform.Pipeline{Resize: thumborFill(300, 200)}
	h := newInfoHarness(t, &fakeInspector{info: jpegInfo(1000, 800, 1)}, func(h *harness) {
		h.router.mutate = thumborMetaRequest(&p)
		h.cfg.Info.CacheTTL = time.Hour
	})
	rec := h.do(t, http.MethodGet, infoPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	etag := rec.Header().Get("ETag")
	if cc := rec.Header().Get("Cache-Control"); cc != "max-age=3600, public" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if rec := h.do(t, http.MethodGet, infoPath, http.Header{"If-None-Match": {etag}}); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("If-None-Match: status %d, %d bytes", rec.Code, rec.Body.Len())
	}
	imgproxy := newInfoHarness(t, &fakeInspector{info: jpegInfo(1000, 800, 1)}, func(h *harness) {
		h.router.mutate = func(r *transform.Request) {
			thumborMetaRequest(&p)(r)
			r.Info.Schema = transform.InfoSchemaImgproxy
		}
	})
	other := imgproxy.do(t, http.MethodGet, infoPath, nil)
	if other.Header().Get("ETag") == etag {
		t.Error("thumbor and imgproxy schemas share an ETag")
	}
	if !json.Valid(other.Body.Bytes()) || bytes.Contains(other.Body.Bytes(), []byte(`"thumbor"`)) {
		t.Errorf("imgproxy schema body = %s", other.Body.String())
	}
	h.checkReleased(t)
}

// TestThumborMetaLimits: a plan over the limits fails like processing.
func TestThumborMetaLimits(t *testing.T) {
	t.Parallel()
	h := newInfoHarness(t, &fakeInspector{info: jpegInfo(1000, 800, 1)}, func(h *harness) {
		h.router.mutate = thumborMetaRequest(&transform.Pipeline{Resize: thumborFill(300, 200)})
		h.cfg.Info.PlanLimits = plan.Limits{MemoryBudget: 1}
	})
	rec := h.do(t, http.MethodGet, infoPath, nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
	h.checkReleased(t)
}
