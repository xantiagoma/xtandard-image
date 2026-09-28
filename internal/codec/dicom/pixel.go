package dicom

import (
	"bytes"
	"encoding/binary"

	"github.com/xantiagoma/xtandard-image/internal/codec/raster"
	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Decode renders the first frame of native (including deflated) and RLE
// pixel data to 8-bit RGB (Header.Render). Frames stored for an image
// codec fail with errs.KindUnsupported: the caller decodes FirstFrame's
// codestream and calls Render.
func Decode(b []byte, o Options) (*raster.Raw, error) {
	p, err := parse(b)
	if err != nil {
		return nil, err
	}
	h := &p.h
	if err := h.pixelLimit(o); err != nil {
		return nil, err
	}
	var frame []byte
	order := p.order
	switch h.Encoding {
	case EncodingNative, EncodingDeflated:
		frame, err = p.nativeFrame()
	case EncodingRLE:
		var frag []byte
		if frag, err = p.firstFrame(); err == nil {
			frame, err = decodeRLE(h, frag)
		}
		order = binary.BigEndian // planes are reassembled most significant byte first
	default:
		return nil, errs.Newf(errs.KindUnsupported, "dicom: %s frames need an image codec", h.Encoding)
	}
	if err != nil {
		return nil, err
	}
	return h.render(h.nativeSampler(frame, order))
}

// frameBytes is the size of one native frame.
func (h *Header) frameBytes() int64 {
	n := int64(h.Width) * int64(h.Height) * int64(h.Samples)
	if h.Photometric == "YBR_FULL_422" {
		n = (int64(h.Width)*int64(h.Height) + 1) / 2 * 4 // Y Y Cb Cr per pair of pixels
	}
	return (n*int64(h.BitsAllocated) + 7) / 8
}

// nativeFrame is the first frame of native pixel data.
func (p *parsed) nativeFrame() ([]byte, error) {
	n := p.h.frameBytes()
	if p.pixLen < n || int64(len(p.data)-p.pix) < n {
		return nil, corrupt("truncated pixel data")
	}
	return p.data[p.pix : p.pix+int(n)], nil
}

// FirstFrame returns the codestream of the first frame of encapsulated
// pixel data: the fragments of the first frame joined. h is the file's
// header (ParseHeader). It fails for native pixel data.
func FirstFrame(b []byte, h *Header) ([]byte, error) {
	if !h.Encoding.Encapsulated() {
		return nil, errs.Newf(errs.KindInternal, "dicom: %s pixel data is not encapsulated", h.Encoding)
	}
	p, err := parse(b)
	if err != nil {
		return nil, err
	}
	return p.firstFrame()
}

// fragments walks encapsulated pixel data: the Basic Offset Table (the
// first item) and then every fragment, up to the sequence delimiter. Each
// fragment is reported with its item's offset from the first fragment
// item.
func (p *parsed) fragments(fn func(off int, frag []byte) bool) ([]byte, error) {
	count := 0
	r := &reader{b: p.data, pos: p.pix, order: binary.LittleEndian, count: &count}
	var bot []byte
	first := -1
	for i := 0; ; i++ {
		e, err := r.next()
		if err != nil {
			return nil, err
		}
		if e.tag == tagSeqDelim {
			return bot, nil
		}
		if e.tag != tagItem || e.length == undefinedLength {
			return nil, corrupt("bad encapsulated pixel data")
		}
		v := r.b[e.val : e.val+int(e.length)]
		r.pos = e.val + int(e.length)
		if i == 0 {
			bot = v
			continue
		}
		if first < 0 {
			first = e.val - 8
		}
		if !fn(e.val-8-first, v) {
			return bot, nil
		}
	}
}

// firstFrame joins the fragments of frame 0: every fragment of a
// single-frame file; for multi-frame files the fragments before the second
// Basic Offset Table entry, else the first fragment when there is one
// fragment per frame, else the fragments up to the one that ends a
// codestream (EOI/EOC, FF D9).
func (p *parsed) firstFrame() ([]byte, error) {
	type frag struct {
		off  int
		data []byte
	}
	var frags []frag
	total := 0
	bot, err := p.fragments(func(off int, data []byte) bool {
		frags = append(frags, frag{off, data})
		total += len(data)
		return true
	})
	if err != nil {
		return nil, err
	}
	if len(frags) == 0 {
		return nil, corrupt("no fragments")
	}
	n := len(frags) // fragments of frame 0
	switch {
	case p.h.Frames == 1:
	case len(bot) >= 8 && len(bot)%4 == 0:
		next := int(binary.LittleEndian.Uint32(bot[4:]))
		n = 0
		for n < len(frags) && frags[n].off < next {
			n++
		}
	case len(frags) == p.h.Frames:
		n = 1
	default:
		n = 0
		for n < len(frags) {
			n++
			if endsCodestream(frags[n-1].data) {
				break
			}
		}
	}
	if n == 0 {
		return nil, corrupt("empty first frame")
	}
	if n == 1 {
		return frags[0].data, nil
	}
	out := make([]byte, 0, min(total, len(p.data)))
	for _, f := range frags[:n] {
		out = append(out, f.data...)
	}
	return out, nil
}

// endsCodestream reports a fragment ending with FF D9 (JPEG EOI, JPEG 2000
// EOC), ignoring trailing padding.
func endsCodestream(b []byte) bool {
	b = bytes.TrimRight(b, "\x00")
	return len(b) >= 2 && b[len(b)-2] == 0xFF && b[len(b)-1] == 0xD9
}

// decodeRLE decodes one RLE Lossless frame (PS3.5 annex G) into native
// layout: samples interleaved, each most significant byte first.
func decodeRLE(h *Header, frag []byte) ([]byte, error) {
	if len(frag) < 64 {
		return nil, corrupt("truncated RLE header")
	}
	if h.BitsAllocated%8 != 0 || h.Photometric == "YBR_FULL_422" {
		return nil, corrupt("RLE with a packed layout")
	}
	bps := h.BitsAllocated / 8
	segs := int(binary.LittleEndian.Uint32(frag))
	if segs != h.Samples*bps || segs > 15 {
		return nil, corruptf("RLE with %d segments", segs)
	}
	pixels := h.Width * h.Height
	out := make([]byte, pixels*segs)
	for s := range segs {
		start := int(binary.LittleEndian.Uint32(frag[4+4*s:]))
		end := len(frag)
		if s+1 < segs {
			end = int(binary.LittleEndian.Uint32(frag[8+4*s:]))
		}
		if start < 64 || start > end || end > len(frag) {
			return nil, corrupt("bad RLE segment offsets")
		}
		// Segment s is byte s%bps (most significant first) of sample s/bps.
		sample, byteIdx := s/bps, s%bps
		if err := unpackBits(frag[start:end], out, pixels, sample*bps+byteIdx, segs); err != nil {
			return nil, err
		}
	}
	// out is interleaved per pixel: sample 0 bytes, sample 1 bytes, ...;
	// the colour-by-plane flag does not apply to decoded RLE.
	h.Planar = false
	return out, nil
}

// unpackBits decodes a PackBits segment of n bytes into dst[first],
// dst[first+stride], ...
func unpackBits(src, dst []byte, n, first, stride int) error {
	i, o := 0, 0
	for o < n && i < len(src) {
		c := int(int8(src[i])) //nolint:gosec // G115: the PackBits header is a signed byte
		i++
		switch {
		case c >= 0:
			k := c + 1
			if i+k > len(src) {
				return corrupt("truncated RLE literal")
			}
			for j := range k {
				if o >= n {
					break
				}
				dst[first+o*stride] = src[i+j]
				o++
			}
			i += k
		case c != -128:
			if i >= len(src) {
				return corrupt("truncated RLE run")
			}
			v := src[i]
			i++
			for range 1 - c {
				if o >= n {
					break
				}
				dst[first+o*stride] = v
				o++
			}
		}
	}
	if o < n {
		return corrupt("short RLE segment")
	}
	return nil
}

// sampler reads stored sample values of a frame.
type sampler struct {
	w, h, bands int
	// at returns the stored value of band b of pixel i.
	at func(i, b int) int32
}

// nativeSampler reads a native frame (or a decoded RLE frame in order).
func (h *Header) nativeSampler(frame []byte, order binary.ByteOrder) sampler {
	pixels := h.Width * h.Height
	shift := uint(h.HighBit + 1 - h.BitsStored)
	mask := uint32(1)<<uint(h.BitsStored) - 1
	if h.BitsStored == 32 {
		mask = 0xFFFFFFFF
	}
	sign := uint32(1) << uint(h.BitsStored-1)
	raw := func(k int) uint32 {
		switch h.BitsAllocated {
		case 1:
			return uint32(frame[k/8]>>(uint(k)%8)) & 1
		case 8:
			return uint32(frame[k])
		case 16:
			return uint32(order.Uint16(frame[2*k:]))
		default:
			return order.Uint32(frame[4*k:])
		}
	}
	value := func(k int) int32 {
		v := (raw(k) >> shift) & mask
		if h.Signed && v&sign != 0 {
			v |= ^mask
		}
		return int32(v) //nolint:gosec // G115: two's complement reinterpretation of the sign-extended value
	}
	s := sampler{w: h.Width, h: h.Height, bands: h.Samples}
	switch {
	case h.Photometric == "YBR_FULL_422":
		s.at = func(i, b int) int32 {
			base := i / 2 * 4
			if b == 0 {
				return value(base + i%2)
			}
			return value(base + 1 + b)
		}
	case h.Samples == 1:
		s.at = func(i, _ int) int32 { return value(i) }
	case h.Planar:
		s.at = func(i, b int) int32 { return value(b*pixels + i) }
	default:
		s.at = func(i, b int) int32 { return value(i*h.Samples + b) }
	}
	return s
}
