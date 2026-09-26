package app

import (
	"bytes"
	"context"
	"fmt"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/svg"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// vipsProcessor is the walking skeleton's pipeline: SVG sanitising, one
// shrink-on-load thumbnail and a JPEG save, all on one locked thread.
// WP-26a/WP-34a replace it with codec + pipeline/plan + pipeline/exec.
type vipsProcessor struct {
	quality int
}

// Thumbnail implements server.Processor.
func (p *vipsProcessor) Thumbnail(ctx context.Context, src server.Body, width, height int) (*server.Result, error) {
	buf, err := p.source(src)
	if err != nil {
		return nil, err
	}
	defer buf.Release()

	var res *server.Result
	err = vips.Run(ctx, func() error {
		img, err := vips.LoadThumbnail(ctx, buf, width, height)
		if err != nil {
			return err
		}
		defer img.Close()
		blob, err := img.SaveJPEG(ctx, p.quality)
		if err != nil {
			return err
		}
		res = &server.Result{
			Body:        blob,
			ContentType: "image/jpeg",
			Format:      "jpeg",
			Width:       img.Width(),
			Height:      img.Height(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// source returns a vips Buffer holding what libvips may decode, with one
// reference owned by the caller. SVG documents are replaced by their
// sanitised form before they can reach librsvg (ARCHITECTURE §10).
func (p *vipsProcessor) source(src server.Body) (*vips.Buffer, error) {
	data := src.Bytes()
	if svg.IsSVG(data) {
		// Cold path, bounded by svg.DefaultMaxBytes: sanitise into Go memory,
		// then copy into a C buffer.
		var clean bytes.Buffer
		if err := svg.Sanitize(&clean, bytes.NewReader(data), svg.Options{}); err != nil {
			return nil, errs.Wrapf(errs.KindCorrupt, err, "sanitise svg")
		}
		return vips.NewBufferFromReader(&clean, int64(clean.Len())+1)
	}
	if buf, ok := src.(*vips.Buffer); ok {
		buf.Retain()
		return buf, nil
	}
	// Fetchers other than httpFetcher (tests) hand over Go memory: copy it
	// into C memory so libvips never holds a Go pointer.
	buf, err := vips.NewBuffer(len(data))
	if err != nil {
		return nil, fmt.Errorf("copy source: %w", err)
	}
	copy(buf.Bytes(), data)
	return buf, nil
}
