package server

import (
	"bytes"
	"net/http"
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// watermarks sets the request's watermarks: per-request images for the
// given source strings (pipeline 0) and one text watermark.
func watermarks(srcs ...string) func(*transform.Request) {
	return func(r *transform.Request) {
		pl := &r.Pipelines[0]
		for _, s := range srcs {
			pl.Watermarks = append(pl.Watermarks, transform.Watermark{Opacity: 1, Source: &transform.SourceSpec{Raw: s}})
		}
		pl.Watermarks = append(pl.Watermarks, transform.Watermark{Opacity: 1, Text: "text"})
	}
}

// TestWatermarkFetched: a per-request watermark image is resolved against
// the allowlist, fetched through the fetcher like the source, and handed
// to the processor with its position; text watermarks fetch nothing. Every
// buffer is released.
func TestWatermarkFetched(t *testing.T) {
	t.Parallel()
	mark := []byte("\x89PNG watermark")
	h := newHarness(t, func(h *harness) {
		h.router.mutate = func(r *transform.Request) {
			r.Pipelines = append(r.Pipelines, transform.Pipeline{Watermarks: []transform.Watermark{
				{Opacity: 1, Source: &transform.SourceSpec{Raw: "http://origin/second.png"}},
			}})
			watermarks("http://origin/mark.png")(r)
		}
		h.f.other = &fakeReply{data: mark}
	})
	rec := h.do(t, http.MethodGet, okPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	got := h.f.fetched()
	slices.Sort(got)
	if want := []string{"http://origin/a.jpg", "http://origin/mark.png", "http://origin/second.png"}; !slices.Equal(got, want) {
		t.Errorf("fetched %v, want %v", got, want)
	}
	h.p.mu.Lock()
	marks, data := h.p.marks, h.p.markData
	h.p.mu.Unlock()
	if len(marks) != 2 || marks[0].Pipeline != 0 || marks[0].Index != 0 || marks[1].Pipeline != 1 || marks[1].Index != 0 {
		t.Errorf("watermarks = %+v", marks)
	}
	for i, d := range data {
		if !bytes.Equal(d, mark) {
			t.Errorf("watermark %d bytes = %q", i, d)
		}
	}
	h.checkReleased(t)
}

// TestWatermarkFetchErrors: a watermark source outside the allowlist is
// 403 before anything is fetched for it, and a failed watermark fetch
// fails the request; neither reaches the processor.
func TestWatermarkFetchErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*harness)
		status int
	}{
		{name: "not allowed", status: http.StatusForbidden, mutate: func(h *harness) {
			h.res.deny = map[string]bool{"http://evil/mark.png": true}
			h.router.mutate = watermarks("http://origin/ok.png", "http://evil/mark.png")
		}},
		{name: "not found", status: http.StatusNotFound, mutate: func(h *harness) {
			h.router.mutate = watermarks("http://origin/missing.png")
			h.f.other = &fakeReply{err: errs.New(errs.KindNotFound, "fetch: not found")}
		}},
		{name: "too large", status: http.StatusUnprocessableEntity, mutate: func(h *harness) {
			h.router.mutate = watermarks("http://origin/big.png")
			h.f.other = &fakeReply{err: errs.New(errs.KindSourceTooLarge, "fetch: too large")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, tc.mutate)
			rec := h.do(t, http.MethodGet, okPath, nil)
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d (%q)", rec.Code, tc.status, rec.Body.String())
			}
			if h.p.calls != 0 {
				t.Error("processor called")
			}
			for _, p := range h.f.fetched() {
				if p == "http://evil/mark.png" {
					t.Error("disallowed watermark fetched")
				}
			}
			h.checkReleased(t)
		})
	}
}

// TestWatermarkSVGSanitised: an SVG watermark reaches the processor
// sanitised (the script is gone).
func TestWatermarkSVGSanitised(t *testing.T) {
	t.Parallel()
	svgMark := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script><rect width="10" height="10"/></svg>`)
	h := newHarness(t, func(h *harness) {
		h.router.mutate = watermarks("http://origin/mark.svg")
		h.f.other = &fakeReply{data: svgMark}
	})
	rec := h.do(t, http.MethodGet, okPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	h.p.mu.Lock()
	data := h.p.markData
	h.p.mu.Unlock()
	if len(data) != 1 || bytes.Contains(data[0], []byte("script")) || !bytes.Contains(data[0], []byte("<rect")) {
		t.Errorf("watermark = %q", data)
	}
	h.checkReleased(t)
}
