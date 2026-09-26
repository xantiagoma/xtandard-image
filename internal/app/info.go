package app

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/imagemeta"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// vipsInspector is the production server.Inspector: codec.Inspect (probe
// plus a header-only load) on a locked vips thread, with IPTC and XMP
// parsed into imgproxy's /info shapes.
type vipsInspector struct{}

// Inspect implements server.Inspector.
func (vipsInspector) Inspect(ctx context.Context, src fetch.Buffer, lim server.ProbeLimits) (*server.SourceInfo, error) {
	vb, err := vipsBuffer(src)
	if err != nil {
		return nil, err
	}
	defer vb.Release()
	var ci codec.Info
	err = vips.Run(ctx, func() error {
		var err error
		ci, err = codec.Inspect(ctx, vb, codec.Limits{
			MaxSrcPixels:            lim.MaxPixels,
			MaxAnimationFrames:      lim.MaxFrames,
			MaxAnimationFramePixels: lim.MaxFramePixels,
		}, "")
		return err
	})
	if err != nil {
		return nil, err
	}
	return sourceInfo(&ci), nil
}

// sourceInfo converts codec.Info to the server's view.
func sourceInfo(ci *codec.Info) *server.SourceInfo {
	m := &ci.Meta
	name, mime := codec.InfoFormat(m.Format)
	si := &server.SourceInfo{
		Meta: server.SourceMeta{
			Format:      infoTransformFormat(m.Format),
			Width:       m.Width,
			Height:      m.Height,
			Frames:      max(m.Frames, 1),
			Pages:       max(m.Pages, m.Frames, 1),
			HasAlpha:    m.HasAlpha,
			IsAnimated:  m.IsAnimated,
			Orientation: m.Orientation,
		},
		Format:       name,
		MIMEType:     mime,
		Pages:        max(m.Pages, 1),
		Bands:        ci.Bands,
		SampleFormat: ci.SampleFormat,
		Colorspace:   ci.Colorspace,
	}
	for _, e := range ci.EXIF {
		si.EXIF = append(si.EXIF, server.EXIFEntry{Name: e.Name, Title: e.Title, Value: e.Value})
	}
	if len(ci.IPTC) > 0 {
		p := imagemeta.ParseIPTC(ci.IPTC)
		si.IPTC, si.Photoshop = p.Tags, p.Photoshop
	}
	if len(ci.XMP) > 0 {
		si.XMP = imagemeta.ParseXMP(ci.XMP)
	}
	return si
}

// infoTransformFormat maps a probed format to the request model; formats
// the model cannot name (JPEG 2000) are FormatUnset.
func infoTransformFormat(f codec.Format) transform.Format {
	switch f {
	case codec.FormatJPEG, codec.FormatUHDR:
		return transform.FormatJPEG
	case codec.FormatPNG:
		return transform.FormatPNG
	case codec.FormatWebP:
		return transform.FormatWebP
	case codec.FormatGIF:
		return transform.FormatGIF
	case codec.FormatAVIF:
		return transform.FormatAVIF
	case codec.FormatHEIC, codec.FormatHEIF:
		return transform.FormatHEIC
	case codec.FormatJXL:
		return transform.FormatJXL
	case codec.FormatTIFF:
		return transform.FormatTIFF
	case codec.FormatSVG:
		return transform.FormatSVG
	case codec.FormatBMP:
		return transform.FormatBMP
	case codec.FormatICO:
		return transform.FormatICO
	default:
		return transform.FormatUnset
	}
}
