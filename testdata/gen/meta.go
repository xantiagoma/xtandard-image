package main

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"os"
)

// bordered is the photo on a white canvas with the given margins (trim
// fixtures: every margin differs, so equal_hor/equal_ver are observable).
func bordered(w, h, top, right, bottom, left int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, left+w+right, top+h+bottom))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(left, top, left+w, top+h), photo(w, h), image.Point{}, draw.Src)
	return img
}

// ---- metadata fixture -------------------------------------------------------

type ifdEntry struct {
	tag, typ uint16
	count    uint32
	data     []byte // value bytes; offsets of out-of-line data are patched in
	ifd      int    // pointer tags: index of the sub-IFD; -1 otherwise
	blob     int    // tiffFile: > 0 is the index of the blob whose offset is the value
}

func asciiEntry(tag uint16, s string) ifdEntry {
	return ifdEntry{tag: tag, typ: 2, count: uint32(len(s) + 1), data: append([]byte(s), 0), ifd: -1}
}

func shortEntry(tag, v uint16) ifdEntry {
	return ifdEntry{tag: tag, typ: 3, count: 1, data: binary.LittleEndian.AppendUint16(nil, v), ifd: -1}
}

func rationals(tag uint16, v ...uint32) ifdEntry {
	b := make([]byte, 0, len(v)*8)
	for _, x := range v {
		b = binary.LittleEndian.AppendUint32(b, x)
		b = binary.LittleEndian.AppendUint32(b, 1)
	}
	return ifdEntry{tag: tag, typ: 5, count: uint32(len(v)), data: b, ifd: -1}
}

// exifPayload is an APP1 Exif payload: IFD0 (Make, Model, Orientation 6,
// Artist, Copyright, pointers), an Exif IFD (DateTimeOriginal) and a GPS IFD.
func exifPayload() []byte {
	ifds := [][]ifdEntry{
		{
			asciiEntry(0x010F, "TestMake"),
			asciiEntry(0x0110, "TestModel"),
			shortEntry(0x0112, 6),
			asciiEntry(0x013B, "Jane Artist"),
			asciiEntry(0x8298, "(c) Jane"),
			{tag: 0x8769, typ: 4, count: 1, ifd: 1},
			{tag: 0x8825, typ: 4, count: 1, ifd: 2},
		},
		{asciiEntry(0x9003, "2024:01:02 03:04:05")},
		{
			{tag: 0x0000, typ: 1, count: 4, data: []byte{2, 2, 0, 0}, ifd: -1},
			asciiEntry(0x0001, "N"),
			rationals(0x0002, 52, 31, 0),
			asciiEntry(0x0003, "E"),
			rationals(0x0004, 13, 24, 0),
		},
	}
	offsets := make([]uint32, len(ifds))
	off := uint32(8)
	for i, ifd := range ifds {
		offsets[i] = off
		off += 2 + uint32(len(ifd))*12 + 4
		for _, e := range ifd {
			if len(e.data) > 4 {
				off += uint32(len(e.data) + len(e.data)%2)
			}
		}
	}
	b := []byte("II*\x00\x08\x00\x00\x00")
	for i, ifd := range ifds {
		data := offsets[i] + 2 + uint32(len(ifd))*12 + 4
		var extra []byte
		b = binary.LittleEndian.AppendUint16(b, uint16(len(ifd)))
		for _, e := range ifd {
			b = binary.LittleEndian.AppendUint16(b, e.tag)
			b = binary.LittleEndian.AppendUint16(b, e.typ)
			b = binary.LittleEndian.AppendUint32(b, e.count)
			switch {
			case e.ifd >= 0:
				b = binary.LittleEndian.AppendUint32(b, offsets[e.ifd])
			case len(e.data) > 4:
				b = binary.LittleEndian.AppendUint32(b, data+uint32(len(extra)))
				extra = append(extra, e.data...)
				if len(e.data)%2 == 1 {
					extra = append(extra, 0)
				}
			default:
				v := make([]byte, 4)
				copy(v, e.data)
				b = append(b, v...)
			}
		}
		b = binary.LittleEndian.AppendUint32(b, 0)
		b = append(b, extra...)
	}
	return append([]byte("Exif\x00\x00"), b...)
}

const fixtureXMP = `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
	`<rdf:Description rdf:about="" xmlns:exif="http://ns.adobe.com/exif/1.0/" xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
	`exif:GPSLatitude="52,31.0N" exif:GPSLongitude="13,24.0E">` +
	`<dc:rights><rdf:Alt><rdf:li xml:lang="x-default">(c) Jane</rdf:li></rdf:Alt></dc:rights>` +
	`</rdf:Description></rdf:RDF></x:xmpmeta>`

// iptcPayload is an APP13 Photoshop payload with an IPTC copyright notice.
func iptcPayload() []byte {
	iptc := append([]byte{0x1C, 0x02, 0x74, 0x00, 0x08}, "(c) Jane"...)
	b := []byte("Photoshop 3.0\x008BIM\x04\x04\x00\x00")
	b = binary.BigEndian.AppendUint32(b, uint32(len(iptc)))
	b = append(b, iptc...)
	if len(iptc)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

// injectMetadata inserts Exif (orientation 6, copyright, GPS), XMP (with
// GPS) and IPTC segments after SOI, keeping the JPEG's own segments (its
// ICC profile).
func injectMetadata(in, out string) error {
	src, err := readJPEG(in)
	if err != nil {
		return err
	}
	seg := func(marker byte, payload []byte) []byte {
		s := []byte{0xFF, marker, 0, 0}
		binary.BigEndian.PutUint16(s[2:], uint16(len(payload)+2))
		return append(s, payload...)
	}
	dst := []byte{0xFF, 0xD8}
	dst = append(dst, seg(0xE1, exifPayload())...)
	dst = append(dst, seg(0xE1, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), fixtureXMP...))...)
	dst = append(dst, seg(0xED, iptcPayload())...)
	dst = append(dst, src[2:]...)
	return write(out, dst)
}

func readJPEG(path string) ([]byte, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(src) < 4 || src[0] != 0xFF || src[1] != 0xD8 {
		return nil, errors.New("not a JPEG")
	}
	return src, nil
}
