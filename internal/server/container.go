package server

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec/container"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
)

// hdrContainerSource is the debug header naming the container a source's
// pixels came from (container.Source.Header).
const hdrContainerSource = "X-Xi-Container-Source"

// InfoContainer is the /info "container" object: the source is a designer
// document and the reported image is the one embedded in it (WP-107). It is
// public API (internal/apidoc reflects it into the OpenAPI document).
type InfoContainer struct {
	Format   string `json:"format"    doc:"Container format: ora (OpenRaster), kra (Krita), procreate, sketch, svgz, pdn (Paint.NET), mng, jng, wmf or emf."`
	MIMEType string `json:"mime_type" doc:"Media type of the container, e.g. image/openraster."`
	Entry    string `json:"entry"     doc:"Where the image was found: the path inside the archive (mergedimage.png, QuickLook/Thumbnail.png, previews/preview.png, ...), gzip for SVGZ, header thumbnail for Paint.NET, first frame for MNG, JDAT (JDAT+IDAT, JDAT+JDAA with alpha) for JNG, xi-metafile2svg for WMF and EMF."`
	Content  string `json:"content"   doc:"What the image is to the document: composite (full-size flattened image), thumbnail (reduced copy), preview (first page), document (SVGZ: the whole document, decompressed), frame (MNG: the first image of the animation) or converted (WMF, EMF: the document converted to SVG)."`
}

// infoContainer renders c for /info; nil when the source is no container.
func infoContainer(c *container.Source) *InfoContainer {
	if c == nil {
		return nil
	}
	return &InfoContainer{Format: c.Kind.String(), MIMEType: c.Kind.MIME(), Entry: c.Entry, Content: c.Content.String()}
}

// unwrap replaces a designer container (OpenRaster, Krita, Procreate,
// Sketch, SVGZ, Paint.NET; codec/container) or an MNG/JNG file (WP-108)
// with the image it carries, or a WMF/EMF metafile (WP-108) with the SVG
// Deps.Metafile converts it to (convertMetafile), in a
// new allocator buffer, before anything else parses the source: the SVG
// of an SVGZ file is then sanitised like any other SVG. maxBytes bounds the
// extracted raster (the source byte limit; zero is the package default),
// the SVG byte limit an SVGZ document. Any other source is returned with
// one extra reference and a nil Source.
func (s *Server) unwrap(ctx context.Context, buf fetch.Buffer, maxBytes int64) (fetch.Buffer, *container.Source, error) {
	if kind, ok := container.DetectMetafile(buf.Bytes()); ok {
		return s.convertMetafile(ctx, buf, kind)
	}
	emb, err := container.Open(buf.Bytes(), container.Limits{
		MaxBytes:    maxBytes,
		MaxSVGBytes: s.cfg.SVG.MaxBytes,
		// A JNG with alpha is decoded and re-encoded here (WP-108): the
		// configured pixel limit bounds it before anything is decoded; the
		// probe then applies the request's own limits to the result.
		MaxPixels: s.cfg.Limits.MaxSrcPixels,
	})
	if err != nil {
		return nil, nil, containerError(err)
	}
	if emb == nil {
		buf.Retain()
		return buf, nil, nil
	}
	out, err := s.deps.Allocator(emb.Size())
	if err != nil {
		return nil, nil, errs.Wrapf(errs.KindInternal, err, "server: allocate embedded image")
	}
	if err := emb.ReadInto(out.Bytes()); err != nil {
		out.Release()
		return nil, nil, containerError(err)
	}
	src := emb.Source
	return out, &src, nil
}

// MetafileConverter turns a WMF or EMF source into an SVG document
// (internal/metafile: the gpl image's xi-metafile2svg in a sandboxed
// subprocess). kind is "wmf" or "emf".
type MetafileConverter interface {
	ToSVG(ctx context.Context, kind string, data []byte) ([]byte, error)
}

// metafileUnavailable is the public message of a WMF/EMF source without a
// converter (slim and full images, or XI_FORMATS_METAFILE_ENABLED=false).
const metafileUnavailable = "WMF and EMF input needs the gpl image (XI_FORMATS_METAFILE_ENABLED)"

// convertMetafile replaces a WMF or EMF source with the SVG document the
// converter makes of it, in a new allocator buffer; the SVG is then
// sanitised and rendered like any other.
func (s *Server) convertMetafile(ctx context.Context, buf fetch.Buffer, kind container.Kind) (fetch.Buffer, *container.Source, error) {
	if s.deps.Metafile == nil {
		return nil, nil, errs.WithPublic(errs.Newf(errs.KindUnsupported, "server: %s input without a converter", kind),
			metafileUnavailable)
	}
	svg, err := s.deps.Metafile.ToSVG(ctx, kind.String(), buf.Bytes())
	if err != nil {
		switch errs.KindOf(err) {
		case errs.KindLimitExceeded, errs.KindUnsupported, errs.KindCorrupt, errs.KindOverloaded,
			errs.KindTimeout, errs.KindCanceled:
			return nil, nil, err
		default:
			return nil, nil, errs.Wrapf(errs.KindCorrupt, err, "server: convert %s", kind)
		}
	}
	out, err := s.deps.Allocator(len(svg))
	if err != nil {
		return nil, nil, errs.Wrapf(errs.KindInternal, err, "server: allocate converted %s", kind)
	}
	copy(out.Bytes(), svg)
	return out, &container.Source{Kind: kind, Entry: "xi-metafile2svg", Content: container.ContentConverted}, nil
}

// containerError keeps the kinds the container reader chose and reports
// anything else as a corrupt source.
func containerError(err error) error {
	switch errs.KindOf(err) {
	case errs.KindLimitExceeded, errs.KindUnsupported, errs.KindCorrupt:
		return err
	default:
		return errs.Wrapf(errs.KindCorrupt, err, "server: container")
	}
}
