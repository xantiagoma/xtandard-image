package container

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// MNG (Multiple-image Network Graphics) and JNG (JPEG Network Graphics)
// are PNG-style chunk streams. Neither is decoded here: the first image an
// MNG carries becomes a standalone PNG (or a JNG, below), and a JNG
// becomes its JPEG stream, which libvips then decodes like any PNG or
// JPEG. A JNG with an alpha channel has no single embedded image, so its
// colour JPEG and alpha (PNG-compressed samples or a second JPEG) are
// decoded with the standard library, bounded by Limits.MaxPixels, and
// re-encoded as one PNG.
//
// MNG composition is not interpreted: the first embedded PNG or JNG is used
// as is, whatever DEFI offsets, BACK backgrounds, FRAM clipping or later
// frames say. Delta-PNG (DHDR) and BASI objects are not read.

// Signatures.
const (
	magicMNG = "\x8aMNG\r\n\x1a\n"
	magicJNG = "\x8bJNG\r\n\x1a\n"
	jpegSOI  = "\xff\xd8"
)

// Header chunk lengths.
const (
	mhdrLen = 28
	jhdrLen = 16
	ihdrLen = 13
)

// maxChunks bounds the chunk walk (an MNG animation of many frames has a
// few chunks per frame; the walk stops at the first image anyway).
const maxChunks = 1 << 16

// chunk is one chunk of a PNG-style stream: raw spans length, type, data
// and CRC.
type chunk struct {
	typ  string
	data []byte
	raw  []byte
}

// chunkReader walks the chunks after a signature, checking lengths and
// CRCs (every real file checked, including the gallery samples, has
// correct CRCs, so there is no leniency).
type chunkReader struct {
	b   []byte
	off int
	n   int
	k   Kind
}

func newChunkReader(b []byte, k Kind) *chunkReader { return &chunkReader{b: b, off: 8, k: k} }

// next returns the next chunk; ok is false at the end of the data.
func (r *chunkReader) next() (chunk, bool, error) {
	if r.off == len(r.b) {
		return chunk{}, false, nil
	}
	if r.n++; r.n > maxChunks {
		return chunk{}, false, errs.Newf(errs.KindCorrupt, "container: %s has more than %d chunks", r.k, maxChunks)
	}
	if len(r.b)-r.off < 12 {
		return chunk{}, false, errs.Newf(errs.KindCorrupt, "container: %s chunk truncated", r.k)
	}
	n := binary.BigEndian.Uint32(r.b[r.off:])
	if n > 1<<31-1 || int64(n) > int64(len(r.b)-r.off-12) {
		return chunk{}, false, errs.Newf(errs.KindCorrupt, "container: %s chunk length %d beyond the data", r.k, n)
	}
	end := r.off + 12 + int(n)
	c := chunk{typ: string(r.b[r.off+4 : r.off+8]), data: r.b[r.off+8 : r.off+8+int(n)], raw: r.b[r.off:end]}
	if !validChunkType(c.typ) {
		return chunk{}, false, errs.Newf(errs.KindCorrupt, "container: %s chunk type is not ASCII letters", r.k)
	}
	if crc32.ChecksumIEEE(r.b[r.off+4:end-4]) != binary.BigEndian.Uint32(r.b[end-4:]) {
		return chunk{}, false, errs.Newf(errs.KindCorrupt, "container: %s %s chunk CRC mismatch", r.k, c.typ)
	}
	r.off = end
	return c, true, nil
}

