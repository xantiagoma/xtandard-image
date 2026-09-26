package awssih

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const formatExpected = "jpeg, jpg, png, webp, avif, gif, tiff, heif or jxl"

// parseFormat maps SIH output format names (lib/enums.ts ImageFormatTypes
// and sharp toFormat names). raw (pixel dump) is not an image format and
// is rejected; heif/heic encode as HEIC (XI_HEIC_ENCODE gates it).
func parseFormat(field, s string) (transform.Format, error) {
	switch strings.ToLower(s) {
	case "jpeg", "jpg":
		return transform.FormatJPEG, nil
	case "png":
		return transform.FormatPNG, nil
	case "webp":
		return transform.FormatWebP, nil
	case "avif":
		return transform.FormatAVIF, nil
	case "gif":
		return transform.FormatGIF, nil
	case "tiff", "tif":
		return transform.FormatTIFF, nil
	case "heif", "heic":
		return transform.FormatHEIC, nil
	case "jxl":
		return transform.FormatJXL, nil
	}
	return 0, dialect.InvalidArgument(field, s, formatExpected)
}

// nativeEffort ranges of sharp's encoders: webp 0–6, avif/heif 0–9,
// png/gif 1–10.
func effortRange(f transform.Format) (lo, hi int) {
	switch f {
	case transform.FormatWebP:
		return 0, 6
	case transform.FormatAVIF, transform.FormatHEIC:
		return 0, 9
	default:
		return 1, 10
	}
}

// normEffort maps a native effort onto the model's 1..9 scale so that the
// encoder's inverse mapping (codec/encode nativeEffort) returns n.
func normEffort(n, lo, hi int) int {
	n = min(max(n, lo), hi)
	return 1 + int(math.Round(float64(n-lo)*8/float64(hi-lo)))
}

func (b *builder) opts(f transform.Format) *formatOpts {
	if b.perFormat == nil {
		b.perFormat = map[transform.Format]*formatOpts{}
	}
	o := b.perFormat[f]
	if o == nil {
		o = &formatOpts{}
		b.perFormat[f] = o
	}
	return o
}

func (b *builder) setQuality(f transform.Format, name string, raw json.RawMessage) error {
	q, err := asInt(name, raw, 1, transform.MaxQuality)
	if err != nil {
		return err
	}
	if b.r.Output.FormatQuality == nil {
		b.r.Output.FormatQuality = map[transform.Format]int{}
	}
	b.r.Output.FormatQuality[f] = q
	return nil
}

func boolArg(name string, raw json.RawMessage) (*bool, error) {
	v, ok := asBool(raw)
	if !ok {
		return nil, invalidJSON(name, "a boolean")
	}
	return &v, nil
}

