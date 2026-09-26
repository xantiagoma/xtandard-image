package encode

import (
	"github.com/xantiagoma/xtandard-image/internal/transform"
	"github.com/xantiagoma/xtandard-image/internal/vips"
)

// ResolveQuality returns the quality for format f: the request's quality,
// else its format_quality[f], else the configured per-format quality, else
// the global default (imgproxy processing/options.go Quality). The result is
// clamped to 1..100.
func (c *Config) ResolveQuality(f transform.Format, quality int, formatQuality map[transform.Format]int) int {
	q := quality
	if q <= 0 {
		q = formatQuality[f]
	}
	if q <= 0 {
		q = c.FormatQuality[f]
	}
	if q <= 0 {
		q = c.Quality
	}
	return min(max(q, vips.MinQuality), vips.MaxQuality)
}

// plan is the resolved encoding of one Target: the libvips format, its
// options, and whether quality drives the output size.
type plan struct {
	format  vips.Format
	opts    vips.SaveOptions
	quality int  // 0 when the save has no quality knob
	flatten bool // the format has no alpha: flatten on white first
}

// setQuality sets the quality of the save to q.
func (p *plan) setQuality(q int) {
	p.quality = q
	switch p.format {
	case vips.FormatJPEG:
		p.opts.JPEG.Quality = q
	case vips.FormatWebP:
		p.opts.WebP.Quality = q
	case vips.FormatAVIF, vips.FormatHEIF:
		p.opts.HEIF.Quality = q
	case vips.FormatJXL:
		p.opts.JXL.Quality = q
	case vips.FormatTIFF:
		p.opts.TIFF.Quality = q
	default:
	}
}

// imageInfo is what option mapping needs to know about the image.
type imageInfo struct {
	pixels   int64 // width × height of one frame
	hasAlpha bool
}

// planFor maps t onto libvips save options. ok is false for formats libvips
// does not save here (BMP, ICO handled in Go; everything else unsupported).
func (c *Config) planFor(t *Target, info imageInfo) (plan, bool) {
	o := &t.Options
	p := plan{opts: vips.DefaultSaveOptions()}
	q := c.ResolveQuality(t.Format, t.Quality, t.FormatQuality)
	switch t.Format {
	case transform.FormatJPEG:
		p.format = vips.FormatJPEG
		p.flatten = true
		progressive := c.JPEGProgressive
		if o.Progressive != nil {
			progressive = *o.Progressive
		}
		sub := subsample(o.ChromaSubsampling)
		if j := o.JPEG; j != nil {
			if j.Progressive != nil {
				progressive = *j.Progressive
			}
			if j.NoSubsample != nil && *j.NoSubsample {
				sub = vips.SubsampleOff
			}
			// mozjpeg extensions (imgproxy vips_jpegsave_go): no-ops with
			// the base image's libjpeg-turbo, honoured by a mozjpeg build.
			p.opts.JPEG.TrellisQuant = isTrue(j.TrellisQuant)
			p.opts.JPEG.OvershootDeringing = isTrue(j.OvershootDeringing)
			p.opts.JPEG.OptimizeScans = isTrue(j.OptimizeScans)
			if j.QuantTable != nil {
				p.opts.JPEG.QuantTable = *j.QuantTable
			}
		}
		p.opts.JPEG.Interlace = progressive
		p.opts.JPEG.OptimizeCoding = true
		p.opts.JPEG.Subsample = sub
		p.setQuality(q)

	case transform.FormatPNG:
		p.format = vips.FormatPNG
		c.pngOptions(&p.opts.PNG, o)

	case transform.FormatWebP:
		p.format = vips.FormatWebP
		w := &p.opts.WebP
		w.Effort = c.WebPEffort
		if o.Effort > 0 {
			w.Effort = nativeEffort(o.Effort, 0, 6)
		}
		compression := transform.WebPCompressionUnset
		if o.WebP != nil {
			compression = o.WebP.Compression
			if o.WebP.SmartSubsample != nil {
				w.SmartSubsample = *o.WebP.SmartSubsample
			}
			w.Preset = webpPreset(o.WebP.Preset)
		}
		switch compression {
		case transform.WebPLossless:
			w.Lossless = true
		case transform.WebPNearLossless:
			w.NearLossless = true
		case transform.WebPLossy:
		default:
			w.Lossless = lossless(o)
		}
		if w.Lossless {
			// libwebp's lossless Q is a speed/size trade-off, not quality:
			// keep the libvips default and report no quality.
			break
		}
		p.setQuality(q)

	case transform.FormatAVIF, transform.FormatHEIC:
		h := &p.opts.HEIF
		h.Subsample = subsample(o.ChromaSubsampling)
		if o.AVIF != nil && o.AVIF.Subsample != transform.SubsamplingAuto {
			h.Subsample = subsample(o.AVIF.Subsample)
		}
		if t.Format == transform.FormatHEIC {
			p.format = vips.FormatHEIF
			// kvazaar/libheif write 8-bit HEVC only (imgproxy heifsave).
			h.BitDepth = 8
			if o.Effort > 0 {
				h.Effort = nativeEffort(o.Effort, 0, 9)
			}
			p.setQuality(q)
			break
		}
		p.format = vips.FormatAVIF
		h.Effort = c.AVIFEffort
		if c.AVIFLargePixels > 0 && info.pixels > c.AVIFLargePixels {
			h.Effort = min(h.Effort, c.AVIFLargeEffort)
		}
		if o.Effort > 0 {
			h.Effort = nativeEffort(o.Effort, 0, 9)
		}
		// Lossless AV1 needs 4:4:4 (libvips forces it).
		h.Lossless = lossless(o)
		if !h.Lossless {
			p.setQuality(q)
		}

	case transform.FormatJXL:
		p.format = vips.FormatJXL
		x := &p.opts.JXL
		x.Effort = c.JXLEffort
		if o.Effort > 0 {
			x.Effort = nativeEffort(o.Effort, 1, 10)
		}
		x.Distance = 0 // derive the butteraugli distance from Quality
		x.Lossless = lossless(o)
		if !x.Lossless {
			p.setQuality(q)
		}

	case transform.FormatGIF:
		p.format = vips.FormatGIF
		g := &p.opts.GIF
		g.Effort = c.GIFEffort
		if o.Effort > 0 {
			g.Effort = nativeEffort(o.Effort, 1, 10)
		}
		if o.Progressive != nil {
			g.Interlace = *o.Progressive
		}

	case transform.FormatTIFF:
		p.format = vips.FormatTIFF
		tf := &p.opts.TIFF
		// FORMATS §5: JPEG compression at the resolved quality; deflate with
		// a horizontal predictor when lossless is asked for or alpha must
		// survive (JPEG-in-TIFF has no alpha).
		if lossless(o) || info.hasAlpha {
			tf.Compression = vips.TIFFCompressionDeflate
			tf.Predictor = vips.TIFFPredictorHorizontal
			break
		}
		tf.Compression = vips.TIFFCompressionJPEG
		tf.Predictor = vips.TIFFPredictorNone
		p.setQuality(q)

	default:
		return plan{}, false
	}
	return p, true
}

