package server

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
)

// refBuf is a reference-counted body (cache.Buffer).
type refBuf struct {
	b    []byte
	refs atomic.Int64
}

func (r *refBuf) Bytes() []byte { return r.b }
func (r *refBuf) Retain()       { r.refs.Add(1) }
func (r *refBuf) Release()      { r.refs.Add(-1) }

// fakeVideo is a VideoTransformer.
type fakeVideo struct {
	out        video.TranscodeOutput
	resolveErr error
	err        error
	body       []byte
	fn         func(ctx context.Context) error

	mu     sync.Mutex
	keys   []string
	ops    []transform.VideoOp
	src    [][]byte
	bodies []*refBuf
}

func (f *fakeVideo) Resolve(*transform.VideoOp) (video.TranscodeOutput, error) {
	return f.out, f.resolveErr
}

func (f *fakeVideo) Transform(ctx context.Context, key string, src fetch.Buffer, op *transform.VideoOp) (*VideoResult, error) {
	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.ops = append(f.ops, *op)
	f.src = append(f.src, bytes.Clone(src.Bytes()))
	f.mu.Unlock()
	if f.fn != nil {
		if err := f.fn(ctx); err != nil {
			return nil, err
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	b := &refBuf{b: f.body}
	b.refs.Store(1)
	f.mu.Lock()
	f.bodies = append(f.bodies, b)
	f.mu.Unlock()
	return &VideoResult{Body: b, Output: f.out, Width: 640, Height: 360}, nil
}

func (f *fakeVideo) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.keys)
}

// checkBodies asserts every transcoded body was released.
func (f *fakeVideo) checkBodies(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, b := range f.bodies {
		if n := b.refs.Load(); n != 0 {
			t.Errorf("body %d holds %d references", i, n)
		}
	}
}

// videoFile is a fake MP4 of 64 distinct bytes.
var videoFile = func() []byte {
	b := make([]byte, 64)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}()

var mp4Output = video.TranscodeOutput{
	Container: transform.VideoContainerMP4, Muxer: "mp4", Video: video.CodecAV1, Audio: video.AudioAAC,
	ContentType: "video/mp4", Ext: ".mp4",
}

func videoRequest(op *transform.VideoOp) func(*transform.Request) {
	return func(r *transform.Request) {
		r.Kind = transform.KindVideo
		o := *op
		r.Video = &o
	}
}

func newVideoHarness(tb testing.TB, cached bool, mutate func(*harness)) (*harness, *fakeVideo) {
	tb.Helper()
	fv := &fakeVideo{out: mp4Output, body: videoFile}
	m := func(h *harness) {
		h.video = fv
		h.router.mutate = videoRequest(&transform.VideoOp{Width: 640, Start: time.Second})
		h.f.data = []byte("source video")
		h.cfg.Video = VideoConfig{Budget: time.Minute}
		if mutate != nil {
			mutate(h)
		}
	}
	if cached {
		h, _ := newCachedHarness(tb, m)
		return h, fv
	}
	return newHarness(tb, m), fv
}

const videoPath = "/video/plain/http://origin/a.jpg"

