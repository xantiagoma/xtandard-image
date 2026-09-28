// Package fits decodes FITS (Flexible Image Transport System) images in
// pure Go: astronomy data, drawn for display.
//
// The image is the first HDU with at least two axes: the primary array or,
// when the primary HDU is empty (NAXIS = 0, the usual multi-extension
// layout), the first IMAGE extension or tile-compressed image (a BINTABLE
// with ZIMAGE = T, as fpack writes). TABLE and other BINTABLE extensions
// are skipped. Axes beyond the third must have length 1. A third axis of
// length 3 is drawn as RGB (the colour composites Siril and PixInsight
// write); any other plane count draws plane 0 (for example the first of
// the four WFPC2 chips).
//
// Samples: BITPIX 8 (unsigned), 16, 32 and 64 (signed) and -32/-64 (IEEE
// float), big-endian, scaled to physical values with BZERO and BSCALE.
// Integer BLANK values and NaNs are "no data": excluded from the
// statistics and drawn black. FITS stores the first row at the bottom
// (DS9, Pillow and astropy display it so), so rows are flipped to come out
// upright.
//
// Tile compression (the FITS tile-compression convention, fpack):
// RICE_1, GZIP_1, GZIP_2 and NOCOMPRESS tiles, integer images and floating
// point images quantised with NO_DITHER, SUBTRACTIVE_DITHER_1 or
// SUBTRACTIVE_DITHER_2 (per-tile ZSCALE/ZZERO, the standard 10 000-value
// dither sequence seeded by ZDITHER0), and lossless GZIP_COMPRESSED_DATA
// float tiles. HCOMPRESS_1 and PLIO_1 are not supported.
//
// Stretch (Decode always returns 8-bit RGB): an 8-bit image without BZERO
// or BSCALE is shown as is (bytes are grey levels). Anything else gets the
// automatic screen transfer function of PixInsight and Siril: values are
// normalised to [0, 1] over the finite samples of the drawn planes
// (x = (v − min)/(max − min), linked across RGB); from a strided subsample
// of at most 1<<20 of them, the median med and the normalised median
// absolute deviation mad = 1.4826·median(|x − med|) give the shadows clip
// c0 = clamp(med − 2.8·mad, 0, 1) and the midtones balance
// m = MTF(0.25, med − c0), where MTF(m, x) = (m − 1)x / ((2m − 1)x − m);
// each sample is drawn as 255·MTF(m, clamp((x − c0)/(1 − c0), 0, 1)), so
// the sky background lands at a quarter of full brightness and faint
// structure becomes visible. A constant image is mid-grey; when the
// balance is degenerate the stretch is linear between c0 and 1.
//
// Everything is bounded: headers (1000 blocks), HDUs scanned (1000), sizes
// computed with overflow checks, the data of the image HDU must be present
// in full, and Options.MaxPixels caps the image before anything is
// allocated.
package fits

