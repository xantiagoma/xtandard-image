package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/ssrf"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// httpFetcher is the walking skeleton's source: one plain GET through the
// SSRF-safe client, read straight into C memory owned by libvips. WP-18
// (internal/fetch + fetch/httpsrc) replaces it with singleflight, the origin
// cache and retries.
type httpFetcher struct {
	client   *http.Client
	timeout  time.Duration
	maxBytes int64
}

// Fetch implements server.Fetcher.
func (f *httpFetcher) Fetch(ctx context.Context, rawURL string) (server.Body, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, errs.Wrapf(errs.KindInvalidURL, err, "fetch: build request")
	}
	host := hostOf(req.URL)
	resp, err := f.client.Do(req)
	if err != nil {
		// The ssrf transport classifies its errors; anything else is an
		// upstream failure.
		if errs.KindOf(err) == errs.KindInternal {
			return nil, errs.Wrapf(errs.KindUpstreamError, err, "fetch %s", host)
		}
		return nil, fmt.Errorf("fetch %s: %w", host, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, errs.Newf(errs.KindNotFound, "fetch %s: status %d", host, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, errs.Newf(errs.KindUpstreamError, "fetch %s: status %d", host, resp.StatusCode)
	}
	if err := ssrf.LimitBody(resp, f.maxBytes); err != nil {
		return nil, err
	}

	if n := resp.ContentLength; n > 0 {
		buf, err := vips.NewBuffer(int(n))
		if err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(resp.Body, buf.Bytes()); err != nil {
			buf.Release()
			return nil, readError(host, err)
		}
		return buf, nil
	}
	buf, err := vips.NewBufferFromReader(resp.Body, f.maxBytes)
	if err != nil {
		return nil, readError(host, err)
	}
	return buf, nil
}

func readError(host string, err error) error {
	if errs.KindOf(err) == errs.KindInternal {
		// A plain transport error mid-body (reset, unexpected EOF).
		return errs.Wrapf(errs.KindUpstreamError, err, "fetch %s: read body", host)
	}
	return fmt.Errorf("fetch %s: read body: %w", host, err)
}

// hostOf returns the host for logs; never the path or query.
func hostOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Host
}
