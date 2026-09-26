package server

import (
	"context"
	"errors"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// errRequestTimeout is the cause of the per-request budget, so our own
// deadline is told apart from the client's (go-standards §2).
var errRequestTimeout = errors.New("server: request timeout")

// image serves GET/HEAD /{sig}/{options}/plain/{url}: parse, source policy,
// fetch (outside the semaphore), admission, process, respond.
func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	req, err := parseImagePath(r.URL.Path)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !s.cfg.DevMode {
		s.writeError(w, r, errs.New(errs.KindForbidden,
			"no source allowlist is configured; set XI_DEV_MODE=true to allow any http(s) source"))
		return
	}

	ctx, cancel := context.WithTimeoutCause(r.Context(), s.cfg.RequestTimeout, errRequestTimeout)
	defer cancel()

	res, err := s.process(ctx, req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	defer res.Body.Release()
	respond(w, r, res)
}

func (s *Server) process(ctx context.Context, req imageRequest) (*Result, error) {
	src, err := s.deps.Fetcher.Fetch(ctx, req.Source)
	if err != nil {
		return nil, err
	}
	defer src.Release()

	release, err := s.deps.Admitter.Acquire(ctx, 0)
	if err != nil {
		return nil, err
	}
	defer release()

	return s.deps.Processor.Thumbnail(ctx, src, req.Width, req.Height)
}

// respond writes the encoded image zero-copy in one Write, with
// Content-Length and a strong ETag over the bytes; If-None-Match → 304.
func respond(w http.ResponseWriter, r *http.Request, res *Result) {
	body := res.Body.Bytes()
	etag := etagOf(body)
	if sw, ok := w.(*statusWriter); ok {
		sw.format = res.Format
	}
	h := w.Header()
	h.Set("Content-Type", res.ContentType)
	h.Set("ETag", etag)
	h.Set("X-Content-Type-Options", "nosniff")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) //nolint:gosec // G705: encoded image bytes with an explicit image Content-Type and nosniff
	}
}

// etagOf returns a quoted strong ETag: FNV-1a 64 of the encoded bytes, stable
// across processes and replicas.
func etagOf(body []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(body)
	b := make([]byte, 0, 18)
	b = append(b, '"')
	b = strconv.AppendUint(b, h.Sum64(), 16)
	b = append(b, '"')
	return string(b)
}

// etagMatches implements the weak comparison of If-None-Match (RFC 9110
// §13.1.2).
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	for tag := range strings.SplitSeq(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
			return true
		}
	}
	return false
}
