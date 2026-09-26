package server

import (
	"crypto/md5"  //nolint:gosec // G501: computing the expected hashsum:md5 digest
	"crypto/sha1" //nolint:gosec // G505: computing the expected hashsum:sha1 digest
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// digests returns the hex digest of data for every hashsum algorithm.
func digests(data []byte) map[transform.HashAlgorithm]string {
	m5 := md5.Sum(data)  //nolint:gosec // G401: see the import
	s1 := sha1.Sum(data) //nolint:gosec // G401: see the import
	s256 := sha256.Sum256(data)
	s512 := sha512.Sum512(data)
	return map[transform.HashAlgorithm]string{
		transform.HashMD5:    hex.EncodeToString(m5[:]),
		transform.HashSHA1:   hex.EncodeToString(s1[:]),
		transform.HashSHA256: hex.EncodeToString(s256[:]),
		transform.HashSHA512: hex.EncodeToString(s512[:]),
	}
}

var hashAlgorithms = []transform.HashAlgorithm{transform.HashMD5, transform.HashSHA1, transform.HashSHA256, transform.HashSHA512}

func TestVerifyHashsum(t *testing.T) {
	t.Parallel()
	data := []byte("the source bytes")
	sums := digests(data)
	for _, a := range hashAlgorithms {
		t.Run(a.String(), func(t *testing.T) {
			t.Parallel()
			if err := verifyHashsum(&transform.Hashsum{Algorithm: a, Digest: sums[a]}, data); err != nil {
				t.Errorf("matching digest: %v", err)
			}
			other := digests([]byte("other bytes"))[a]
			err := verifyHashsum(&transform.Hashsum{Algorithm: a, Digest: other}, data)
			if errs.KindOf(err) != errs.KindHashMismatch {
				t.Errorf("mismatch: err = %v (kind %v), want hash_mismatch", err, errs.KindOf(err))
			}
			// A digest of another algorithm never matches (different length).
			wrong := sums[hashAlgorithms[(int(a))%len(hashAlgorithms)]]
			if err := verifyHashsum(&transform.Hashsum{Algorithm: a, Digest: wrong}, data); errs.KindOf(err) != errs.KindHashMismatch {
				t.Errorf("other algorithm's digest: err = %v", err)
			}
		})
	}
	for _, want := range []*transform.Hashsum{nil, {}} {
		if err := verifyHashsum(want, data); err != nil {
			t.Errorf("verifyHashsum(%v) = %v, want nil (no check)", want, err)
		}
	}
}

// TestHashsumRequests checks the fetched source against hashsum for
// processing, raw and /info requests: a match is served, a mismatch is 422
// hash_mismatch, is not answered with the fallback image and is never
// processed. The fallback image itself is not checked.
func TestHashsumRequests(t *testing.T) {
	t.Parallel()
	sums := digests(jpegSource)
	for _, a := range hashAlgorithms {
		for _, tc := range []struct {
			name  string
			match bool
			raw   bool
		}{
			{name: "match", match: true},
			{name: "mismatch"},
			{name: "raw match", match: true, raw: true},
			{name: "raw mismatch", raw: true},
		} {
			t.Run(a.String()+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				digest := sums[a]
				if !tc.match {
					digest = strings.Repeat("0", len(digest))
				}
				h := newHarness(t, func(h *harness) {
					h.f.other = &fakeReply{data: fallbackSource}
					h.cfg.Fallback = Fallback{Source: fallbackRef}
					h.router.mutate = func(r *transform.Request) {
						r.Source.Hashsum = &transform.Hashsum{Algorithm: a, Digest: digest}
						if tc.raw {
							r.Kind = transform.KindRaw
						}
					}
				})
				rec := h.do(t, http.MethodGet, okPath, nil)
				if !tc.match {
					if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "hash mismatch") {
						t.Fatalf("status = %d body %q, want 422 source hash mismatch", rec.Code, rec.Body.String())
					}
					if h.fetchedFallback() {
						t.Error("a hashsum mismatch was answered with the fallback image")
					}
					if h.p.calls != 0 {
						t.Error("processed a source whose hashsum does not match")
					}
				} else if rec.Code != http.StatusOK {
					t.Fatalf("status = %d body %q, want 200", rec.Code, rec.Body.String())
				}
				h.checkReleased(t)
			})
		}
	}

	t.Run("fallback not checked", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, func(h *harness) {
			h.f.err = errs.New(errs.KindNotFound, "origin 404")
			h.f.other = &fakeReply{data: fallbackSource}
			h.cfg.Fallback = Fallback{Source: fallbackRef}
			h.router.mutate = func(r *transform.Request) {
				r.Source.Hashsum = &transform.Hashsum{Algorithm: transform.HashSHA256, Digest: sums[transform.HashSHA256]}
			}
		})
		rec := h.do(t, http.MethodGet, okPath, nil)
		if rec.Code != http.StatusOK || !h.fetchedFallback() {
			t.Fatalf("status = %d, fallback fetched %v; want the fallback image served", rec.Code, h.fetchedFallback())
		}
		h.checkReleased(t)
	})
}

// TestHashsumInResultKey: the hashsum is part of the canonical request, so
// it changes the result-cache key (and the ETag): a cached result is only
// ever served for the hashsum that was verified when it was produced.
func TestHashsumInResultKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, okPath, http.NoBody)
	key := func(hs *transform.Hashsum) string {
		req := &transform.Request{Source: transform.SourceSpec{Raw: "http://origin/a.jpg", Hashsum: hs}}
		req.ApplyDefaults(h.cfg.Defaults)
		return h.srv.resultKey(r, &job{req: req, key: source.Key{Source: "origin", Path: "http://origin/a.jpg"}})
	}
	sums := digests(jpegSource)
	seen := map[string]string{key(nil): "none"}
	for _, a := range hashAlgorithms {
		k := key(&transform.Hashsum{Algorithm: a, Digest: sums[a]})
		if prev, dup := seen[k]; dup {
			t.Errorf("hashsum %s has the result key of %s", a, prev)
		}
		seen[k] = a.String()
	}
	other := key(&transform.Hashsum{Algorithm: transform.HashMD5, Digest: strings.Repeat("0", 32)})
	if _, dup := seen[other]; dup {
		t.Error("a different md5 digest has the same result key")
	}
}