func TestVideoTransform(t *testing.T) {
	t.Parallel()
	h, fv := newVideoHarness(t, false, func(h *harness) { h.cfg.DebugHeaders = true })
	rec := h.do(t, http.MethodGet, videoPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	want := map[string]string{
		"Content-Type":        "video/mp4",
		"Content-Length":      "64",
		"Accept-Ranges":       "bytes",
		"Content-Disposition": `inline; filename="a.mp4"`,
		"X-Xi-Video-Codec":    "av1+aac",
		"X-Xi-Result-Width":   "640",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if rec.Header().Get("ETag") == "" || rec.Header().Get("Cache-Control") == "" {
		t.Errorf("caching headers missing: %v", rec.Header())
	}
	if !bytes.Equal(rec.Body.Bytes(), videoFile) {
		t.Error("body differs")
	}
	if fv.calls() != 1 || fv.ops[0].Width != 640 || string(fv.src[0]) != "source video" {
		t.Fatalf("transform calls %d %+v", fv.calls(), fv.ops)
	}
	if h.a.acquired.Load() != 0 || h.pr.calls.Load() != 0 || h.p.calls != 0 {
		t.Error("a video transformation went through the image admission, probe or processor")
	}
	h.checkReleased(t)
	fv.checkBodies(t)
}

// TestVideoRange: byte ranges are answered from the file, exactly.
func TestVideoRange(t *testing.T) {
	t.Parallel()
	h, _ := newVideoHarness(t, false, nil)
	full := h.do(t, http.MethodGet, videoPath, nil)
	etag := full.Header().Get("ETag")
	n := len(videoFile)
	tests := []struct {
		name    string
		hdr     http.Header
		status  int
		body    []byte
		cr      string
		partial bool
	}{
		{"first bytes", http.Header{"Range": {"bytes=0-9"}}, http.StatusPartialContent, videoFile[:10], "bytes 0-9/64", true},
		{"middle", http.Header{"Range": {"bytes=10-19"}}, http.StatusPartialContent, videoFile[10:20], "bytes 10-19/64", true},
		{"open end", http.Header{"Range": {"bytes=60-"}}, http.StatusPartialContent, videoFile[60:], "bytes 60-63/64", true},
		{"suffix", http.Header{"Range": {"bytes=-4"}}, http.StatusPartialContent, videoFile[n-4:], "bytes 60-63/64", true},
		{"past the end clamps", http.Header{"Range": {"bytes=50-1000"}}, http.StatusPartialContent, videoFile[50:], "bytes 50-63/64", true},
		{"unsatisfiable", http.Header{"Range": {"bytes=64-70"}}, http.StatusRequestedRangeNotSatisfiable, nil, "bytes */64", false},
		{"if-range matches", http.Header{"Range": {"bytes=0-1"}, "If-Range": {etag}}, http.StatusPartialContent, videoFile[:2], "bytes 0-1/64", true},
		{"if-range stale", http.Header{"Range": {"bytes=0-1"}, "If-Range": {`"xi1-stale"`}}, http.StatusOK, videoFile, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := h.do(t, http.MethodGet, videoPath, tc.hdr)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Range"); got != tc.cr {
				t.Errorf("Content-Range %q, want %q", got, tc.cr)
			}
			if tc.body != nil && !bytes.Equal(rec.Body.Bytes(), tc.body) {
				t.Errorf("body %v, want %v", rec.Body.Bytes(), tc.body)
			}
			if tc.body != nil {
				if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(tc.body)) {
					t.Errorf("Content-Length %q, want %d", got, len(tc.body))
				}
			}
		})
	}

	t.Run("sequential ranges rebuild the file", func(t *testing.T) {
		t.Parallel()
		var got []byte
		for off := 0; off < n; off += 13 {
			rec := h.do(t, http.MethodGet, videoPath, http.Header{"Range": {"bytes=" + strconv.Itoa(off) + "-" + strconv.Itoa(off+12)}})
			if rec.Code != http.StatusPartialContent {
				t.Fatalf("range at %d: status %d", off, rec.Code)
			}
			got = append(got, rec.Body.Bytes()...)
		}
		if !bytes.Equal(got, videoFile) {
			t.Error("the ranges do not rebuild the file")
		}
	})

	t.Run("several ranges are multipart", func(t *testing.T) {
		t.Parallel()
		rec := h.do(t, http.MethodGet, videoPath, http.Header{"Range": {"bytes=0-1,10-12"}})
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("status %d", rec.Code)
		}
		mt, params, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
		if err != nil || mt != "multipart/byteranges" {
			t.Fatalf("Content-Type %q", rec.Header().Get("Content-Type"))
		}
		mr := multipart.NewReader(rec.Body, params["boundary"])
		var parts [][]byte
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Header.Get("Content-Type") != "video/mp4" {
				t.Errorf("part type %q", p.Header.Get("Content-Type"))
			}
			b, _ := io.ReadAll(p)
			parts = append(parts, b)
		}
		if d := cmp.Diff([][]byte{videoFile[:2], videoFile[10:13]}, parts); d != "" {
			t.Errorf("parts (-want +got):\n%s", d)
		}
	})
}