import (
	"encoding/binary"
	"math"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Sniff reports whether b starts like a FITS file: the first card is
// "SIMPLE  = " with the logical value T.
func Sniff(b []byte) bool {
	if len(b) < cardSize || string(b[:10]) != "SIMPLE  = " {
		return false
	}
	for _, c := range b[10:cardSize] {
		if c != ' ' {
			return c == 'T'
		}
	}
	return false
}

// Config is what the header of the image HDU declares.
type Config struct {
	Width, Height int
	// Colour reports three planes (NAXIS3 = 3), drawn as RGB.
	Colour bool
	// Planes is NAXIS3 (1 when absent); counts other than 3 draw plane 0.
	Planes int
	// Bitpix is the sample type of the image (ZBITPIX when tile-compressed).
	Bitpix int
	// HDU is the index of the image HDU (0 is the primary).
	HDU int
	// Compressed reports a tile-compressed image (ZIMAGE = T).
	Compressed bool
}

// Options control Decode.
type Options struct {
	// MaxPixels bounds Width*Height (errs.KindLimitExceeded). Zero means
	// raster.DefaultMaxPixels.
	MaxPixels int64
}

// imageHDU is the image found by locate.
type imageHDU struct {
	Config
	h header
	// data is the HDU's data unit (exactly its declared size).
	data []byte
}

// DecodeConfig reads the headers up to the image HDU. Malformed headers
// fail with errs.KindCorrupt, unsupported layouts with
// errs.KindUnsupported. Only the image HDU's data length is checked
// against the file, not its contents.
func DecodeConfig(b []byte) (Config, error) {
	im, err := locate(b)
	if err != nil {
		return Config{}, err
	}
	return im.Config, nil
}

// locate walks the HDUs to the first image.
func locate(b []byte) (imageHDU, error) {
	if !Sniff(b) {
		return imageHDU{}, corrupt("not a FITS file")
	}
	var off int64
	for i := range maxHDUs {
		if off >= int64(len(b)) {
			break
		}
		h, err := parseHeader(b, off)
		if err != nil {
			return imageHDU{}, err
		}
		size, err := h.dataSize(i == 0)
		if err != nil {
			return imageHDU{}, err
		}
		start := off + h.size
		if size > int64(len(b))-start {
			return imageHDU{}, corrupt("truncated data")
		}
		im, ok, err := h.image(i)
		if err != nil {
			return imageHDU{}, err
		}
		if ok {
			im.data = b[start : start+size]
			return im, nil
		}
		off = start + padded(size)
	}
	return imageHDU{}, errs.WithPublic(errs.New(errs.KindUnsupported, "fits: no image HDU"),
		"FITS file without an image is not supported")
}

// dataSize is the declared size of the HDU's data unit:
// |BITPIX|/8 × GCOUNT × (PCOUNT + NAXIS1 × … × NAXISn).
func (h *header) dataSize(primary bool) (int64, error) {
	bitpix, ok, err := h.integer("BITPIX")
	if err != nil {
		return 0, err
	}
	switch {
	case !ok:
		return 0, corrupt("missing BITPIX")
	case bitpix != 8 && bitpix != 16 && bitpix != 32 && bitpix != 64 && bitpix != -32 && bitpix != -64:
		return 0, corruptf("bad BITPIX %d", bitpix)
	}
	ax, err := h.axes("NAXIS")
	if err != nil {
		return 0, err
	}
	if len(ax) == 0 {
		return 0, nil
	}
	n := int64(1)
	for _, a := range ax {
		if n, err = mul(n, a); err != nil {
			return 0, err
		}
	}
	pcount, gcount := int64(0), int64(1)
	if !primary {
		if pcount, err = h.integerOr("PCOUNT", 0); err != nil {
			return 0, err
		}
		if gcount, err = h.integerOr("GCOUNT", 1); err != nil {
			return 0, err
		}
		if pcount < 0 || gcount < 0 || pcount > maxBytes {
			return 0, corrupt("bad PCOUNT or GCOUNT")
		}
	}
	if n+pcount > maxBytes {
		return 0, corrupt("size overflow")
	}
	if n, err = mul(n+pcount, gcount); err != nil {
		return 0, err
	}
	return mul(n, abs64(bitpix)/8)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// image reports whether the HDU holds the image and describes it.
func (h *header) image(index int) (imageHDU, bool, error) {
	im := imageHDU{h: *h, HDU: index}
	prefix := "NAXIS"
	bitpixKey := "BITPIX"
	if index > 0 {
		switch h.str("XTENSION") {
		case "IMAGE":
		case "BINTABLE":
			if !h.logical("ZIMAGE") {
				return imageHDU{}, false, nil
			}
			im.Compressed = true
			prefix, bitpixKey = "ZNAXIS", "ZBITPIX"
		default:
			return imageHDU{}, false, nil
		}
	}
	ax, err := h.axes(prefix)
	if err != nil {
		return imageHDU{}, false, err
	}
	if len(ax) < 2 || ax[0] < 1 || ax[1] < 1 {
		return imageHDU{}, false, nil
	}
	for k, a := range ax {
		if k >= 3 && a != 1 {
			return imageHDU{}, false, errs.WithPublic(
				errs.Newf(errs.KindUnsupported, "fits: %d-dimensional image", len(ax)),
				"FITS images with more than three axes are not supported")
		}
	}
	if ax[0] > maxSide || ax[1] > maxSide {
		return imageHDU{}, false, corruptf("%dx%d", ax[0], ax[1])
	}
	im.Width, im.Height, im.Planes = int(ax[0]), int(ax[1]), 1
	if len(ax) >= 3 {
		if ax[2] < 1 {
			return imageHDU{}, false, nil // an empty cube
		}
		im.Planes = int(min(ax[2], math.MaxInt32))
	}
	im.Colour = im.Planes == 3
	bitpix, ok, err := h.integer(bitpixKey)
	if err != nil {
		return imageHDU{}, false, err
	}
	switch bitpix {
	case 8, 16, 32, 64, -32, -64:
	default:
		if !ok {
			return imageHDU{}, false, corruptf("missing %s", bitpixKey)
		}
		return imageHDU{}, false, corruptf("bad %s %d", bitpixKey, bitpix)
	}
	im.Bitpix = int(bitpix)
	return im, true, nil
}

// drawn is the number of planes drawn: 3 for colour, else 1.
func (c *Config) drawn() int {
	if c.Colour {
		return 3
	}
	return 1
}

// scaling is BZERO, BSCALE and the integer BLANK of an image.
type scaling struct {
	zero, scale float64
	blank       int64
	hasBlank    bool
}

func (h *header) scaling() (scaling, error) {
	var s scaling
	var err error
	if s.zero, err = h.realOr("BZERO", 0); err != nil {
		return s, err
	}
	if s.scale, err = h.realOr("BSCALE", 1); err != nil {
		return s, err
	}
	s.blank, s.hasBlank, err = h.integer("BLANK")
	return s, err
}

// identity reports an 8-bit image shown as is.
func (s *scaling) identity(bitpix int) bool {
	return bitpix == 8 && s.zero == 0 && s.scale == 1
}

// Decode decodes the image: 8-bit RGB, stretched for display (package
// doc).
func Decode(b []byte, o Options) (raster.Decoded, error) {
	im, err := locate(b)
	if err != nil {
		return raster.Decoded{}, err
	}
	limit := o.MaxPixels
	if limit <= 0 {
		limit = raster.DefaultMaxPixels
	}
	if int64(im.Width)*int64(im.Height) > limit {
		return raster.Decoded{}, errs.Newf(errs.KindLimitExceeded, "fits: %dx%d exceeds %d pixels",
			im.Width, im.Height, limit)
	}
	sc, err := im.h.scaling()
	if err != nil {
		return raster.Decoded{}, err
	}
	var src samples
	if im.Compressed {
		if src, err = decodeTiles(&im, &sc); err != nil {
			return raster.Decoded{}, err
		}
	} else {
		src = newRawSamples(&im, &sc)
	}
	return raster.Decoded{Raw: render(src, im.Width, im.Height, im.drawn(), sc.identity(im.Bitpix))}, nil
}

// samples gives the physical value of sample i (row-major, first row at
// the bottom) of plane p; NaN is "no data".
type samples func(p, i int) float64

// newRawSamples reads an uncompressed data unit.
func newRawSamples(im *imageHDU, sc *scaling) samples {
	be := binary.BigEndian
	n := im.Width * im.Height
	d := im.data
	switch im.Bitpix {
	case 8:
		return func(p, i int) float64 {
			v := int64(d[p*n+i])
			if sc.hasBlank && v == sc.blank {
				return math.NaN()
			}
			return sc.zero + sc.scale*float64(v)
		}
	case 16:
		return func(p, i int) float64 {
			v := s16(be.Uint16(d[(p*n+i)*2:]))
			if sc.hasBlank && v == sc.blank {
				return math.NaN()
			}
			return sc.zero + sc.scale*float64(v)
		}
	case 32:
		return func(p, i int) float64 {
			v := s32(be.Uint32(d[(p*n+i)*4:]))
			if sc.hasBlank && v == sc.blank {
				return math.NaN()
			}
			return sc.zero + sc.scale*float64(v)
		}
	case 64:
		return func(p, i int) float64 {
			v := s64(be.Uint64(d[(p*n+i)*8:]))
			if sc.hasBlank && v == sc.blank {
				return math.NaN()
			}
			return sc.zero + sc.scale*float64(v)
		}
	case -32:
		return func(p, i int) float64 {
			return sc.zero + sc.scale*float64(math.Float32frombits(be.Uint32(d[(p*n+i)*4:])))
		}
	default: // -64
		return func(p, i int) float64 {
			return sc.zero + sc.scale*math.Float64frombits(be.Uint64(d[(p*n+i)*8:]))
		}
	}
}
