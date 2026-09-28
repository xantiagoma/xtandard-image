package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"slices"
)

// MNG and JNG fixtures (WP-108): chunk streams around PNG and JPEG data the
// standard library encodes from the generated photo and graphic.

const (
	sigMNG = "\x8aMNG\r\n\x1a\n"
	sigJNG = "\x8bJNG\r\n\x1a\n"
	sigPNG = "\x89PNG\r\n\x1a\n"
)

type pngChunk struct {
	typ  string
	data []byte
}

func putChunk(b *bytes.Buffer, typ string, data []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data))) //nolint:gosec // small fixtures
	b.Write(n[:])
	start := b.Len()
	b.WriteString(typ)
	b.Write(data)
	binary.BigEndian.PutUint32(n[:], crc32.ChecksumIEEE(b.Bytes()[start:]))
	b.Write(n[:])
}

// pngChunks encodes img as PNG and returns its chunks after the signature.
func pngChunks(img image.Image) ([]pngChunk, error) {
	p, err := pngBytes(img)
	if err != nil {
		return nil, err
	}
	var out []pngChunk
	for p = p[len(sigPNG):]; len(p) >= 12; {
		n := int(binary.BigEndian.Uint32(p))
		out = append(out, pngChunk{string(p[4:8]), p[8 : 8+n]})
		p = p[12+n:]
	}
	return out, nil
}

// mhdr is an MNG-VLC header: frame size, ticks per second 100, no layers,
// frame or play-time counts, simplicity profile 1 (MNG-VLC).
func mhdr(w, h int) []byte {
	d := make([]byte, 28)
	binary.BigEndian.PutUint32(d[0:], uint32(w)) //nolint:gosec // small fixtures
	binary.BigEndian.PutUint32(d[4:], uint32(h)) //nolint:gosec // small fixtures
	binary.BigEndian.PutUint32(d[8:], 100)
	binary.BigEndian.PutUint32(d[24:], 1)
	return d
}

// jhdrData is a JNG header for an 8-bit JPEG.
func jhdrData(w, h int, colour, alphaDepth, alphaCompression uint8) []byte {
	d := make([]byte, 16)
	binary.BigEndian.PutUint32(d[0:], uint32(w)) //nolint:gosec // small fixtures
	binary.BigEndian.PutUint32(d[4:], uint32(h)) //nolint:gosec // small fixtures
	d[8], d[9], d[10], d[12], d[13] = colour, 8, 8, alphaDepth, alphaCompression
	return d
}

// alphaMask is an 8-bit grey ramp towards the corners.
func alphaMask(w, h int) *image.Gray {
	a := withAlpha(photo(w, h))
	g := image.NewGray(a.Bounds())
	for i := range w * h {
		g.Pix[i] = a.Pix[i*4+3]
	}
	return g
}

func jpegBytes(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 85})
	return b.Bytes(), err
}

// writeJNGStream writes JHDR…IEND for a w×h photo; alpha is "", "idat" or
// "jdaa".
func writeJNGStream(b *bytes.Buffer, w, h int, alpha string) error {
	colour, err := jpegBytes(photo(w, h))
	if err != nil {
		return err
	}
	switch alpha {
	case "":
		putChunk(b, "JHDR", jhdrData(w, h, 10, 0, 0))
	case "idat":
		putChunk(b, "JHDR", jhdrData(w, h, 14, 8, 0))
		cs, err := pngChunks(alphaMask(w, h))
		if err != nil {
			return err
		}
		for _, c := range cs {
			if c.typ == "IDAT" {
				putChunk(b, "IDAT", c.data)
			}
		}
	case "jdaa":
		putChunk(b, "JHDR", jhdrData(w, h, 14, 8, 8))
		a, err := jpegBytes(alphaMask(w, h))
		if err != nil {
			return err
		}
		putChunk(b, "JDAA", a)
	}
	// Two JDAT chunks: readers concatenate them.
	half := len(colour) / 2
	putChunk(b, "JDAT", colour[:half])
	putChunk(b, "JDAT", colour[half:])
	putChunk(b, "IEND", nil)
	return nil
}

// writeMNG writes one MNG or JNG fixture: mng-png (one PNG frame),
// mng-palette (a global PLTE, the frame's PLTE empty), mng-frames (two
// frames with FRAM and DEFI), mng-jng (a JNG frame), jng, jng-idat (8-bit
// PNG-compressed alpha) and jng-jdaa (JPEG alpha).
func writeMNG(kind, out string) error {
	const w, h = 48, 32
	var b bytes.Buffer
	switch kind {
	case "jng", "jng-idat", "jng-jdaa":
		b.WriteString(sigJNG)
		alpha := map[string]string{"jng": "", "jng-idat": "idat", "jng-jdaa": "jdaa"}[kind]
		if err := writeJNGStream(&b, w, h, alpha); err != nil {
			return err
		}
		return os.WriteFile(out, b.Bytes(), 0o644) //nolint:gosec // fixtures are world-readable
	case "mng-png", "mng-palette", "mng-frames", "mng-jng":
	default:
		return errors.New("mng: kind must be mng-png, mng-palette, mng-frames, mng-jng, jng, jng-idat or jng-jdaa")
	}
	b.WriteString(sigMNG)
	putChunk(&b, "MHDR", mhdr(w, h))
	putChunk(&b, "TERM", []byte{3, 0, 0, 0, 0, 100, 0, 0, 0, 1})
	putChunk(&b, "BACK", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	switch kind {
	case "mng-jng":
		if err := writeJNGStream(&b, w, h, "idat"); err != nil {
			return err
		}
	case "mng-palette":
		pal := color.Palette{}
		src := graphic(w, h)
		for y := range h {
			for x := range w {
				if c := src.RGBAAt(x, y); !slices.Contains(pal, color.Color(c)) && len(pal) < 256 {
					pal = append(pal, c)
				}
			}
		}
		img := image.NewPaletted(src.Bounds(), pal)
		for y := range h {
			for x := range w {
				img.Set(x, y, src.At(x, y))
			}
		}
		cs, err := pngChunks(img)
		if err != nil {
			return err
		}
		for _, c := range cs {
			if c.typ == "PLTE" {
				putChunk(&b, "PLTE", c.data) // global palette
			}
		}
		for _, c := range cs {
			if c.typ == "PLTE" {
				putChunk(&b, "PLTE", nil) // "use the global palette"
				continue
			}
			putChunk(&b, c.typ, c.data)
		}
	default: // mng-png, mng-frames
		frames := []image.Image{photo(w, h)}
		if kind == "mng-frames" {
			frames = append(frames, graphic(w, h))
		}
		for i, f := range frames {
			if i > 0 {
				putChunk(&b, "FRAM", []byte{1, 0})
				putChunk(&b, "DEFI", []byte{0, 1, 0, 0})
			}
			cs, err := pngChunks(f)
			if err != nil {
				return err
			}
			for _, c := range cs {
				putChunk(&b, c.typ, c.data)
			}
		}
	}
	putChunk(&b, "MEND", nil)
	return os.WriteFile(out, b.Bytes(), 0o644) //nolint:gosec // fixtures are world-readable
}
