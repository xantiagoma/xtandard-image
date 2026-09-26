package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"math"
	"os"
	"strings"
)

// writePDF writes a hand-made three-page PDF 1.4: page 1 (200×150 pt) is
// pure vector art (fills, a 0.5 pt line grid, a circle and Helvetica text)
// on an unpainted margin; page 2 (120×160 pt, a different size) embeds the
// procedural photo as a Flate-compressed RGB image; page 3 (200×150 pt) is
// vector again. The fonts are the standard 14 (not embedded), so nothing
// third-party is in the file.
func writePDF(out string) error {
	img := photo(160, 120)
	var raw bytes.Buffer
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := img.PixOffset(x, y)
			raw.Write(img.Pix[i : i+3])
		}
	}
	var z bytes.Buffer
	zw, err := zlib.NewWriterLevel(&z, zlib.BestCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(raw.Bytes()); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}

	page1 := strings.Join([]string{
		"0.75 0.85 0.95 rg 10 10 180 130 re f",
		"0 0 0 RG 0.5 w",
		grid(10, 10, 190, 140, 10),
		"0.85 0.1 0.1 rg " + circle(100, 75, 40) + " f",
		"0 0 0 rg BT /F1 18 Tf 20 115 Td (PDF page 1) Tj ET",
	}, "\n")
	page2 := strings.Join([]string{
		"q 100 0 0 75 10 45 cm /Im1 Do Q",
		"0.1 0.6 0.2 RG 2 w 5 5 110 150 re S",
		"0 0 0 rg BT /F1 14 Tf 12 130 Td (page 2) Tj ET",
	}, "\n")
	page3 := strings.Join([]string{
		"0.95 0.6 0.1 rg 0 0 200 150 re f",
		"0.2 0.2 0.6 RG 1 w",
		diagonals(200, 150, 12),
		"1 1 1 rg BT /F1 22 Tf 30 65 Td (page 3) Tj ET",
	}, "\n")

	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R 5 0 R] /Count 3 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 150] /Resources << /Font << /F1 6 0 R >> >> /Contents 8 0 R >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 120 160] /Resources << /Font << /F1 6 0 R >> /XObject << /Im1 7 0 R >> >> /Contents 9 0 R >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 150] /Resources << /Font << /F1 6 0 R >> >> /Contents 10 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream",
			b.Dx(), b.Dy(), z.Len(), z.String()),
		stream(page1),
		stream(page2),
		stream(page3),
	}
	var f bytes.Buffer
	f.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = f.Len()
		fmt.Fprintf(&f, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := f.Len()
	fmt.Fprintf(&f, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&f, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&f, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return os.WriteFile(out, f.Bytes(), 0o644)
}

func stream(content string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)
}

// grid strokes vertical and horizontal lines every step points.
func grid(x0, y0, x1, y1, step float64) string {
	var sb strings.Builder
	for x := x0; x <= x1; x += step {
		fmt.Fprintf(&sb, "%g %g m %g %g l ", x, y0, x, y1)
	}
	for y := y0; y <= y1; y += step {
		fmt.Fprintf(&sb, "%g %g m %g %g l ", x0, y, x1, y)
	}
	sb.WriteString("S")
	return sb.String()
}

// diagonals strokes parallel diagonal lines every step points.
func diagonals(w, h, step float64) string {
	var sb strings.Builder
	for x := -h; x <= w; x += step {
		fmt.Fprintf(&sb, "%g 0 m %g %g l ", x, x+h, h)
	}
	sb.WriteString("S")
	return sb.String()
}

// circle is a closed path of four Bézier arcs.
func circle(cx, cy, r float64) string {
	k := r * 4 * (math.Sqrt2 - 1) / 3
	return fmt.Sprintf("%g %g m %g %g %g %g %g %g c %g %g %g %g %g %g c %g %g %g %g %g %g c %g %g %g %g %g %g c h",
		cx+r, cy,
		cx+r, cy+k, cx+k, cy+r, cx, cy+r,
		cx-k, cy+r, cx-r, cy+k, cx-r, cy,
		cx-r, cy-k, cx-k, cy-r, cx, cy-r,
		cx+k, cy-r, cx+r, cy-k, cx+r, cy)
}

