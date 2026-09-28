//go:build acceptance

package acceptance

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
	"time"
)

// epsDisabled is the public 422 message of an EPS without Ghostscript.
const epsDisabled = "EPS needs the gpl image with XI_FORMATS_EPS_ENABLED=true"

// fellBack reports the fallback image: the acceptance app answers a
// failed request with XI_FALLBACK_SOURCE (200, X-Xi-Fallback: 1).
func fellBack(r *response) bool {
	return r.Status == 200 && r.Header.Get("X-Xi-Fallback") == "1"
}

// wantFailure checks that a request failed with want, or was answered
// with the fallback image, which hides the status.
func wantFailure(t *testing.T, r *response, want int) {
	t.Helper()
	if !fellBack(r) {
		r.wantStatus(t, want)
	}
}

// TestEPS: EPS/PostScript input (WP-99). scripts/acceptance.sh sets
// XI_ACC_EPS=true, and XI_FORMATS_EPS_ENABLED on the app, when the image
// has Ghostscript (the gpl flavour): pages render through the sandboxed gs
// and hostile documents (served by the origin at /hostile/) fail with 422
// or 504 while the service keeps serving. Other images answer 422 with the
// message naming the switch, and decode a DOS EPS from its TIFF preview.
func TestEPS(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	enabled := env(t, "XI_ACC_EPS") == "true"
	pngSize := func(t *testing.T, r *response) (int, int) {
		t.Helper()
		r.wantStatus(t, 200)
		c, err := png.DecodeConfig(bytes.NewReader(r.Body))
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return c.Width, c.Height
	}
	preview := get(t, app+"/unsafe/plain/local:///eps/preview.eps@png", nil)
	if w, h := pngSize(t, preview); w != 96 || h != 72 {
		t.Errorf("preview.eps: %dx%d, want 96x72", w, h)
	}
	if !enabled {
		r := get(t, app+"/unsafe/plain/local:///eps/simple.eps@webp", nil)
		wantFailure(t, r, 422)
		if !fellBack(r) && !strings.Contains(string(r.Body), epsDisabled) {
			t.Errorf("422 body %q, want %q", r.Body, epsDisabled)
		}
		t.Log("no Ghostscript (slim/full, or gpl with EPS off): 422 with the message")
		return
	}

	r := get(t, app+"/unsafe/plain/local:///eps/simple.eps@webp", nil)
	r.wantStatus(t, 200)
	if ct := r.Header.Get("Content-Type"); ct != "image/webp" {
		t.Errorf("Content-Type %q, want image/webp", ct)
	}
	if w, h := pngSize(t, get(t, app+"/unsafe/rs:fit:400:400/plain/http://fixtures/eps/simple.eps@png", nil)); w != 400 || h != 200 {
		t.Errorf("simple.eps at 400: %dx%d, want 400x200", w, h)
	}
	if w, h := pngSize(t, get(t, app+"/unsafe/plain/s3://fixtures/eps/illustrator.ai@png", nil)); w != 160 || h != 120 {
		t.Errorf("illustrator.ai: %dx%d, want 160x120", w, h)
	}
	if w, h := pngSize(t, get(t, app+"/unsafe/pg:2/plain/local:///eps/pages.ps@png", nil)); w != 200 || h != 100 {
		t.Errorf("pages.ps page 3: %dx%d, want 200x100", w, h)
	}
	get(t, app+"/info/unsafe/plain/local:///eps/pages.ps", nil).wantStatus(t, 200)

	for _, tc := range []struct {
		doc  string
		want int
	}{
		{"readfile.eps", 422},
		{"pipe.eps", 422},
		{"recursion.eps", 422},
		{"memory.eps", 422}, // or 504 when the timeout comes first
		{"loop.eps", 504},
		{"pagesize.eps", 200},
	} {
		// One at a time: XI_FORMATS_EPS_MAX_PROCS is 2, and parallel renders
		// beyond it are a 429 by design.
		start := time.Now()
		r := get(t, app+"/unsafe/plain/http://fixtures/hostile/"+tc.doc+"@png", nil)
		switch {
		case tc.want == 200:
			r.wantStatus(t, 200)
			if fellBack(r) {
				t.Errorf("%s: fallback image, want the render", tc.doc)
			}
		case tc.doc == "memory.eps" && r.Status == 504:
		default:
			wantFailure(t, r, tc.want)
		}
		for _, leak := range []string{"root:", "invalidfileaccess", "ghostscript", "/etc/"} {
			if r.Status != 200 && strings.Contains(string(r.Body), leak) {
				t.Errorf("%s: body leaks %q: %s", tc.doc, leak, truncate(r.Body))
			}
		}
		t.Logf("%s: %d in %s", tc.doc, r.Status, time.Since(start).Round(time.Millisecond))
	}
	// Still serving.
	get(t, app+"/unsafe/plain/local:///eps/simple.eps@png", nil).wantStatus(t, 200)
}
