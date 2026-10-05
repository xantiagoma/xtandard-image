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

// maxWatermarkFetches bounds the concurrent watermark (or overlay)
// fetches of one request.
const maxWatermarkFetches = 4

// fetchWatermarks is stage 9 for the request's own watermark images
// (fetchImages). The caller releases every returned buffer
// (releaseWatermarks).
func (s *Server) fetchWatermarks(ctx context.Context, req *transform.Request, maxBytes int64) ([]WatermarkInput, error) {
	var out []WatermarkInput
	var specs []transform.SourceSpec
	for p := range req.Pipelines {
		for i := range req.Pipelines[p].Watermarks {
			if w := &req.Pipelines[p].Watermarks[i]; w.Source != nil && w.Text == "" {
				out = append(out, WatermarkInput{Pipeline: p, Index: i})
				specs = append(specs, *w.Source)
			}
		}
	}
	bufs, err := s.fetchImages(ctx, specs, maxBytes)
	if err != nil {
		return nil, err
	}
	for i := range bufs {
		out[i].Source = bufs[i]
	}
	return out, nil
}

// fetchOverlays fetches the images of a video transformation's image
// overlays (WP-124) like watermark images (fetchImages): out[i] is
// op.Overlays[i]'s, nil for text, timecodes and the configured
// watermark. The caller releases every non-nil buffer.
func (s *Server) fetchOverlays(ctx context.Context, op *transform.VideoOp, maxBytes int64) ([]fetch.Buffer, error) {
	var at []int
	var specs []transform.SourceSpec
	for i := range op.Overlays {
		if o := &op.Overlays[i]; o.Kind() == transform.VideoOverlayImage {
			at = append(at, i)
			specs = append(specs, *o.Source)
		}
	}
	bufs, err := s.fetchImages(ctx, specs, maxBytes)
	if err != nil || bufs == nil {
		return nil, err
	}
	out := make([]fetch.Buffer, len(op.Overlays))
	for k, i := range at {
		out[i] = bufs[k]
	}
	return out, nil
}

// fetchImages fetches watermark and overlay images: each source is
// resolved against the allowlist and fetched through the same fetcher as
// the source (SSRF policy, origin cache, coalescing), with the request's
// source byte limit, and prepared like a source (an SVG is sanitised, a
// designer container or SVGZ unpacked). The fetches run in parallel; the
// first failure cancels the others and is returned. The caller releases
// every returned buffer.
func (s *Server) fetchImages(ctx context.Context, specs []transform.SourceSpec, maxBytes int64) ([]fetch.Buffer, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	keys := make([]source.Key, len(specs))
	for i := range specs {
		key, err := s.resolve(ctx, specs[i])
		if err != nil {
			return nil, fmt.Errorf("server: watermark source: %w", err)
		}
		keys[i] = key
	}
	out := make([]fetch.Buffer, len(specs))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxWatermarkFetches)
	for i := range out {
		g.Go(func() error {
			fr, err := s.fetch(gctx, keys[i], source.FetchOptions{MaxBytes: maxBytes}, nil)
			if err != nil {
				return fmt.Errorf("server: watermark fetch: %w", err)
			}
			defer fr.Release()
			// A designer container or SVGZ watermark works like a source.
			buf, _, _, err := s.prepare(gctx, fr.Buffer, maxBytes)
			if err != nil {
				return fmt.Errorf("server: watermark: %w", err)
			}
			out[i] = buf
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		releaseBuffers(out)
		if ctx.Err() != nil {
			return nil, errs.Wrap(errs.KindOf(ctx.Err()), context.Cause(ctx))
		}
		return nil, err
	}
	return out, nil
}

// releaseBuffers drops every non-nil reference of bs.
func releaseBuffers(bs []fetch.Buffer) {
	for i := range bs {
		if bs[i] != nil {
			bs[i].Release()
			bs[i] = nil
		}
	}
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