func validChunkType(t string) bool {
	for i := range len(t) {
		c := t[i] | 0x20
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// firstChunk reports whether b starts with sig followed by a chunk of type
// typ and length n.
func firstChunk(b []byte, sig, typ string, n uint32) bool {
	return len(b) >= 16 && string(b[:8]) == sig && binary.BigEndian.Uint32(b[8:]) == n && string(b[12:16]) == typ
}

func isMNG(b []byte) bool { return firstChunk(b, magicMNG, "MHDR", mhdrLen) }
func isJNG(b []byte) bool { return firstChunk(b, magicJNG, "JHDR", jhdrLen) }

// appendChunk writes a chunk of type typ with data, CRC computed.
func appendChunk(out []byte, typ string, data []byte) []byte {
	out = binary.BigEndian.AppendUint32(out, uint32(len(data))) //nolint:gosec // bounded by the source's 31-bit lengths
	start := len(out)
	out = append(out, typ...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out[start:]))
}

// mngUnsupported is the public refusal of an MNG without a usable image.
func mngUnsupported(why string) error {
	return errs.WithPublic(errs.New(errs.KindUnsupported, "container: mng "+why),
		"MNG without a PNG or JNG image is not supported")
}

// openMNG extracts the first image of an MNG stream.
func openMNG(data []byte, lim Limits) (*Embedded, error) {
	r := newChunkReader(data, KindMNG)
	globals := map[string][]byte{} // global PLTE, tRNS, gAMA, cHRM, sRGB, iCCP: chunk data
	first := true
	for {
		c, ok, err := r.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, mngUnsupported("without an image")
		}
		if first {
			first = false
			if c.typ != "MHDR" {
				return nil, errs.New(errs.KindCorrupt, "container: mng does not start with MHDR")
			}
			continue
		}
		switch c.typ {
		case "MEND":
			return nil, mngUnsupported("without an image")
		case "DHDR", "BASI":
			return nil, mngUnsupported("delta-png or basi object before any image")
		case "PLTE", "tRNS", "gAMA", "cHRM", "sRGB", "iCCP":
			globals[c.typ] = c.data
		case "IHDR":
			png, err := mngPNG(r, c, globals, lim)
			if err != nil {
				return nil, err
			}
			return embeddedBytes(Source{Kind: KindMNG, Entry: "first frame", Content: ContentFrame}, png), nil
		case "JHDR":
			img, _, err := jngImage(r, c, lim)
			if err != nil {
				return nil, err
			}
			return embeddedBytes(Source{Kind: KindMNG, Entry: "first frame", Content: ContentFrame}, img), nil
		default:
			// TERM, BACK, FRAM, DEFI, LOOP, ENDL, SAVE, SEEK, MOVE, CLIP,
			// SHOW, text and other control chunks: composition is ignored.
		}
	}
}

// mngPNG builds a standalone PNG from the IHDR…IEND stream starting at
// ihdr, with the MNG's global chunks where the stream relies on them.
func mngPNG(r *chunkReader, ihdr chunk, globals map[string][]byte, lim Limits) ([]byte, error) {
	if len(ihdr.data) != ihdrLen {
		return nil, errs.New(errs.KindCorrupt, "container: mng: bad IHDR")
	}
	var body []chunk
	for {
		c, ok, err := r.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errs.New(errs.KindCorrupt, "container: mng: embedded png without IEND")
		}
		if c.typ == "IEND" {
			break
		}
		if c.typ == "IHDR" || c.typ == "MEND" {
			return nil, errs.New(errs.KindCorrupt, "container: mng: embedded png without IEND")
		}
		body = append(body, c)
	}
	has := map[string]bool{}
	for _, c := range body {
		if len(c.data) > 0 {
			has[c.typ] = true
		}
	}
	out := make([]byte, 0, len(magicPNG)+len(ihdr.raw)+12)
	out = append(out, magicPNG...)
	out = append(out, ihdr.raw...)
	// Colour-space chunks go before PLTE and IDAT; iCCP and sRGB exclude
	// each other.
	if !has["iCCP"] && !has["sRGB"] {
		for _, t := range []string{"iCCP", "sRGB"} {
			if g, ok := globals[t]; ok {
				out = appendChunk(out, t, g)
				break
			}
		}
	}
	for _, t := range []string{"cHRM", "gAMA"} {
		if g, ok := globals[t]; ok && !has[t] {
			out = appendChunk(out, t, g)
		}
	}
	paletted := ihdr.data[9] == 3
	globalPLTE := false // the global palette was emitted in place of the embedded one
	emittedPLTE, emittedTRNS := false, false
	for _, c := range body {
		switch {
		case c.typ == "PLTE":
			if len(c.data) == 0 {
				g, ok := globals["PLTE"]
				if !ok {
					return nil, errs.New(errs.KindCorrupt, "container: mng: empty PLTE without a global palette")
				}
				out = appendChunk(out, "PLTE", g)
				globalPLTE = true
			} else {
				out = append(out, c.raw...)
			}
			emittedPLTE = true
			continue
		case c.typ == "tRNS":
			if len(c.data) == 0 {
				if g, ok := globals["tRNS"]; ok {
					out = appendChunk(out, "tRNS", g)
					emittedTRNS = true
				}
				continue
			}
			emittedTRNS = true
		case c.typ == "IDAT" && paletted && !emittedPLTE:
			g, ok := globals["PLTE"]
			if !ok {
				return nil, errs.New(errs.KindCorrupt, "container: mng: paletted png without a palette")
			}
			out = appendChunk(out, "PLTE", g)
			emittedPLTE, globalPLTE = true, true
			if t, ok := globals["tRNS"]; ok && !has["tRNS"] {
				out = appendChunk(out, "tRNS", t)
				emittedTRNS = true
			}
		case c.typ == "IDAT" && globalPLTE && !emittedTRNS:
			if t, ok := globals["tRNS"]; ok {
				out = appendChunk(out, "tRNS", t)
			}
			emittedTRNS = true
		case (c.typ == "iCCP" || c.typ == "sRGB" || c.typ == "cHRM" || c.typ == "gAMA") && len(c.data) == 0:
			continue // an empty colour chunk cancels the global one
		}
		out = append(out, c.raw...)
		if int64(len(out)) > lim.MaxBytes {
			return nil, errs.Newf(errs.KindLimitExceeded, "container: mng frame exceeds %d bytes", lim.MaxBytes)
		}
	}
	out = appendChunk(out, "IEND", nil)
	if int64(len(out)) > lim.MaxBytes {
		return nil, errs.Newf(errs.KindLimitExceeded, "container: mng frame exceeds %d bytes", lim.MaxBytes)
	}
	return out, nil
}

