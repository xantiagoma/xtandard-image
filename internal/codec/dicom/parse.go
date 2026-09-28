package dicom

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

// Tags (group<<16 | element).
const (
	tagTransferSyntax   = 0x00020010
	tagSamplesPerPixel  = 0x00280002
	tagPhotometric      = 0x00280004
	tagPlanar           = 0x00280006
	tagNumberOfFrames   = 0x00280008
	tagRows             = 0x00280010
	tagColumns          = 0x00280011
	tagBitsAllocated    = 0x00280100
	tagBitsStored       = 0x00280101
	tagHighBit          = 0x00280102
	tagPixelRepr        = 0x00280103
	tagWindowCenter     = 0x00281050
	tagWindowWidth      = 0x00281051
	tagRescaleIntercept = 0x00281052
	tagRescaleSlope     = 0x00281053
	tagVOILUTFunction   = 0x00281056
	tagRedDescriptor    = 0x00281101
	tagGreenDescriptor  = 0x00281102
	tagBlueDescriptor   = 0x00281103
	tagRedData          = 0x00281201
	tagGreenData        = 0x00281202
	tagBlueData         = 0x00281203
	tagPixelData        = 0x7FE00010
	tagItem             = 0xFFFEE000
	tagItemDelim        = 0xFFFEE00D
	tagSeqDelim         = 0xFFFEE0DD
)

const (
	undefinedLength = 0xFFFFFFFF
	// maxElements bounds the elements read in one file, nested ones
	// included.
	maxElements = 1 << 20
	// maxDepth bounds sequence nesting.
	maxDepth = 32
	// maxInflated bounds a deflated dataset once inflated, and
	// maxInflateRatio its expansion (a floor of minInflated keeps small
	// files with large blank images readable).
	maxInflated     = 512 << 20
	maxInflateRatio = 200
	minInflated     = 16 << 20
)

// wanted reports the top-level elements whose value is read. Every other
// value in the file is skipped unread (privacy, see the package doc).
func wanted(tag uint32) bool {
	switch tag {
	case tagSamplesPerPixel, tagPhotometric, tagPlanar, tagNumberOfFrames, tagRows, tagColumns,
		tagBitsAllocated, tagBitsStored, tagHighBit, tagPixelRepr, tagWindowCenter, tagWindowWidth,
		tagRescaleIntercept, tagRescaleSlope, tagVOILUTFunction, tagRedDescriptor, tagGreenDescriptor,
		tagBlueDescriptor, tagRedData, tagGreenData, tagBlueData:
		return true
	}
	return false
}

// longVR reports the explicit VRs with a 4-byte length (after 2 reserved
// bytes).
func longVR(vr string) bool {
	switch vr {
	case "OB", "OD", "OF", "OL", "OV", "OW", "SQ", "SV", "UC", "UN", "UR", "UT", "UV":
		return true
	}
	return false
}

// parsed is a file read up to its pixel data.
type parsed struct {
	h Header
	// data holds the pixel data at pix (the file, or the inflated dataset
	// of a deflated transfer syntax); pixLen is its length, or -1 for
	// encapsulated (undefined length) pixel data.
	data   []byte
	order  binary.ByteOrder
	pix    int
	pixLen int64
}

// element is one element header; val is where its value starts.
type element struct {
	tag    uint32
	vr     string
	length uint32
	val    int
}

type reader struct {
	b        []byte
	pos      int
	order    binary.ByteOrder
	explicit bool
	count    *int
}

// next reads an element header at r.pos and moves past it (not past the
// value).
func (r *reader) next() (element, error) {
	if len(r.b)-r.pos < 8 {
		return element{}, errTruncated
	}
	*r.count++
	if *r.count > maxElements {
		return element{}, corrupt("too many elements")
	}
	b := r.b[r.pos:]
	tag := uint32(r.order.Uint16(b))<<16 | uint32(r.order.Uint16(b[2:]))
	e := element{tag: tag}
	switch {
	case tag>>16 == 0xFFFE || !r.explicit:
		e.length = r.order.Uint32(b[4:])
		r.pos += 8
	default:
		vr := b[4:6]
		if vr[0] < 'A' || vr[0] > 'Z' || vr[1] < 'A' || vr[1] > 'Z' {
			return element{}, corruptf("bad VR in (%04X,%04X)", tag>>16, tag&0xFFFF)
		}
		e.vr = string(vr)
		if longVR(e.vr) {
			if len(b) < 12 {
				return element{}, errTruncated
			}
			e.length = r.order.Uint32(b[8:])
			r.pos += 12
		} else {
			e.length = uint32(r.order.Uint16(b[6:]))
			r.pos += 8
		}
	}
	e.val = r.pos
	if e.length != undefinedLength && int64(e.length) > int64(len(r.b)-r.pos) {
		return element{}, corruptf("(%04X,%04X) runs past the end of the file", tag>>16, tag&0xFFFF)
	}
	return e, nil
}