// TestVideoConditional: If-None-Match and HEAD are answered after the
// fetch, without transcoding.
func TestVideoConditional(t *testing.T) {
	t.Parallel()
	h, fv := newVideoHarness(t, false, nil)
	etag := h.do(t, http.MethodGet, videoPath, nil).Header().Get("ETag")
	rec := h.do(t, http.MethodGet, videoPath, http.Header{"If-None-Match": {etag}})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Fatalf("conditional: status %d", rec.Code)
	}
	rec = h.do(t, http.MethodHead, videoPath, nil)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("ETag") != etag ||
		rec.Header().Get("Accept-Ranges") != "bytes" || rec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("HEAD: %d %v", rec.Code, rec.Header())
	}
	if fv.calls() != 1 {
		t.Errorf("%d transcodes, want 1 (the first GET)", fv.calls())
	}
	h.checkReleased(t)
}

// TestVideoResultCache: a hit replays the file, its headers and ranges
// without fetching or transcoding.
func TestVideoResultCache(t *testing.T) {
	t.Parallel()
	h, fv := newVideoHarness(t, true, nil)
	store, ok := h.cache.(interface{ Wait() })
	if !ok {
		t.Fatal("the result cache is not asynchronous")
	}
	miss := h.do(t, http.MethodGet, videoPath, nil)
	if miss.Code != http.StatusOK || miss.Header().Get(hdrXiCache) != cacheMiss {
		t.Fatalf("miss: %d %q", miss.Code, miss.Header().Get(hdrXiCache))
	}
	store.Wait()
	hit := h.do(t, http.MethodGet, videoPath, nil)
	if hit.Code != http.StatusOK || hit.Header().Get(hdrXiCache) != cacheHit || !bytes.Equal(hit.Body.Bytes(), videoFile) {
		t.Fatalf("hit: %d %q", hit.Code, hit.Header().Get(hdrXiCache))
	}
	if d := cmp.Diff(comparableHeader(miss.Header()), comparableHeader(hit.Header())); d != "" {
		t.Errorf("hit headers (-miss +hit):\n%s", d)
	}
	rng := h.do(t, http.MethodGet, videoPath, http.Header{"Range": {"bytes=3-5"}})
	if rng.Code != http.StatusPartialContent || !bytes.Equal(rng.Body.Bytes(), videoFile[3:6]) {
		t.Fatalf("range on a hit: %d", rng.Code)
	}
	head := h.do(t, http.MethodHead, videoPath, nil)
	if head.Code != http.StatusOK || head.Header().Get("Content-Length") != "64" {
		t.Fatalf("HEAD on a hit: %d %q", head.Code, head.Header().Get("Content-Length"))
	}
	if fv.calls() != 1 || h.f.calls.Load() != 1 {
		t.Errorf("%d transcodes and %d fetches, want 1 each", fv.calls(), h.f.calls.Load())
	}
}

// TestVideoKeys: identical requests share the coalescing key; another
// output does not.
func TestVideoKeys(t *testing.T) {
	t.Parallel()
	op := transform.VideoOp{Width: 640}
	h, fv := newVideoHarness(t, false, func(h *harness) {
		h.router.mutate = func(r *transform.Request) { videoRequest(&op)(r) }
	})
	h.do(t, http.MethodGet, videoPath, nil)
	h.do(t, http.MethodGet, videoPath, nil)
	op.Width = 320
	h.do(t, http.MethodGet, videoPath, nil)
	if fv.calls() != 3 || fv.keys[0] != fv.keys[1] || fv.keys[0] == fv.keys[2] || fv.keys[0] == "" {
		t.Fatalf("keys %q", fv.keys)
	}
}

func TestVideoErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(h *harness, fv *fakeVideo)
		status int
		fetch  bool
	}{
		{"unsupported output", func(_ *harness, fv *fakeVideo) {
			fv.resolveErr = errs.New(errs.KindUnsupported, "no hevc")
		}, http.StatusUnprocessableEntity, false},
		{"invalid op", func(h *harness, _ *fakeVideo) {
			h.router.mutate = videoRequest(&transform.VideoOp{Rotate: 45})
		}, http.StatusBadRequest, false},
		{"limit", func(_ *harness, fv *fakeVideo) {
			fv.err = errs.New(errs.KindLimitExceeded, "too long")
		}, http.StatusUnprocessableEntity, true},
		{"overloaded", func(_ *harness, fv *fakeVideo) {
			fv.err = errs.New(errs.KindOverloaded, "queue full")
		}, http.StatusTooManyRequests, true},
		{"image role", func(h *harness, _ *fakeVideo) { h.cfg.Role = RoleImage }, http.StatusNotFound, false},
		{"disabled", func(h *harness, _ *fakeVideo) { h.video = nil }, http.StatusNotFound, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newVideoHarness(t, false, func(h *harness) {
				fv, _ := h.video.(*fakeVideo)
				tc.mutate(h, fv)
			})
			rec := h.do(t, http.MethodGet, videoPath, nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if fetched := h.f.calls.Load() > 0; fetched != tc.fetch {
				t.Errorf("fetched %v, want %v", fetched, tc.fetch)
			}
			h.checkReleased(t)
		})
	}
}

// TestRoleVideo: a video-only instance answers images, info and raw
// requests 404, and serves video transformations and health.
func TestRoleVideo(t *testing.T) {
	t.Parallel()
	for _, kind := range []transform.Kind{transform.KindProcess, transform.KindInfo, transform.KindRaw} {
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, func(h *harness) {
				h.cfg.Role = RoleVideo
				h.video = &fakeVideo{out: mp4Output, body: videoFile}
				h.router.mutate = func(r *transform.Request) {
					r.Kind = kind
					if kind == transform.KindInfo {
						r.Info = &transform.InfoOptions{Size: true}
					}
				}
			})
			if rec := h.do(t, http.MethodGet, okPath, nil); rec.Code != http.StatusNotFound {
				t.Fatalf("status %d, want 404", rec.Code)
			}
			if h.f.calls.Load() != 0 {
				t.Error("a refused request fetched its source")
			}
			if rec := h.do(t, http.MethodGet, "/healthz", nil); rec.Code != http.StatusOK {
				t.Errorf("healthz %d", rec.Code)
			}
		})
	}
	t.Run("video", func(t *testing.T) {
		t.Parallel()
		h, _ := newVideoHarness(t, false, func(h *harness) { h.cfg.Role = RoleVideo })
		if rec := h.do(t, http.MethodGet, videoPath, nil); rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

// TestVideoBudget: a video request outlives XI_SERVER_REQUEST_TIMEOUT,
// bounded by its own budget instead.
func TestVideoBudget(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, fv := newVideoHarness(t, false, func(h *harness) {
			h.cfg.RequestTimeout = time.Second
			h.cfg.Video.Budget = 10 * time.Second
		})
		fv.fn = func(ctx context.Context) error {
			select {
			case <-time.After(5 * time.Second):
				return nil
			case <-ctx.Done():
				return errs.Wrap(errs.KindOf(ctx.Err()), context.Cause(ctx))
			}
		}
		if rec := h.do(t, http.MethodGet, videoPath, nil); rec.Code != http.StatusOK {
			t.Fatalf("5 s transcode: status %d", rec.Code)
		}
		fv.fn = func(ctx context.Context) error {
			<-ctx.Done()
			return errs.Wrap(errs.KindOf(ctx.Err()), context.Cause(ctx))
		}
		if rec := h.do(t, http.MethodGet, videoPath, nil); rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("endless transcode: status %d, want 504", rec.Code)
		}
	})
}
