package codec

import (
	"bytes"
	"encoding/binary"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// LegacyHeader is what the header of a legacy format decoded by
// ImageMagick declares (Format.IsMagick). It is read in Go so that probing
// never runs ImageMagick, which decodes the whole file even for a header,
// and so that the pixel limits hold before it runs.
type LegacyHeader struct {
	Width, Height int
	// Alpha reports a transparency channel (or, for XPM, a transparent
	// colour).
	Alpha bool
	// Grey reports a single-channel (greyscale or bilevel) image.
	Grey bool
	// Depth is the bits per sample: 1, 8 or 16.
	Depth int
}

// legacyMaxSide bounds each dimension a legacy header may declare; larger
// values are corrupt or hostile (ImageMagick's policy caps images at 16K
// per side anyway).
const legacyMaxSide = 1 << 16

// ParseLegacyHeader reads the header of f, one of the Format.IsMagick
// formats. Truncated or inconsistent headers fail with errs.KindCorrupt.
func ParseLegacyHeader(f Format, b []byte) (LegacyHeader, error) {
	var h LegacyHeader
	var err error
	switch f {
	case FormatTGA:
		h, err = tgaHeader(b)
	case FormatPCX:
		h, err = pcxHeader(b)
	case FormatQOI:
		h, err = qoiHeader(b)
	case FormatSGI:
		h, err = sgiHeader(b)
	case FormatSunRaster:
		h, err = sunHeader(b)
	case FormatXBM:
		h, err = xbmHeader(b)
	case FormatXPM:
		h, err = xpmHeader(b)
	case FormatPAM:
		h, err = pamHeader(b)
	default:
		return LegacyHeader{}, errs.Newf(errs.KindInternal, "codec: %s is not a legacy format", f)
	}
	if err != nil {
		return LegacyHeader{}, err
	}
	if h.Width < 1 || h.Height < 1 || h.Width > legacyMaxSide || h.Height > legacyMaxSide {
		return LegacyHeader{}, errs.Newf(errs.KindCorrupt, "codec: %s: %dx%d", f, h.Width, h.Height)
	}
	if h.Depth == 0 {
		h.Depth = 8
	}
	return h, nil
}

func legacyCorrupt(f Format, what string) error {
	return errs.Newf(errs.KindCorrupt, "codec: %s: %s", f, what)
}

// tgaHeader: image type at 2, width and height at 12 and 14 (LE), pixel
// depth at 16, alpha bits in the low nibble of the descriptor at 17.
func tgaHeader(b []byte) (LegacyHeader, error) {
	if !isTGA(b) {
		return LegacyHeader{}, legacyCorrupt(FormatTGA, "bad header")
	}
	le := binary.LittleEndian
	depth, alphaBits := b[16], b[17]&0x0F
	grey := b[2] == 3 || b[2] == 11
	h := LegacyHeader{
		Width:  int(le.Uint16(b[12:])),
		Height: int(le.Uint16(b[14:])),
		Grey:   grey,
		Alpha:  alphaBits > 0 && (depth == 32 || depth == 16),
	}
	if grey && depth == 16 {
		h.Alpha = true // 8-bit grey + 8-bit alpha
	}
	return h, nil
}

// pcxHeader: the window (xmin, ymin, xmax, ymax) at 4 (LE), bits per plane
// at 3 and planes at 65.
func pcxHeader(b []byte) (LegacyHeader, error) {
	if !isPCX(b) {
		return LegacyHeader{}, legacyCorrupt(FormatPCX, "bad header")
	}
	le := binary.LittleEndian
	xmin, ymin := int(le.Uint16(b[4:])), int(le.Uint16(b[6:]))
	xmax, ymax := int(le.Uint16(b[8:])), int(le.Uint16(b[10:]))
	if xmax < xmin || ymax < ymin {
		return LegacyHeader{}, legacyCorrupt(FormatPCX, "empty window")
	}
	planes := b[65]
	return LegacyHeader{
		Width:  xmax - xmin + 1,
		Height: ymax - ymin + 1,
		Alpha:  planes == 4 && b[3] == 8,
		Grey:   planes == 1 && b[3] == 1,
	}, nil
}

// qoiHeader: "qoif", width and height (BE), channels (3 or 4).
func qoiHeader(b []byte) (LegacyHeader, error) {
	if len(b) < 14 || !has(b, magicQOI) || (b[12] != 3 && b[12] != 4) {
		return LegacyHeader{}, legacyCorrupt(FormatQOI, "bad header")
	}
	be := binary.BigEndian
	return LegacyHeader{
		Width:  int(min(be.Uint32(b[4:]), legacyMaxSide+1)),
		Height: int(min(be.Uint32(b[8:]), legacyMaxSide+1)),
		Alpha:  b[12] == 4,
	}, nil
}

// sgiHeader: bytes per channel at 3, width, height and channels at 6, 8
// and 10 (BE).
func sgiHeader(b []byte) (LegacyHeader, error) {
	if !isSGI(b) {
		return LegacyHeader{}, legacyCorrupt(FormatSGI, "bad header")
	}
	be := binary.BigEndian
	z := be.Uint16(b[10:])
	if be.Uint16(b[4:]) < 3 {
		z = 1
	}
	if z < 1 {
		return LegacyHeader{}, legacyCorrupt(FormatSGI, "no channels")
	}
	return LegacyHeader{
		Width:  int(be.Uint16(b[6:])),
		Height: int(max(be.Uint16(b[8:]), 1)),
		Alpha:  z == 2 || z >= 4,
		Grey:   z <= 2,
		Depth:  int(b[3]) * 8,
	}, nil
}

// sunHeader: width, height and depth at 4, 8 and 12 (BE).
func sunHeader(b []byte) (LegacyHeader, error) {
	if len(b) < 32 || !has(b, magicSun) {
		return LegacyHeader{}, legacyCorrupt(FormatSunRaster, "bad header")
	}
	be := binary.BigEndian
	depth := be.Uint32(b[12:])
	switch depth {
	case 1, 8, 24, 32:
	default:
		return LegacyHeader{}, legacyCorrupt(FormatSunRaster, "bad depth")
	}
	return LegacyHeader{
		Width:  int(min(be.Uint32(b[4:]), legacyMaxSide+1)),
		Height: int(min(be.Uint32(b[8:]), legacyMaxSide+1)),
		Grey:   depth == 1,
	}, nil
}

// xbmHeader reads "#define <name>_width N" and "#define <name>_height N".
func xbmHeader(b []byte) (LegacyHeader, error) {
	if !isXBM(b) {
		return LegacyHeader{}, legacyCorrupt(FormatXBM, "bad header")
	}
	head := b[:min(len(b), 1024)]
	w, okW := defineValue(head, "_width")
	h, okH := defineValue(head, "_height")
	if !okW || !okH {
		return LegacyHeader{}, legacyCorrupt(FormatXBM, "missing _width or _height")
	}
	return LegacyHeader{Width: w, Height: h, Grey: true, Depth: 1}, nil
}

// defineValue finds "#define <name><suffix> <number>" in head.
func defineValue(head []byte, suffix string) (int, bool) {
	for line := range bytes.Lines(head) {
		f := bytes.Fields(line)
		if len(f) == 3 && string(f[0]) == "#define" && bytes.HasSuffix(f[1], []byte(suffix)) {
			n, err := strconv.Atoi(string(f[2]))
			return n, err == nil
		}
	}
	return 0, false
}

// xpmHeader reads the values string, the first C string of the file:
// "<width> <height> <colours> <chars per pixel> ...". A colour "None"
// (case-insensitive) makes the image transparent.
func xpmHeader(b []byte) (LegacyHeader, error) {
	head := b[:min(len(b), 4096)]
	i := bytes.IndexByte(head, '"')
	if !has(b, magicXPM) || i < 0 {
		return LegacyHeader{}, legacyCorrupt(FormatXPM, "bad header")
	}
	j := bytes.IndexByte(head[i+1:], '"')
	if j < 0 {
		return LegacyHeader{}, legacyCorrupt(FormatXPM, "unterminated values")
	}
	f := bytes.Fields(head[i+1 : i+1+j])
	if len(f) < 4 {
		return LegacyHeader{}, legacyCorrupt(FormatXPM, "short values")
	}
	var v [4]int
	for k := range v {
		n, err := strconv.Atoi(string(f[k]))
		if err != nil || n < 0 {
			return LegacyHeader{}, legacyCorrupt(FormatXPM, "bad values")
		}
		v[k] = n
	}
	if v[2] < 1 || v[3] < 1 || v[3] > 8 {
		return LegacyHeader{}, legacyCorrupt(FormatXPM, "bad colour table")
	}
	return LegacyHeader{
		Width:  v[0],
		Height: v[1],
		Alpha:  bytes.Contains(bytes.ToLower(b[:min(len(b), 1<<16)]), []byte("none")),
	}, nil
}

// pamHeader reads the PAM (P7) header tokens up to ENDHDR.
func pamHeader(b []byte) (LegacyHeader, error) {
	head := b[:min(len(b), 1024)]
	end := bytes.Index(head, []byte("ENDHDR"))
	if len(b) < 3 || string(b[:2]) != "P7" || end < 0 {
		return LegacyHeader{}, legacyCorrupt(FormatPAM, "bad header")
	}
	var h LegacyHeader
	var depth, maxval int
	var tuple string
	for line := range bytes.Lines(head[2:end]) {
		f := bytes.Fields(line)
		if len(f) < 2 || f[0][0] == '#' {
			continue
		}
		n, _ := strconv.Atoi(string(f[1]))
		switch string(f[0]) {
		case "WIDTH":
			h.Width = n
		case "HEIGHT":
			h.Height = n
		case "DEPTH":
			depth = n
		case "MAXVAL":
			maxval = n
		case "TUPLTYPE":
			tuple = string(f[1])
		}
	}
	if depth < 1 || depth > 4 || maxval < 1 || maxval > 65535 {
		return LegacyHeader{}, legacyCorrupt(FormatPAM, "bad depth or maxval")
	}
	h.Alpha = depth == 2 || depth == 4 || bytes.HasSuffix([]byte(tuple), []byte("_ALPHA"))
	h.Grey = depth <= 2
	h.Depth = 8
	if maxval > 255 {
		h.Depth = 16
	}
	return h, nil
}