var errTruncated = corrupt("truncated")

// parse reads the meta information and the top-level dataset up to the
// pixel data.
func parse(b []byte) (*parsed, error) {
	if !Sniff(b) {
		return nil, corrupt("no DICM preamble")
	}
	count := 0
	meta := &reader{b: b, pos: preambleLen + 4, order: binary.LittleEndian, explicit: true, count: &count}
	var ts string
	for len(b)-meta.pos >= 8 && binary.LittleEndian.Uint16(b[meta.pos:]) == 0x0002 {
		e, err := meta.next()
		if err != nil {
			return nil, err
		}
		if e.length == undefinedLength {
			return nil, corrupt("undefined length in the file meta information")
		}
		if e.tag == tagTransferSyntax {
			ts = text(b[e.val : e.val+int(e.length)])
		}
		meta.pos += int(e.length)
	}
	if ts == "" {
		return nil, corrupt("missing transfer syntax")
	}
	syn, ok := syntaxOf(ts)
	if !ok {
		return nil, unsupported("transfer syntax", "this DICOM transfer syntax is not supported")
	}
	p := &parsed{
		h: Header{
			Frames: 1, Samples: 1, HighBit: -1, RescaleSlope: 1, VOILUTFunction: "LINEAR",
			TransferSyntax: ts, Encoding: syn.enc,
		},
		data: b, order: binary.LittleEndian, pix: -1,
	}
	if syn.bigEndian {
		p.order = binary.BigEndian
	}
	start := meta.pos
	if syn.deflated {
		inflated, err := inflate(b[start:])
		if err != nil {
			return nil, err
		}
		p.data, start = inflated, 0
	}
	r := &reader{b: p.data, pos: start, order: p.order, explicit: syn.explicit, count: &count}
	if err := p.dataset(r, len(p.data), 0, true); err != nil {
		return nil, err
	}
	if p.pix < 0 {
		return nil, unsupported("no pixel data", "DICOM file without pixel data is not supported")
	}
	if err := p.h.check(); err != nil {
		return nil, err
	}
	encapsulated := p.pixLen < 0
	if encapsulated != syn.enc.Encapsulated() {
		return nil, corrupt("pixel data encapsulation does not match the transfer syntax")
	}
	return p, nil
}

// inflate inflates a deflated dataset, up to maxInflated bytes and
// maxInflateRatio times its size.
func inflate(b []byte) ([]byte, error) {
	limit := min(int64(maxInflated), max(int64(minInflated), int64(len(b))*maxInflateRatio))
	zr := flate.NewReader(bytes.NewReader(b))
	defer func() { _ = zr.Close() }()
	var out bytes.Buffer
	n, err := io.Copy(&out, io.LimitReader(zr, limit+1))
	switch {
	case err != nil && !errors.Is(err, io.ErrUnexpectedEOF):
		return nil, corrupt("bad deflated dataset")
	case n > limit:
		return nil, corrupt("deflated dataset too large")
	}
	return out.Bytes(), nil
}

// dataset reads elements until end (a defined length) or, when end < 0,
// an item delimiter. At the top level (top) it reads the wanted values and
// stops at the pixel data.
func (p *parsed) dataset(r *reader, end, depth int, top bool) error {
	for end < 0 || r.pos < end {
		if end >= 0 && end-r.pos < 8 {
			// Trailing padding shorter than an element header.
			r.pos = end
			return nil
		}
		e, err := r.next()
		if err != nil {
			return err
		}
		switch e.tag {
		case tagItemDelim:
			if end >= 0 || e.length != 0 {
				return corrupt("unexpected item delimiter")
			}
			return nil
		case tagSeqDelim, tagItem:
			return corrupt("unexpected item or sequence delimiter")
		}
		if top && e.tag == tagPixelData {
			p.pix = e.val
			p.pixLen = int64(e.length)
			if e.length == undefinedLength {
				p.pixLen = -1
			}
			return nil
		}
		if e.length == undefinedLength {
			if e.vr != "" && e.vr != "SQ" && e.vr != "UN" && e.tag != tagPixelData {
				return corruptf("undefined length for (%04X,%04X)", e.tag>>16, e.tag&0xFFFF)
			}
			// A sequence (or encapsulated pixel data nested in one): its
			// items are skipped. UN sequences are implicit VR little endian.
			sub := r
			if e.vr == "UN" {
				sub = &reader{b: r.b, pos: r.pos, order: binary.LittleEndian, count: r.count}
			}
			if err := p.sequence(sub, depth+1); err != nil {
				return err
			}
			r.pos = sub.pos
			continue
		}
		if end >= 0 && e.val+int(e.length) > end {
			return corruptf("(%04X,%04X) runs past its item", e.tag>>16, e.tag&0xFFFF)
		}
		if top && wanted(e.tag) {
			p.set(e.tag, r.b[e.val:e.val+int(e.length)])
		}
		r.pos = e.val + int(e.length)
	}
	return nil
}