// pngOptions maps png_options onto pngsave (imgproxy vips_pngsave_go).
func (c *Config) pngOptions(p *vips.PNGOptions, o *Options) {
	quantize := c.PNGQuantize || o.Quantize
	colors := 0
	adaptive := true
	if o.Progressive != nil {
		p.Interlace = *o.Progressive
	}
	if po := o.PNG; po != nil {
		if po.Quantize != nil {
			quantize = *po.Quantize
		}
		if po.Interlaced != nil {
			p.Interlace = *po.Interlaced
		}
		if po.Compression != nil {
			p.Compression = min(max(*po.Compression, 0), 9)
		}
		if po.AdaptiveFilter != nil {
			adaptive = *po.AdaptiveFilter
		}
		colors = po.QuantizationColors
	}
	if o.Effort > 0 {
		p.Effort = nativeEffort(o.Effort, 1, 10)
	}
	if !quantize {
		if adaptive {
			p.Filter = vips.PNGFilterAll
		}
		return
	}
	p.Palette = true
	p.Filter = vips.PNGFilterNone
	p.BitDepth = paletteBitDepth(colors)
}

// paletteBitDepth is the smallest PNG bit depth holding colors palette
// entries (0 or > 256 means 256).
func paletteBitDepth(colors int) int {
	switch {
	case colors <= 0 || colors > 16:
		return 8
	case colors > 4:
		return 4
	case colors > 2:
		return 2
	default:
		return 1
	}
}

func isTrue(b *bool) bool { return b != nil && *b }

// lossless resolves the request's lossless flag, falling back to the format
// decision's hint.
func lossless(o *Options) bool {
	if o.Lossless != nil {
		return *o.Lossless
	}
	return o.PreferLossless
}

// nativeEffort maps the normalised effort 1..9 linearly onto an encoder's
// low..high scale.
func nativeEffort(e, low, high int) int {
	e = min(max(e, 1), 9)
	return low + ((e-1)*(high-low)+4)/8
}

// subsample maps the request's chroma subsampling onto libvips, which only
// knows 4:2:0 (on), 4:4:4 (off) and auto (4:2:0 below quality 90).
func subsample(s transform.Subsampling) vips.Subsample {
	switch s {
	case transform.Subsampling420, transform.Subsampling422:
		return vips.SubsampleOn
	case transform.Subsampling444:
		return vips.SubsampleOff
	default:
		return vips.SubsampleAuto
	}
}

func webpPreset(p transform.WebPPreset) vips.WebPPreset {
	switch p {
	case transform.WebPPresetPhoto:
		return vips.WebPPresetPhoto
	case transform.WebPPresetPicture:
		return vips.WebPPresetPicture
	case transform.WebPPresetDrawing:
		return vips.WebPPresetDrawing
	case transform.WebPPresetIcon:
		return vips.WebPPresetIcon
	case transform.WebPPresetText:
		return vips.WebPPresetText
	default:
		return vips.WebPPresetDefault
	}
}

// animatable reports whether f can hold an animation (FORMATS §3). AVIF
// sequences are not written in M1.
func animatable(f transform.Format) bool {
	switch f {
	case transform.FormatGIF, transform.FormatWebP, transform.FormatJXL, transform.FormatMP4,
		transform.FormatWebM, transform.FormatHEVC:
		return true
	default:
		return false
	}
}
