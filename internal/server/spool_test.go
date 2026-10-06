package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/cache"
	"github.com/xantiagoma/xtandard-image/internal/cache/disk"
	"github.com/xantiagoma/xtandard-image/internal/cache/memory"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/spool"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// Spool files (WP-121b) seen from the server: file-backed sources and
// results, the 503 of a full spool.

// fileBody writes data to a temporary file and returns it as a
// cache.FileBuffer, with a flag set when it is freed.
func fileBody(tb testing.TB, data []byte) (*cache.FileBuffer, *atomic.Bool) {
	tb.Helper()
	f, err := os.CreateTemp(tb.TempDir(), "spool-*")
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		tb.Fatal(err)
	}
	freed := new(atomic.Bool)
	return cache.NewFileBuffer(f, 0, int64(len(data)), func() { freed.Store(true) }), freed
}

// fileVideo is a VideoTransformer whose results are spool files.
type fileVideo struct {
	fakeVideo
	tb     testing.TB
	bodies []*cache.FileBuffer
	freed  []*atomic.Bool
}

func (f *fileVideo) Transform(ctx context.Context, key string, src fetch.Buffer, op *transform.VideoOp, overlays []fetch.Buffer) (*VideoResult, error) {
	if _, err := f.fakeVideo.Transform(ctx, key, src, op, overlays); err != nil {
		return nil, err
	}
	b, freed := fileBody(f.tb, f.body)
	f.mu.Lock()
	f.bodies, f.freed = append(f.bodies, b), append(f.freed, freed)
	f.mu.Unlock()
	return &VideoResult{Body: b, Output: f.out, Width: 640, Height: 360}, nil
}

// TestVideoSpooledOutput: an output in a spool file is served (whole and
// by ranges) from the file without mapping it, and closed afterwards.
func TestVideoSpooledOutput(t *testing.T) {
	t.Parallel()
	fv := &fileVideo{out: mp4Output, body: videoFile, tb: t}
	h, _ := newVideoHarness(t, false, func(h *harness) { h.video = fv })
	for _, tc := range []struct {
		rng    string
		status int
		body   []byte
	}{
		{"", http.StatusOK, videoFile},
		{"bytes=10-19", http.StatusPartialContent, videoFile[10:20]},
		{"bytes=-4", http.StatusPartialContent, videoFile[60:]},
	} {
		hdr := http.Header{}
		if tc.rng != "" {
			hdr.Set("Range", tc.rng)
		}
		rec := h.do(t, http.MethodGet, videoPath, hdr)
		if rec.Code != tc.status || !bytes.Equal(rec.Body.Bytes(), tc.body) {
			t.Fatalf("range %q: %d %v", tc.rng, rec.Code, rec.Body.Bytes())
		}
		if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(tc.body)) {
			t.Errorf("range %q: Content-Length %s", tc.rng, got)
		}
	}
	for i, b := range fv.bodies {
		if b.Mapped() {
			t.Errorf("output %d was mapped into memory", i)
		}
		if !fv.freed[i].Load() {
			t.Errorf("output %d still open", i)
		}
	}
	h.checkReleased(t)
}