// writePSD writes a hand-made two-layer RGB Photoshop file, 8 bits, raw
// (uncompressed) channels: a full-canvas background layer (the procedural
// photo) and a smaller opaque "sticker" layer (the flat-colour graphic)
// placed at an offset. With composite it stores the merged image the way
// Photoshop does with "Maximize compatibility" on; without, it stores a
// version-info resource (0x0421) with hasRealMergedData = 0 and a blank
// white merged image, as Photoshop does with the option off, so readers must
// composite the layers themselves. psb selects the large-document format
// (version 2, 64-bit section lengths).
func writePSD(out string, psb, composite bool) error {
	const w, h = 96, 72
	bg := photo(w, h)
	sticker := graphic(40, 30)
	const sx, sy = 12, 10
	merged := image.NewRGBA(bg.Bounds())
	draw.Draw(merged, merged.Bounds(), bg, image.Point{}, draw.Src)
	draw.Draw(merged, image.Rect(sx, sy, sx+40, sy+30), sticker, image.Point{}, draw.Src)

	be := binary.BigEndian
	var f bytes.Buffer
	put := func(v any) { _ = binary.Write(&f, be, v) }
	// putLen writes a section length, 8 bytes in PSB where the spec says so.
	putLen := func(n int, long bool) {
		if long {
			put(uint64(n))
		} else {
			put(uint32(n))
		}
	}
	version := uint16(1)
	if psb {
		version = 2
	}
	f.WriteString("8BPS")
	put(version)
	f.Write(make([]byte, 6))
	put(uint16(3)) // channels of the merged image
	put(uint32(h))
	put(uint32(w))
	put(uint16(8))
	put(uint16(3)) // RGB
	put(uint32(0)) // colour mode data

	var res bytes.Buffer
	if !composite {
		res.WriteString("8BIM")
		res.Write([]byte{0x04, 0x21, 0, 0})
		_ = binary.Write(&res, be, uint32(17))
		// version 1, hasRealMergedData 0, empty writer and reader names,
		// file version 1, padded to even.
		res.Write([]byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0})
	}
	put(uint32(res.Len()))
	f.Write(res.Bytes())

	type layer struct {
		name       string
		rect       image.Rectangle
		img        image.Image
		off        image.Point
		alphaPlane bool
	}
	layers := []layer{
		{name: "Background", rect: image.Rect(0, 0, w, h), img: bg},
		{name: "Sticker", rect: image.Rect(sx, sy, sx+40, sy+30), img: sticker, alphaPlane: true},
	}
	planes := func(l layer) [][]byte {
		n := l.rect.Dx() * l.rect.Dy()
		p := [][]byte{make([]byte, 0, n), make([]byte, 0, n), make([]byte, 0, n)}
		var a []byte
		for y := range l.rect.Dy() {
			for x := range l.rect.Dx() {
				r, g, b, al := l.img.At(l.off.X+x, l.off.Y+y).RGBA()
				p[0] = append(p[0], byte(r>>8))
				p[1] = append(p[1], byte(g>>8))
				p[2] = append(p[2], byte(b>>8))
				a = append(a, byte(al>>8))
			}
		}
		if l.alphaPlane {
			p = append([][]byte{a}, p...) // channel -1 first
		}
		return p
	}

	var li bytes.Buffer
	lput := func(v any) { _ = binary.Write(&li, be, v) }
	lput(int16(len(layers)))
	var data bytes.Buffer
	for _, l := range layers {
		lput(int32(l.rect.Min.Y))
		lput(int32(l.rect.Min.X))
		lput(int32(l.rect.Max.Y))
		lput(int32(l.rect.Max.X))
		ps := planes(l)
		lput(uint16(len(ps)))
		ids := []int16{0, 1, 2}
		if l.alphaPlane {
			ids = []int16{-1, 0, 1, 2}
		}
		for i, p := range ps {
			lput(ids[i])
			if psb {
				lput(uint64(2 + len(p)))
			} else {
				lput(uint32(2 + len(p)))
			}
			_ = binary.Write(&data, be, uint16(0)) // raw
			data.Write(p)
		}
		li.WriteString("8BIMnorm")
		li.Write([]byte{255, 0, 0, 0}) // opacity, clipping, flags (visible), filler
		name := []byte{byte(len(l.name))}
		name = append(name, l.name...)
		for len(name)%4 != 0 {
			name = append(name, 0)
		}
		lput(uint32(8 + len(name))) // extra data
		lput(uint32(0))             // layer mask data
		lput(uint32(0))             // blending ranges
		li.Write(name)
	}
	li.Write(data.Bytes())
	if li.Len()%2 != 0 {
		li.WriteByte(0)
	}
	var lm bytes.Buffer
	if psb {
		_ = binary.Write(&lm, be, uint64(li.Len()))
	} else {
		_ = binary.Write(&lm, be, uint32(li.Len()))
	}
	lm.Write(li.Bytes())
	_ = binary.Write(&lm, be, uint32(0)) // global layer mask info
	putLen(lm.Len(), psb)
	f.Write(lm.Bytes())

	put(uint16(0)) // raw merged image, planar
	if composite {
		for _, p := range planes(layer{rect: merged.Bounds(), img: merged}) {
			f.Write(p)
		}
	} else {
		f.Write(bytes.Repeat([]byte{0xff}, 3*w*h))
	}
	return os.WriteFile(out, f.Bytes(), 0o644)
}