// formatEdit maps sharp's jpeg/png/webp/avif/heif/gif/tiff(options). Like
// sharp, the edit also selects the output format unless force is false;
// quality and the per-format encoder structs apply whenever that format is
// written, lossless and effort only when it is the output format.
//
//nolint:gocyclo // one switch per sharp format option table
func (b *builder) formatEdit(name string, raw json.RawMessage) error {
	f, _ := parseFormat(name, name)
	force := true
	if kind(raw) != kindObject {
		if kind(raw) != kindNull && kind(raw) != kindBool {
			return invalidJSON(name, "an object")
		}
		b.editFormat = f
		return nil
	}
	fs, err := objectFields(raw)
	if err != nil {
		return invalidJSON(name, "an object")
	}
	out := &b.r.Output
	o := b.opts(f)
	for _, fl := range fs {
		opt := name + "." + fl.key
		var err error
		switch fl.key {
		case "force":
			v, e := boolArg(opt, fl.raw)
			if e != nil {
				return e
			}
			force = *v
		case "quality":
			err = b.setQuality(f, opt, fl.raw)
		case "effort":
			lo, hi := effortRange(f)
			var n int
			if n, err = asInt(opt, fl.raw, lo, hi); err == nil {
				o.effort = normEffort(n, lo, hi)
			}
		case "lossless":
			o.lossless, err = boolArg(opt, fl.raw)
			if err == nil && f == transform.FormatWebP && *o.lossless {
				webp(out).Compression = transform.WebPLossless
			}
		case "progressive":
			var v *bool
			if v, err = boolArg(opt, fl.raw); err == nil {
				switch f {
				case transform.FormatJPEG:
					jpeg(out).Progressive = v
				case transform.FormatPNG:
					png(out).Interlaced = v
				default:
					b.ignore(opt, reasonUnsupported)
				}
			}
		case "chromaSubsampling":
			s, _ := asString(fl.raw)
			var sub transform.Subsampling
			switch s {
			case "4:4:4":
				sub = transform.Subsampling444
			case "4:2:0":
				sub = transform.Subsampling420
			default:
				return dialect.InvalidArgument(opt, s, "4:2:0 or 4:4:4")
			}
			switch f {
			case transform.FormatJPEG:
				jpeg(out).NoSubsample = new(sub == transform.Subsampling444)
			case transform.FormatAVIF, transform.FormatHEIC:
				avif(out).Subsample = sub
			default:
				b.ignore(opt, reasonUnsupported)
			}
		case "mozjpeg":
			var v *bool
			if v, err = boolArg(opt, fl.raw); err == nil && *v && f == transform.FormatJPEG {
				// sharp: mozjpeg = trellis + overshoot deringing + optimised
				// scans + quantisation table 3.
				j := jpeg(out)
				j.TrellisQuant, j.OvershootDeringing, j.OptimizeScans, j.QuantTable = new(true), new(true), new(true), new(3)
			}
		case "trellisQuantisation", "trellisQuantization":
			jpeg(out).TrellisQuant, err = boolArg(opt, fl.raw)
		case "overshootDeringing":
			jpeg(out).OvershootDeringing, err = boolArg(opt, fl.raw)
		case "optimiseScans", "optimizeScans":
			jpeg(out).OptimizeScans, err = boolArg(opt, fl.raw)
		case "quantisationTable", "quantizationTable":
			var n int
			if n, err = asInt(opt, fl.raw, 0, transform.MaxJPEGQuant); err == nil {
				jpeg(out).QuantTable = &n
			}
		case "compressionLevel":
			if f != transform.FormatPNG {
				b.ignore(opt, reasonUnsupported)
				continue
			}
			var n int
			if n, err = asInt(opt, fl.raw, 0, transform.MaxPNGLevel); err == nil {
				png(out).Compression = &n
			}
		case "adaptiveFiltering":
			png(out).AdaptiveFilter, err = boolArg(opt, fl.raw)
		case "palette":
			png(out).Quantize, err = boolArg(opt, fl.raw)
		case "colours", "colors":
			if f != transform.FormatPNG {
				b.ignore(opt, reasonUnsupported)
				continue
			}
			var n int
			if n, err = asInt(opt, fl.raw, 2, transform.MaxPaletteSize); err == nil {
				png(out).QuantizationColors = n
			}
		case "nearLossless":
			var v *bool
			if v, err = boolArg(opt, fl.raw); err == nil && *v {
				webp(out).Compression = transform.WebPNearLossless
			}
		case "smartSubsample":
			webp(out).SmartSubsample, err = boolArg(opt, fl.raw)
		case "preset":
			s, _ := asString(fl.raw)
			p, ok := webpPresets[s]
			if !ok {
				return dialect.InvalidArgument(opt, s, "default, photo, picture, drawing, icon or text")
			}
			webp(out).Preset = p
		default:
			b.ignore(truncate(opt), reasonUnsupported)
		}
		if err != nil {
			return err
		}
	}
	if force {
		b.editFormat = f
	}
	return nil
}

var webpPresets = map[string]transform.WebPPreset{
	"default": transform.WebPPresetDefault, "photo": transform.WebPPresetPhoto, "picture": transform.WebPPresetPicture,
	"drawing": transform.WebPPresetDrawing, "icon": transform.WebPPresetIcon, "text": transform.WebPPresetText,
}

func jpeg(o *transform.Output) *transform.JPEGOptions {
	if o.JPEG == nil {
		o.JPEG = &transform.JPEGOptions{}
	}
	return o.JPEG
}

func png(o *transform.Output) *transform.PNGOptions {
	if o.PNG == nil {
		o.PNG = &transform.PNGOptions{}
	}
	return o.PNG
}

func webp(o *transform.Output) *transform.WebPOptions {
	if o.WebP == nil {
		o.WebP = &transform.WebPOptions{}
	}
	return o.WebP
}

func avif(o *transform.Output) *transform.AVIFOptions {
	if o.AVIF == nil {
		o.AVIF = &transform.AVIFOptions{}
	}
	return o.AVIF
}

// finishFormat decides the output format as SIH does: edits.toFormat, else
// the top-level outputFormat, else the last format edit (sharp's jpeg()
// etc. switch the output format), else the source format. Per-format
// lossless and effort then apply if they belong to it, and the top-level
// effort applies to WebP output (0–6; SIH falls back to 4 when invalid).
func (b *builder) finishFormat(effort json.RawMessage) {
	out := &b.r.Output
	switch {
	case b.toFormat != transform.FormatUnset:
		out.Format = b.toFormat
	case b.outFormat != transform.FormatUnset:
		out.Format = b.outFormat
	case b.editFormat != transform.FormatUnset:
		out.Format = b.editFormat
	default:
	}
	if o := b.perFormat[out.Format]; o != nil && out.Format != transform.FormatUnset {
		out.Lossless = o.lossless
		if o.effort != 0 {
			out.Effort = o.effort
		}
	}
	if effort != nil && out.Format == transform.FormatWebP {
		e := 4
		if f, ok := asNumber(effort); ok && kind(effort) == kindNumber {
			if t := math.Trunc(f); t >= 0 && t <= 6 {
				e = int(t)
			}
		}
		out.Effort = normEffort(e, 0, 6)
	}
}
