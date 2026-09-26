package app

import (
	"bytes"
	"context"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/exec"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/source"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// loadWatermark fetches XI_PROCESSING_WATERMARK_SOURCE once at startup
// through the source registry (allowlists and SSRF policy included) and the
// fetcher, sanitises it when it is an SVG, and decodes it for the process
// lifetime; it is never refreshed. It returns nil when no watermark is
// configured. Any failure is a startup error: a configured watermark that
// cannot be loaded would otherwise silently disappear from every image.
func loadWatermark(ctx context.Context, cfg *config.Config, res server.Resolver, f server.Fetcher) (*exec.Watermark, error) {
	raw := cfg.Processing.WatermarkSource
	if raw == "" {
		return nil, nil
	}
	key, err := res.Resolve(transform.SourceSpec{Raw: raw})
	if err != nil {
		return nil, fmt.Errorf("watermark source: %w", err)
	}
	fr, err := f.Fetch(ctx, key, source.FetchOptions{})
	if err != nil {
		return nil, fmt.Errorf("watermark fetch: %w", err)
	}
	defer fr.Release()

	buf, err := watermarkBuffer(fr.Bytes(), cfg)
	if err != nil {
		return nil, fmt.Errorf("watermark: %w", err)
	}
	defer buf.Release()
	format, ok := codec.Sniff(buf.Bytes())
	if !ok {
		return nil, errs.New(errs.KindUnsupported, "watermark: unrecognised image format")
	}
	name := format.String()
	if format == codec.FormatUHDR {
		name = "jpeg"
	}
	wm, err := exec.NewWatermark(ctx, buf, &exec.WatermarkOptions{
		Format:    name,
		Opacity:   cfg.Processing.WatermarkOpacity,
		FailOn:    vipsFailOn(cfg.Vips.FailOn),
		MaxPixels: cfg.Limits.MaxSrcPixels,
	})
	if err != nil {
		return nil, fmt.Errorf("watermark decode: %w", err)
	}
	return wm, nil
}

// watermarkBuffer copies data into C memory, sanitising an SVG first with
// the request limits (librsvg never sees an unsanitised document).
func watermarkBuffer(data []byte, cfg *config.Config) (*vips.Buffer, error) {
	if svg.IsSVG(data) {
		var clean bytes.Buffer
		err := svg.Sanitize(&clean, bytes.NewReader(data), svg.Options{
			MaxBytes:    int64(cfg.Limits.MaxSVGBytes),
			MaxDepth:    cfg.Limits.MaxSVGDepth,
			MaxElements: cfg.Limits.MaxSVGElements,
		})
		if err != nil {
			return nil, fmt.Errorf("sanitise svg: %w", err)
		}
		data = clean.Bytes()
	}
	buf, err := vips.NewBuffer(len(data))
	if err != nil {
		return nil, err
	}
	copy(buf.Bytes(), data)
	return buf, nil
}