// jhdr is a JNG header.
type jhdr struct {
	width, height                                int
	colour, depth, interlace                     uint8
	alphaDepth, alphaCompression, alphaInterlace uint8
}

func (h *jhdr) alpha() bool { return h.colour == 12 || h.colour == 14 }

func parseJHDR(d []byte, lim Limits) (jhdr, error) {
	if len(d) != jhdrLen {
		return jhdr{}, errs.New(errs.KindCorrupt, "container: jng: bad JHDR")
	}
	w, hgt := binary.BigEndian.Uint32(d), binary.BigEndian.Uint32(d[4:])
	h := jhdr{colour: d[8], depth: d[9], interlace: d[11], alphaDepth: d[12], alphaCompression: d[13], alphaInterlace: d[15]}
	bad := func(what string) (jhdr, error) {
		return jhdr{}, errs.Newf(errs.KindCorrupt, "container: jng: bad JHDR %s", what)
	}
	switch {
	case w == 0 || hgt == 0 || w > 1<<31-1 || hgt > 1<<31-1:
		return bad("size")
	case h.colour != 8 && h.colour != 10 && h.colour != 12 && h.colour != 14:
		return bad("colour type")
	case h.depth != 8 && h.depth != 12 && h.depth != 20:
		return bad("sample depth")
	case d[10] != 8 || (h.interlace != 0 && h.interlace != 8):
		return bad("compression or interlace")
	case d[14] != 0 || h.alphaInterlace > 1:
		return bad("alpha filter or interlace")
	}
	if h.alpha() {
		switch {
		case h.alphaCompression == 0 && (h.alphaDepth == 1 || h.alphaDepth == 2 || h.alphaDepth == 4 ||
			h.alphaDepth == 8 || h.alphaDepth == 16):
		case h.alphaCompression == 8 && h.alphaDepth == 8:
		default:
			return bad("alpha depth or compression")
		}
	} else if h.alphaDepth != 0 {
		return bad("alpha depth")
	}
	h.width, h.height = int(w), int(hgt)
	if int64(w)*int64(hgt) > lim.MaxPixels {
		return jhdr{}, errs.Newf(errs.KindLimitExceeded, "container: jng of %dx%d exceeds %d pixels", w, hgt, lim.MaxPixels)
	}
	if h.depth == 12 {
		return jhdr{}, errs.WithPublic(errs.New(errs.KindUnsupported, "container: 12-bit jng"),
			"12-bit JNG is not supported")
	}
	return h, nil
}

// openJNG extracts the image of a JNG stream.
func openJNG(data []byte, lim Limits) (*Embedded, error) {
	r := newChunkReader(data, KindJNG)
	c, _, err := r.next()
	if err != nil {
		return nil, err
	}
	img, entry, err := jngImage(r, c, lim)
	if err != nil {
		return nil, err
	}
	return embeddedBytes(Source{Kind: KindJNG, Entry: entry, Content: ContentComposite}, img), nil
}

