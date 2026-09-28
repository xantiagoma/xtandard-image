package server

import (
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
	Format   string `json:"format"    doc:"Container format: ora (OpenRaster), kra (Krita), procreate, sketch, svgz, pdn (Paint.NET), mng or jng."`
	MIMEType string `json:"mime_type" doc:"Media type of the container, e.g. image/openraster."`
	Entry    string `json:"entry"     doc:"Where the image was found: the path inside the archive (mergedimage.png, QuickLook/Thumbnail.png, previews/preview.png, ...), gzip for SVGZ, header thumbnail for Paint.NET, first frame for MNG, JDAT (JDAT+IDAT, JDAT+JDAA with alpha) for JNG."`
	Content  string `json:"content"   doc:"What the image is to the document: composite (full-size flattened image), thumbnail (reduced copy), preview (first page), document (SVGZ: the whole document, decompressed) or frame (MNG: the first image of the animation)."`
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
// with the image it carries, in a
// new allocator buffer, before anything else parses the source: the SVG
// of an SVGZ file is then sanitised like any other SVG. maxBytes bounds the
// extracted raster (the source byte limit; zero is the package default),
// the SVG byte limit an SVGZ document. Any other source is returned with
// one extra reference and a nil Source.
func (s *Server) unwrap(buf fetch.Buffer, maxBytes int64) (fetch.Buffer, *container.Source, error) {
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