// TestVideoSpooledCache: a spooled output is streamed into the memory and
// disk tiers (the memory tier skips it), and a hit is a file body over
// the disk entry that serves a range without mapping it.
func TestVideoSpooledCache(t *testing.T) {
	t.Parallel()
	mem, err := memory.New(memory.Options{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	dsk, err := disk.New(&disk.Options{Dir: t.TempDir(), MaxBytes: 1 << 20, FileBodyBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	tiers, err := cache.NewTiered(mem, dsk)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewAsync(tiers, cache.AsyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	fv := &fileVideo{out: mp4Output, body: videoFile, tb: t}
	h, _ := newVideoHarness(t, false, func(h *harness) {
		h.video = fv
		h.cache = store
		h.cfg.ResultCache = ResultCacheConfig{TTL: time.Hour, Build: "test"}
	})
	if rec := h.do(t, http.MethodGet, videoPath, nil); rec.Code != http.StatusOK {
		t.Fatalf("miss: %d", rec.Code)
	}
	store.Wait()
	if !fv.freed[0].Load() || fv.bodies[0].Mapped() {
		t.Error("the cache kept or mapped the spooled output")
	}
	if _, n := mem.Usage(); n != 0 {
		t.Errorf("the memory tier holds %d entries", n)
	}
	rec := h.do(t, http.MethodGet, videoPath, http.Header{"Range": {"bytes=0-9"}})
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), videoFile[:10]) {
		t.Fatalf("hit: %d %v", rec.Code, rec.Body.Bytes())
	}
	if fv.calls() != 1 {
		t.Errorf("%d transcodes, want 1", fv.calls())
	}
}

// TestVideoSpoolFull: a full spool budget is a 503 with Retry-After.
func TestVideoSpoolFull(t *testing.T) {
	t.Parallel()
	h, fv := newVideoHarness(t, false, nil)
	fv.err = &errs.Error{
		Kind: errs.KindUnavailable, Msg: "spool: over budget", Err: &spool.BudgetError{Need: 10, Max: 5},
	}
	rec := h.do(t, http.MethodGet, videoPath, nil)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("spool")) {
		t.Errorf("internal text in the body: %s", rec.Body.Bytes())
	}
}

// largeFetcher is a fetcher with FetchLarge whose sources are spool files.
type largeFetcher struct {
	*fakeFetcher
	tb    testing.TB
	large atomic.Int32
	src   []*cache.FileBuffer
	freed []*atomic.Bool
}

func (f *largeFetcher) FetchLarge(_ context.Context, _ source.Key, _ source.FetchOptions) (*fetch.Result, error) {
	f.large.Add(1)
	b, freed := fileBody(f.tb, f.data)
	f.mu.Lock()
	f.src, f.freed = append(f.src, b), append(f.freed, freed)
	f.mu.Unlock()
	return &fetch.Result{Buffer: b, Object: f.obj}, nil
}

// TestVideoFetchLarge: video sources come through FetchLarge; a spooled
// source's hashsum is checked from its file, and the file is released.
func TestVideoFetchLarge(t *testing.T) {
	t.Parallel()
	src := bytes.Repeat([]byte("source video "), 1000)
	sum := sha256.Sum256(src)
	for _, tc := range []struct {
		name   string
		digest string
		status int
	}{
		{"no hashsum", "", http.StatusOK},
		{"matching hashsum", hex.EncodeToString(sum[:]), http.StatusOK},
		{"wrong hashsum", hex.EncodeToString(make([]byte, 32)), errs.Status(errs.KindHashMismatch)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lf := &largeFetcher{fakeFetcher: &fakeFetcher{}, tb: t}
			h, fv := newVideoHarness(t, false, func(h *harness) {
				lf.data = src
				h.fetcher = lf
				if tc.digest != "" {
					next := h.router.mutate
					h.router.mutate = func(r *transform.Request) {
						next(r)
						r.Source.Hashsum = &transform.Hashsum{Algorithm: transform.HashSHA256, Digest: tc.digest}
					}
				}
			})
			rec := h.do(t, http.MethodGet, videoPath, nil)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if lf.large.Load() != 1 || lf.calls.Load() != 0 {
				t.Errorf("FetchLarge %d, Fetch %d", lf.large.Load(), lf.calls.Load())
			}
			if tc.status == http.StatusOK && (fv.calls() != 1 || !bytes.Equal(fv.src[0], src)) {
				t.Errorf("the transcode did not get the spooled source")
			}
			if !lf.freed[0].Load() {
				t.Error("the spooled source was not released")
			}
			if rec.Header().Get("ETag") == "" && tc.status == http.StatusOK {
				t.Error("no ETag")
			}
		})
	}
}

// TestEtagBodyMatchesBytes: the ETag of a spooled source (hashed from its
// file) is the one of the same bytes in memory.
func TestEtagBodyMatchesBytes(t *testing.T) {
	t.Parallel()
	data := bytes.Repeat([]byte("abc"), 10_000)
	fb, _ := fileBody(t, data)
	defer fb.Release()
	obj := &source.Object{}
	if got, want := etagBody("k", transform.FormatUnset, 0, obj, fb, "b"), etag("k", transform.FormatUnset, 0, obj, data, "b"); got != want {
		t.Errorf("etagBody %s, etag %s", got, want)
	}
	if fb.Mapped() {
		t.Error("hashing mapped the file")
	}
	withValidator := &source.Object{ETag: `"v"`, LastModified: time.Unix(1, 0)}
	if etagBody("k", transform.FormatUnset, 0, withValidator, fb, "") != etag("k", transform.FormatUnset, 0, withValidator, nil, "") {
		t.Error("a validator ETag depends on the bytes")
	}
}