// sequence skips the items of an undefined-length sequence up to its
// delimiter. Items of undefined length are walked (their elements are
// skipped, never read); items of defined length are skipped whole.
func (p *parsed) sequence(r *reader, depth int) error {
	if depth > maxDepth {
		return corrupt("sequences nested too deep")
	}
	for {
		e, err := r.next()
		if err != nil {
			return err
		}
		switch e.tag {
		case tagSeqDelim:
			return nil
		case tagItem:
			if e.length == undefinedLength {
				if err := p.dataset(r, -1, depth, false); err != nil {
					return err
				}
				continue
			}
			r.pos = e.val + int(e.length)
		default:
			return corruptf("(%04X,%04X) inside a sequence", e.tag>>16, e.tag&0xFFFF)
		}
	}
}

// set stores one wanted value. Malformed values are ignored (the element
// then keeps its default and check() reports what is missing).
func (p *parsed) set(tag uint32, v []byte) {
	h := &p.h
	us := func() int {
		if len(v) < 2 {
			return 0
		}
		return int(p.order.Uint16(v))
	}
	switch tag {
	case tagSamplesPerPixel:
		h.Samples = us()
	case tagPhotometric:
		h.Photometric = text(v)
	case tagPlanar:
		h.Planar = us() == 1
	case tagNumberOfFrames:
		if n, err := strconv.Atoi(text(v)); err == nil {
			h.Frames = n
		}
	case tagRows:
		h.Height = us()
	case tagColumns:
		h.Width = us()
	case tagBitsAllocated:
		h.BitsAllocated = us()
	case tagBitsStored:
		h.BitsStored = us()
	case tagHighBit:
		if len(v) >= 2 {
			h.HighBit = us()
		}
	case tagPixelRepr:
		h.Signed = us() == 1
	case tagWindowCenter:
		if c, ok := decimal(v); ok {
			h.WindowCenter = c
			h.HasWindow = h.WindowWidth > 0
		}
	case tagWindowWidth:
		if w, ok := decimal(v); ok && w > 0 {
			h.WindowWidth = w
			h.HasWindow = true
		}
	case tagRescaleIntercept:
		if f, ok := decimal(v); ok {
			h.RescaleIntercept = f
		}
	case tagRescaleSlope:
		if f, ok := decimal(v); ok && f != 0 {
			h.RescaleSlope = f
		}
	case tagVOILUTFunction:
		if s := text(v); s != "" {
			h.VOILUTFunction = s
		}
	case tagRedDescriptor, tagGreenDescriptor, tagBlueDescriptor:
		if len(v) >= 6 {
			pal := p.palette()
			i := int(tag - tagRedDescriptor)
			for k := range 3 {
				pal.desc[i][k] = p.order.Uint16(v[2*k:])
			}
			pal.hasDesc[i] = true
		}
	case tagRedData, tagGreenData, tagBlueData:
		pal := p.palette()
		pal.data[int(tag-tagRedData)] = v
	}
	if h.HasWindow && h.WindowWidth <= 0 {
		h.HasWindow = false
	}
}

func (p *parsed) palette() *palette {
	if p.h.palette == nil {
		p.h.palette = &palette{order: p.order}
	}
	return p.h.palette
}

// text is a string value without its padding (spaces, NUL), first value
// only.
func text(v []byte) string {
	s := string(v)
	if i := strings.IndexByte(s, '\\'); i >= 0 {
		s = s[:i]
	}
	return strings.Trim(s, " \x00")
}

// decimal parses the first value of a DS string.
func decimal(v []byte) (float64, bool) {
	f, err := strconv.ParseFloat(text(v), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}
