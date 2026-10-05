package server

import (
	"bytes"
	"net/http"
	"slices"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// overlayOp is a video op with an image overlay per source and a text
// overlay and a timecode between them.
func overlayOp(srcs ...string) *transform.VideoOp {
	op := &transform.VideoOp{Width: 640}
	for i, s := range srcs {
		op.Overlays = append(op.Overlays, transform.VideoOverlay{
			Opacity: 1, Source: &transform.SourceSpec{Raw: s},
		})
		if i == 0 {
			op.Overlays = append(op.Overlays,
				transform.VideoOverlay{Opacity: 1, Text: "text"},
				transform.VideoOverlay{Opacity: 1, Timecode: transform.VideoTimecodeHMS})
		}
	}
	return op
}

// TestVideoOverlaysFetched: the images of image overlays are resolved and
// fetched like watermark images and handed to the transformer at their
// overlay's index; text and timecodes fetch nothing; everything is
// released. A HEAD fetches no overlay (nor does a cache hit, which never
// gets this far: TestVideoResultCache).
func TestVideoOverlaysFetched(t *testing.T) {
	t.Parallel()
	mark := []byte("\x89PNG overlay")
	h, fv := newVideoHarness(t, false, func(h *harness) {
		h.router.mutate = videoRequest(overlayOp("http://origin/logo.png", "http://origin/second.png"))
		h.f.other = &fakeReply{data: mark}
	})
	if rec := h.do(t, http.MethodHead, videoPath, nil); rec.Code != http.StatusOK {
		t.Fatalf("HEAD: %d %s", rec.Code, rec.Body.String())
	}
	if got := h.f.fetched(); slices.Contains(got, "http://origin/logo.png") {
		t.Errorf("HEAD fetched the overlays: %v", got)
	}
	rec := h.do(t, http.MethodGet, videoPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	got := h.f.fetched()
	slices.Sort(got)
	got = slices.Compact(got)
	if want := []string{"http://origin/a.jpg", "http://origin/logo.png", "http://origin/second.png"}; !slices.Equal(got, want) {
		t.Errorf("fetched %v, want %v", got, want)
	}
	fv.mu.Lock()
	marks := fv.marks[0]
	fv.mu.Unlock()
	if len(marks) != 4 || !bytes.Equal(marks[0], mark) || marks[1] != nil || marks[2] != nil || !bytes.Equal(marks[3], mark) {
		t.Errorf("overlay images %q", marks)
	}
	h.checkReleased(t)
	fv.checkBodies(t)
}

// TestVideoOverlayFetchErrors: an overlay source outside the allowlist is
// 403 and a failed overlay fetch fails the request, before any transcode.
func TestVideoOverlayFetchErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*harness)
		status int
	}{
		{name: "not allowed", status: http.StatusForbidden, mutate: func(h *harness) {
			h.res.deny = map[string]bool{"http://evil/logo.png": true}
			h.router.mutate = videoRequest(overlayOp("http://origin/ok.png", "http://evil/logo.png"))
		}},
		{name: "not found", status: http.StatusNotFound, mutate: func(h *harness) {
			h.router.mutate = videoRequest(overlayOp("http://origin/missing.png"))
			h.f.other = &fakeReply{err: errs.New(errs.KindNotFound, "fetch: not found")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h, fv := newVideoHarness(t, false, tc.mutate)
			rec := h.do(t, http.MethodGet, videoPath, nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			if fv.calls() != 0 {
				t.Error("transcoded despite the failed overlay")
			}
			h.checkReleased(t)
		})
	}
}