// jngImage reads the JNG stream starting at its JHDR chunk up to IEND and
// returns its image: the JPEG stream, or a PNG when there is an alpha
// channel. entry names the chunks used.
func jngImage(r *chunkReader, head chunk, lim Limits) ([]byte, string, error) {
	h, err := parseJHDR(head.data, lim)
	if err != nil {
		return nil, "", err
	}
	var jdat, jdaa, idat []byte
	sep := false
	for {
		c, ok, err := r.next()
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, "", errs.New(errs.KindCorrupt, "container: jng without IEND")
		}
		if c.typ == "IEND" {
			break
		}
		switch c.typ {
		case "JDAT":
			if !sep {
				jdat = append(jdat, c.data...)
			}
		case "JSEP":
			sep = true
		case "JDAA":
			jdaa = append(jdaa, c.data...)
		case "IDAT":
			idat = append(idat, c.data...)
		case "JHDR", "IHDR", "MHDR", "MEND":
			return nil, "", errs.New(errs.KindCorrupt, "container: jng without IEND")
		}
		if int64(len(jdat))+int64(len(jdaa))+int64(len(idat)) > lim.MaxBytes {
			return nil, "", errs.Newf(errs.KindLimitExceeded, "container: jng exceeds %d bytes", lim.MaxBytes)
		}
	}
	if !bytes.HasPrefix(jdat, []byte(jpegSOI)) {
		return nil, "", errs.New(errs.KindCorrupt, "container: jng JDAT is not a JPEG stream")
	}
	if !h.alpha() {
		return jdat, "JDAT", nil
	}
	colour, err := decodeJPEG(jdat, &h, "JDAT")
	if err != nil {
		return nil, "", err
	}
	var alpha image.Image
	entry := "JDAT+IDAT"
	if h.alphaCompression == 8 {
		entry = "JDAT+JDAA"
		if alpha, err = decodeJPEG(jdaa, &h, "JDAA"); err != nil {
			return nil, "", err
		}
	} else if alpha, err = decodeAlphaPNG(idat, &h); err != nil {
		return nil, "", err
	}
	out := image.NewNRGBA(image.Rect(0, 0, h.width, h.height))
	for y := range h.height {
		for x := range h.width {
			cr, cg, cb, _ := colour.At(x, y).RGBA()
			a := color.Gray16Model.Convert(alpha.At(x, y)).(color.Gray16).Y //nolint:forcetypeassert // Gray16Model returns Gray16
			i := out.PixOffset(x, y)
			out.Pix[i+0], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = uint8(cr>>8), uint8(cg>>8), uint8(cb>>8), uint8(a>>8) //nolint:gosec // 16-bit values
		}
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&b, out); err != nil {
		return nil, "", errs.Wrapf(errs.KindInternal, err, "container: jng png encode")
	}
	if int64(b.Len()) > lim.MaxBytes {
		return nil, "", errs.Newf(errs.KindLimitExceeded, "container: jng image exceeds %d bytes", lim.MaxBytes)
	}
	return b.Bytes(), entry, nil
}

// decodeJPEG decodes a JPEG stream of a JNG, whose size must be the JHDR's
// (checked before anything is allocated).
func decodeJPEG(b []byte, h *jhdr, what string) (image.Image, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, errs.Wrapf(errs.KindCorrupt, err, "container: jng %s", what)
	}
	if cfg.Width != h.width || cfg.Height != h.height {
		return nil, errs.Newf(errs.KindCorrupt, "container: jng %s is %dx%d, JHDR says %dx%d", what,
			cfg.Width, cfg.Height, h.width, h.height)
	}
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, errs.Wrapf(errs.KindCorrupt, err, "container: jng %s", what)
	}
	return img, nil
}

// decodeAlphaPNG decodes the PNG-compressed alpha samples of a JNG by
// wrapping them in a greyscale PNG of the JHDR's size and alpha depth.
func decodeAlphaPNG(idat []byte, h *jhdr) (image.Image, error) {
	if len(idat) == 0 {
		return nil, errs.New(errs.KindCorrupt, "container: jng alpha without IDAT")
	}
	var hdr [ihdrLen]byte
	binary.BigEndian.PutUint32(hdr[0:], uint32(h.width))  //nolint:gosec // parseJHDR bounds it to 31 bits
	binary.BigEndian.PutUint32(hdr[4:], uint32(h.height)) //nolint:gosec // idem
	hdr[8], hdr[9], hdr[12] = h.alphaDepth, 0, h.alphaInterlace
	p := append([]byte(magicPNG), nil...)
	p = appendChunk(p, "IHDR", hdr[:])
	p = appendChunk(p, "IDAT", idat)
	p = appendChunk(p, "IEND", nil)
	img, err := png.Decode(bytes.NewReader(p))
	if err != nil {
		return nil, errs.Wrapf(errs.KindCorrupt, err, "container: jng alpha")
	}
	return img, nil
}

// embeddedBytes wraps an image built in memory.
func embeddedBytes(src Source, b []byte) *Embedded {
	return &Embedded{Source: src, size: len(b), read: func(dst []byte) error { copy(dst, b); return nil }}
}
