package negotiate

import "github.com/xantiagoma/xtandard-image/internal/transform"

// mediaType is a lowercase type/subtype pair.
type mediaType struct{ typ, sub string }

// mediaTypeOf returns the media type of an output format; ok is false for
// FormatUnset and FormatBest, which are not concrete formats.
func mediaTypeOf(f transform.Format) (mediaType, bool) {
	switch f {
	case transform.FormatJPEG:
		return mediaType{"image", "jpeg"}, true
	case transform.FormatPNG:
		return mediaType{"image", "png"}, true
	case transform.FormatWebP:
		return mediaType{"image", "webp"}, true
	case transform.FormatAVIF:
		return mediaType{"image", "avif"}, true
	case transform.FormatJXL:
		return mediaType{"image", "jxl"}, true
	case transform.FormatHEIC:
		return mediaType{"image", "heic"}, true
	case transform.FormatGIF:
		return mediaType{"image", "gif"}, true
	case transform.FormatTIFF:
		return mediaType{"image", "tiff"}, true
	case transform.FormatBMP:
		return mediaType{"image", "bmp"}, true
	case transform.FormatICO:
		return mediaType{"image", "x-icon"}, true
	case transform.FormatSVG:
		return mediaType{"image", "svg+xml"}, true
	case transform.FormatMP4:
		return mediaType{"video", "mp4"}, true
	case transform.FormatWebM:
		return mediaType{"video", "webm"}, true
	case transform.FormatHEVC:
		return mediaType{"video", "mp4"}, true
	case transform.FormatJSON:
		return mediaType{"application", "json"}, true
	default:
		return mediaType{}, false
	}
}

// ContentType returns the Content-Type value for an output format, or "" for
// FormatUnset and FormatBest. The strings are constants: no allocation.
func ContentType(f transform.Format) string {
	switch f {
	case transform.FormatJPEG:
		return "image/jpeg"
	case transform.FormatPNG:
		return "image/png"
	case transform.FormatWebP:
		return "image/webp"
	case transform.FormatAVIF:
		return "image/avif"
	case transform.FormatJXL:
		return "image/jxl"
	case transform.FormatHEIC:
		return "image/heic"
	case transform.FormatGIF:
		return "image/gif"
	case transform.FormatTIFF:
		return "image/tiff"
	case transform.FormatBMP:
		return "image/bmp"
	case transform.FormatICO:
		return "image/x-icon"
	case transform.FormatSVG:
		return "image/svg+xml"
	case transform.FormatMP4, transform.FormatHEVC:
		return "video/mp4"
	case transform.FormatWebM:
		return "video/webm"
	case transform.FormatJSON:
		return "application/json"
	default:
		return ""
	}
}

// rasterEncodable reports whether f is a still/animated raster format the
// encoder can produce (FORMATS §5). SVG, video, JSON and the pseudo formats
// are not.
func rasterEncodable(f transform.Format) bool {
	switch f {
	case transform.FormatJPEG, transform.FormatPNG, transform.FormatWebP, transform.FormatAVIF,
		transform.FormatJXL, transform.FormatHEIC, transform.FormatGIF, transform.FormatTIFF,
		transform.FormatBMP, transform.FormatICO:
		return true
	default:
		return false
	}
}

// supportsAlpha is the Alpha column of FORMATS §5 (GIF: 1-bit, counted).
func supportsAlpha(f transform.Format) bool {
	switch f {
	case transform.FormatPNG, transform.FormatWebP, transform.FormatAVIF, transform.FormatJXL,
		transform.FormatHEIC, transform.FormatGIF, transform.FormatTIFF, transform.FormatBMP,
		transform.FormatICO:
		return true
	default:
		return false
	}
}

// supportsLossless is the Lossless column of FORMATS §5. GIF is excluded: its
// 256-colour palette is lossy for most graphics.
func supportsLossless(f transform.Format) bool {
	switch f {
	case transform.FormatPNG, transform.FormatWebP, transform.FormatAVIF, transform.FormatJXL,
		transform.FormatTIFF, transform.FormatBMP, transform.FormatICO:
		return true
	default:
		return false
	}
}

// supportsAnimation is the Animation column of FORMATS §5. AVIF sequences are
// only written when the build supports them (Config.AVIFAnimation).
func (c *Config) supportsAnimation(f transform.Format) bool {
	switch f {
	case transform.FormatGIF, transform.FormatWebP, transform.FormatJXL:
		return true
	case transform.FormatAVIF:
		return c.AVIFAnimation
	default:
		return false
	}
}
