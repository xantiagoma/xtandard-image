package app

import (
	"context"

	"github.com/xantiagoma/xtandard-image/internal/codec"
	"github.com/xantiagoma/xtandard-image/internal/codec/imagemeta"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/fetch"
	"github.com/xantiagoma/xtandard-image/internal/imageinfo"
	"github.com/xantiagoma/xtandard-image/internal/pipeline/plan"
	"github.com/xantiagoma/xtandard-image/internal/server"
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/video"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// vipsInspector is the production server.Inspector: codec.Inspect (probe
// plus a header-only load) on a locked vips thread, with IPTC and XMP
// parsed into imgproxy's /info shapes.
type vipsInspector struct {
	formats codec.Limits   // static per-format limits and settings (formatLimits)
	video   video.Settings // XI_VIDEO_* (videoSettings)
	// detect answers detect_objects and crops placed on detections; nil
	// is no detection.
	detect *detections
}

// Inspect implements server.Inspector. The pixel-derived fields px asks
// for are computed in the same vips.Run: codec.Sample decodes the first
// page to a copy of at most imageinfo.MaxSide pixels a side, and
// imageinfo.Analyze runs on it. Detection (det) runs after it on its own
// sample, through the detection cache shared with processing.
func (in *vipsInspector) Inspect(ctx context.Context, src fetch.Buffer, lim server.ProbeLimits, px *imageinfo.Options, det plan.DetectionNeeds) (*server.SourceInfo, error) {
	vb, err := vipsBuffer(src)
	if err != nil {
		return nil, err
	}
	defer vb.Release()
	var ci codec.Info
	var pixels *imageinfo.Result
	err = vips.Run(ctx, func() error {
		var err error
		l := withProbeLimits(in.formats, &lim)
		if ci, err = codec.InspectMedia(ctx, vb, l, &in.video, ""); err != nil || !px.Any() {
			return err
		}
		raw, err := codec.SampleMedia(ctx, vb, ci.Meta.Format, imageinfo.MaxSide, l, &in.video)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pixels, err = imageinfo.Analyze(&imageinfo.Image{Width: raw.Width, Height: raw.Height, Pix: raw.Data}, px)
		if err != nil {
			return errs.Wrap(errs.KindInternal, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	si := sourceInfo(&ci)
	si.Pixels = pixels
	ds := imageSource(vb, ci.Meta.Format)
	var frame *codec.DecodedFrame
	defer func() { frame.Release() }()
	if ci.Meta.Format.IsVideo() {
		// The frame the default selection picks, like the pixel fields
		// (WP-91).
		ds.frame = func(ctx context.Context) (*codec.DecodedFrame, int, error) {
			f, orientation, err := codec.SampleVideoFrame(ctx, vb, detectSampleSide, &in.video)
			frame = f
			return f, orientation, err
		}
	}
	if si.Detections, err = in.detect.forRequest(ctx, ds, det); err != nil {
		return nil, err
	}
	return si, nil
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
		HDR:          ci.HDR,
		Video:        ci.Video,
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
