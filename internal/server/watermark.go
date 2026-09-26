package server

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// WatermarkInput is a per-request watermark image (imgproxy
// watermark_url, imgix mark, cloudinary l_, imagekit l-image): the image of
// Request.Pipelines[Pipeline].Watermarks[Index], fetched like a source.
type WatermarkInput struct {
	Pipeline, Index int
	// Source holds the bytes, sanitised when the image is an SVG. The
	// server owns the reference and releases it after Process returns.
	Source fetch.Buffer
}

// maxWatermarkFetches bounds the concurrent watermark fetches of one
// request.
const maxWatermarkFetches = 4

// fetchWatermarks is stage 9 for the request's own watermark images: each
// source is resolved against the allowlist and fetched through the same
// fetcher as the source (SSRF policy, origin cache, coalescing), with the
// request's source byte limit, and an SVG is sanitised. The fetches run in
// parallel; the first failure cancels the others and is returned. The
// caller releases every returned buffer (releaseWatermarks).
func (s *Server) fetchWatermarks(ctx context.Context, req *transform.Request, maxBytes int64) ([]WatermarkInput, error) {
	var out []WatermarkInput
	for p := range req.Pipelines {
		for i := range req.Pipelines[p].Watermarks {
			if w := &req.Pipelines[p].Watermarks[i]; w.Source != nil && w.Text == "" {
				out = append(out, WatermarkInput{Pipeline: p, Index: i})
			}
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	keys := make([]source.Key, len(out))
	for i, in := range out {
		key, err := s.deps.Resolver.Resolve(*req.Pipelines[in.Pipeline].Watermarks[in.Index].Source)
		if err != nil {
			return nil, fmt.Errorf("server: watermark source: %w", err)
		}
		keys[i] = key
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxWatermarkFetches)
	for i := range out {
		g.Go(func() error {
			fr, err := s.fetch(gctx, keys[i], source.FetchOptions{MaxBytes: maxBytes}, nil)
			if err != nil {
				return fmt.Errorf("server: watermark fetch: %w", err)
			}
			defer fr.Release()
			buf, _, err := s.sanitize(fr.Buffer)
			if err != nil {
				return fmt.Errorf("server: watermark: %w", err)
			}
			out[i].Source = buf
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		releaseWatermarks(out)
		if ctx.Err() != nil {
			return nil, errs.Wrap(errs.KindOf(ctx.Err()), context.Cause(ctx))
		}
		return nil, err
	}
	return out, nil
}

// releaseWatermarks drops the references fetchWatermarks returned.
func releaseWatermarks(ws []WatermarkInput) {
	for i := range ws {
		if ws[i].Source != nil {
			ws[i].Source.Release()
			ws[i].Source = nil
		}
	}
}
