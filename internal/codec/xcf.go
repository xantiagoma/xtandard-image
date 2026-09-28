package codec

import (
	"encoding/binary"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// GIMP XCF (FormatXCF) has no stored composite: the layers must be
// flattened. ImageMagick's XCF coder (magickload, allowed by the base
// image's policy.xml) decodes one layer's pixels at a time; the layer
// table, the image properties and every layer's placement, opacity,
// visibility and blend mode are read here, in Go, so probing never runs
// ImageMagick and the loader composites the layers itself (FlattenXCF).
// Reference: GIMP devel-docs/xcf.txt; ImageMagick coders/xcf.c.

const (
	magicXCF = "gimp xcf "
	// xcfHeaderLen is the signature, the version and the NUL: "gimp xcf
	// file\0" or "gimp xcf v011\0".
	xcfHeaderLen = 14
	// xcfMaxSide is ImageMagick's bound on the canvas (and ours on a
	// layer).
	xcfMaxSide = 262_144
	// MaxXCFLayers bounds the layer table. Each visible top-level layer is
	// one ImageMagick decode.
	MaxXCFLayers = 256
	// xcfMaxProps bounds one property list.
	xcfMaxProps = 4096
	// xcfPrecisionU8 is GIMP_PRECISION_U8_NON_LINEAR, the only precision
	// ImageMagick decodes.
	xcfPrecisionU8 = 150
)

// XCF property types (xcf.txt "PROPERTIES").
const (
	xcfPropEnd          = 0
	xcfPropFloating     = 5
	xcfPropOpacity      = 6
	xcfPropMode         = 7
	xcfPropVisible      = 8
	xcfPropOffsets      = 15
	xcfPropCompression  = 17
	xcfPropGroupItem    = 29
	xcfPropItemPath     = 30
	xcfCompressRLE      = 1
	xcfBaseRGB          = 0
	xcfBaseGrey         = 1
	xcfBaseIndexed      = 2
	xcfLayerIndexed     = 4
	xcfMaxLayerNameLen  = 64 << 10
	xcfMaxPropertyBytes = 16 << 20
)

// XCFLayer is one entry of an XCF layer table, as GIMP stores it.
type XCFLayer struct {
	// Width and Height are the layer's size, X and Y its offset on the
	// canvas (may be negative or reach past it).
	Width, Height, X, Y int
	// Alpha reports a layer type with an alpha channel (RGBA, GRAYA).
	Alpha bool
	// Opacity is 0-255 (PROP_OPACITY; 255 when absent).
	Opacity uint8
	// Mode is the GimpLayerMode (PROP_MODE; 0, legacy normal, when absent).
	Mode uint32
	// Visible reports PROP_VISIBLE (false when absent, as in GIMP).
	Visible bool
	// Group reports a layer group: its pixels are the group's projection.
	Group bool
	// Depth is the length of the item path: 0 or 1 for a top-level item,
	// more for a layer inside a group.
	Depth int
	// Offset is where the layer structure starts in the file.
	Offset int64
}

// TopLevel reports a layer that is not inside a group.
func (l *XCFLayer) TopLevel() bool { return l.Depth <= 1 }

// XCFHeader is what the XCF header, image properties and layer table
// declare.
type XCFHeader struct {
	// Version is the file format version: 0 for "file", else the vNNN
	// number.
	Version int
	Width   int
	Height  int
	// Grey reports a greyscale image (base type 1); otherwise RGB.
	Grey bool
	// Layers is the layer table in file order: the top of the stack first.
	Layers []XCFLayer
	// TableOffset is where the layer pointer table starts, PointerSize the
	// width of one pointer (8 from version 11, else 4).
	TableOffset int
	PointerSize int
}

// Composited returns the layers that make up the flattened image, bottom
// first: visible top-level layers (a group's projection stands for its
// children). index is the layer's position in Layers.
func (h *XCFHeader) Composited() (layers []XCFLayer, index []int) {
	for i := len(h.Layers) - 1; i >= 0; i-- {
		if l := &h.Layers[i]; l.Visible && l.TopLevel() && l.Opacity > 0 {
			layers = append(layers, *l)
			index = append(index, i)
		}
	}
	return layers, index
}

// HasAlpha reports whether the flattened image can be transparent: unless
// the bottom composited layer is an opaque, fully opaque normal layer that
// covers the whole canvas.
func (h *XCFHeader) HasAlpha() bool {
	ls, _ := h.Composited()
	if len(ls) == 0 {
		return true
	}
	b := &ls[0]
	return b.Alpha || b.Opacity != 255 || b.X > 0 || b.Y > 0 ||
		b.X+b.Width < h.Width || b.Y+b.Height < h.Height ||
		(b.Mode != 0 && b.Mode != xcfModeNormal)
}

// isXCF recognises the XCF signature.
func isXCF(b []byte) bool {
	return len(b) >= xcfHeaderLen && has(b, magicXCF) && b[xcfHeaderLen-1] == 0
}

// xcfReader reads big-endian fields, remembering the first overrun.
type xcfReader struct {
	b   []byte
	off int
	bad bool
}

func (r *xcfReader) u32() uint32 {
	if r.off < 0 || r.off+4 > len(r.b) {
		r.bad = true
		return 0
	}
	v := binary.BigEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v
}

// ptr reads a file offset of size bytes (4 or 8); offsets beyond the file
// are reported as bad.
func (r *xcfReader) ptr(size int) int64 {
	var v uint64
	if size == 8 {
		hi := uint64(r.u32())
		v = hi<<32 | uint64(r.u32())
	} else {
		v = uint64(r.u32())
	}
	if v > uint64(len(r.b)) {
		r.bad = true
		return 0
	}
	return int64(v) //nolint:gosec // v <= len(r.b), checked above
}

func (r *xcfReader) skip(n int) {
	if n < 0 || r.off+n > len(r.b) {
		r.bad = true
		return
	}
	r.off += n
}

func xcfCorrupt(msg string) error { return errs.New(errs.KindCorrupt, "codec: xcf: "+msg) }

func xcfUnsupported(public, msg string) error {
	return errs.WithPublic(errs.New(errs.KindUnsupported, "codec: xcf: "+msg), public)
}

// ParseXCFHeader reads the XCF header, the image properties and every
// layer's header and properties. It refuses what ImageMagick's coder
// cannot decode correctly, with errs.KindUnsupported: indexed images,
// precisions other than 8-bit gamma, tiles that are not RLE-coded (zlib
// needs GIMP; uncompressed tiles are misread by the coder) and versions 4
// to 10 (their 32-bit offsets are read as 64-bit ones). Truncated or
// inconsistent structures fail with errs.KindCorrupt.
func ParseXCFHeader(data []byte) (XCFHeader, error) {
	if !isXCF(data) {
		return XCFHeader{}, xcfCorrupt("bad signature")
	}
	var h XCFHeader
	switch v := string(data[9:13]); {
	case v == "file":
		h.Version = 0
	case v[0] == 'v':
		n, err := strconv.Atoi(v[1:])
		if err != nil || n < 0 {
			return XCFHeader{}, xcfCorrupt("bad version")
		}
		h.Version = n
	default:
		return XCFHeader{}, xcfCorrupt("bad version")
	}
	if h.Version >= 4 && h.Version <= 10 {
		return XCFHeader{}, xcfUnsupported("this GIMP XCF version is not supported",
			"version "+strconv.Itoa(h.Version))
	}
	h.PointerSize = 4
	if h.Version >= 11 {
		h.PointerSize = 8
	}
	r := &xcfReader{b: data, off: xcfHeaderLen}
	w, ht, base := r.u32(), r.u32(), r.u32()
	if r.bad || w == 0 || ht == 0 || w > xcfMaxSide || ht > xcfMaxSide {
		return XCFHeader{}, xcfCorrupt("bad canvas size")
	}
	h.Width, h.Height = int(w), int(ht)
	switch base {
	case xcfBaseRGB:
	case xcfBaseGrey:
		h.Grey = true
	case xcfBaseIndexed:
		return XCFHeader{}, xcfUnsupported("indexed GIMP XCF images are not supported", "indexed image")
	default:
		return XCFHeader{}, xcfCorrupt("bad base type")
	}
	if h.Version >= 4 {
		// ImageMagick's mapping: 0 in v4 and 100 before v7 mean 8-bit gamma.
		p := r.u32()
		if (p == 0 && h.Version == 4) || (p == 100 && h.Version < 7) {
			p = xcfPrecisionU8
		}
		if p != xcfPrecisionU8 {
			return XCFHeader{}, xcfUnsupported("only 8-bit GIMP XCF images are supported",
				"precision "+strconv.FormatUint(uint64(p), 10))
		}
	}
	compression := -1
	err := xcfProps(r, func(typ uint32, p []byte) error {
		if typ == xcfPropCompression {
			if len(p) != 1 {
				return xcfCorrupt("bad compression property")
			}
			compression = int(p[0])
		}
		return nil
	})
	if err != nil {
		return XCFHeader{}, err
	}
	if compression != xcfCompressRLE {
		return XCFHeader{}, xcfUnsupported("only RLE-compressed GIMP XCF files are supported",
			"compression "+strconv.Itoa(compression))
	}
	h.TableOffset = r.off
	for {
		off := r.ptr(h.PointerSize)
		if r.bad {
			return XCFHeader{}, xcfCorrupt("truncated layer table")
		}
		if off == 0 {
			break
		}
		if len(h.Layers) == MaxXCFLayers {
			return XCFHeader{}, errs.Newf(errs.KindLimitExceeded, "codec: xcf with more than %d layers", MaxXCFLayers)
		}
		l, err := parseXCFLayer(data, off)
		if err != nil {
			return XCFHeader{}, err
		}
		h.Layers = append(h.Layers, l)
	}
	if len(h.Layers) == 0 {
		return XCFHeader{}, xcfCorrupt("no layers")
	}
	return h, nil
}

// xcfProps walks a property list up to PROP_END, handing each payload to
// fn.
func xcfProps(r *xcfReader, fn func(typ uint32, payload []byte) error) error {
	for range xcfMaxProps {
		typ, n := r.u32(), r.u32()
		if r.bad {
			return xcfCorrupt("truncated property list")
		}
		if typ == xcfPropEnd {
			return nil
		}
		if n > xcfMaxPropertyBytes {
			return xcfCorrupt("property too large")
		}
		start := r.off
		r.skip(int(n))
		if r.bad {
			return xcfCorrupt("truncated property")
		}
		if err := fn(typ, r.b[start:r.off]); err != nil {
			return err
		}
	}
	return xcfCorrupt("too many properties")
}

// parseXCFLayer reads one layer header and its properties.
func parseXCFLayer(data []byte, off int64) (XCFLayer, error) {
	r := &xcfReader{b: data, off: int(off)}
	w, ht, typ, nameLen := r.u32(), r.u32(), r.u32(), r.u32()
	if r.bad || w == 0 || ht == 0 || w > xcfMaxSide || ht > xcfMaxSide {
		return XCFLayer{}, xcfCorrupt("bad layer size")
	}
	switch {
	case typ >= xcfLayerIndexed && typ <= xcfLayerIndexed+1:
		return XCFLayer{}, xcfUnsupported("indexed GIMP XCF images are not supported", "indexed layer")
	case typ > 3:
		return XCFLayer{}, xcfCorrupt("bad layer type")
	case nameLen > xcfMaxLayerNameLen:
		return XCFLayer{}, xcfCorrupt("layer name too long")
	}
	r.skip(int(nameLen))
	l := XCFLayer{Width: int(w), Height: int(ht), Alpha: typ%2 == 1, Opacity: 255, Offset: off}
	be := binary.BigEndian
	err := xcfProps(r, func(p uint32, b []byte) error {
		switch p {
		case xcfPropOpacity:
			if len(b) != 4 {
				return xcfCorrupt("bad opacity property")
			}
			l.Opacity = uint8(min(be.Uint32(b), 255))
		case xcfPropMode:
			if len(b) != 4 {
				return xcfCorrupt("bad mode property")
			}
			l.Mode = be.Uint32(b)
		case xcfPropVisible:
			if len(b) != 4 {
				return xcfCorrupt("bad visible property")
			}
			l.Visible = be.Uint32(b) != 0
		case xcfPropOffsets:
			if len(b) != 8 {
				return xcfCorrupt("bad offsets property")
			}
			// Offsets are signed 32-bit values.
			l.X, l.Y = int(int32(be.Uint32(b))), int(int32(be.Uint32(b[4:]))) //nolint:gosec // two's complement on purpose
		case xcfPropGroupItem:
			l.Group = true
		case xcfPropItemPath:
			if len(b)%4 != 0 {
				return xcfCorrupt("bad item path property")
			}
			l.Depth = len(b) / 4
		case xcfPropFloating:
			// A floating selection is pasted onto another drawable; it is
			// not part of the flattened image until anchored.
			l.Visible = false
		}
		return nil
	})
	if err != nil {
		return XCFLayer{}, err
	}
	if l.X < -xcfMaxSide || l.X > xcfMaxSide || l.Y < -xcfMaxSide || l.Y > xcfMaxSide {
		return XCFLayer{}, xcfCorrupt("bad layer offset")
	}
	return l, nil
}

// XCFLayerFile returns data with its layer table reduced to layer i: the
// input ImageMagick decodes that layer from, and only that layer (the
// coder otherwise decodes every layer below the one asked for). dst must
// be len(data) bytes; it is overwritten.
func XCFLayerFile(dst, data []byte, h *XCFHeader, i int) error {
	if len(dst) != len(data) || i < 0 || i >= len(h.Layers) {
		return errs.New(errs.KindInternal, "codec: xcf layer file arguments")
	}
	copy(dst, data)
	t := h.TableOffset
	if t+2*h.PointerSize > len(dst) {
		return xcfCorrupt("truncated layer table")
	}
	off := uint64(h.Layers[i].Offset) //nolint:gosec // a file offset: 0 <= Offset <= len(data)
	if h.PointerSize == 8 {
		binary.BigEndian.PutUint64(dst[t:], off)
		binary.BigEndian.PutUint64(dst[t+8:], 0)
	} else {
		binary.BigEndian.PutUint32(dst[t:], uint32(off)) //nolint:gosec // read from a 32-bit pointer
		binary.BigEndian.PutUint32(dst[t+4:], 0)
	}
	return nil
}
