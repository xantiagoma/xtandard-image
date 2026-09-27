package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"
)

// writeICNS writes an Apple icon with two images of the procedural photo
// with alpha: a classic 16×16 entry (is32, run-length coded RGB planes, with
// its s8mk 8-bit mask) and a 128×128 PNG entry (ic07), plus a TOC.
func writeICNS(out string) error {
	small := withAlpha(photo(16, 16))
	var r, g, b, a []byte
	for i := 0; i < len(small.Pix); i += 4 {
		r, g, b, a = append(r, small.Pix[i]), append(g, small.Pix[i+1]), append(b, small.Pix[i+2]), append(a, small.Pix[i+3])
	}
	is32 := append(append(icnsPack(r), icnsPack(g)...), icnsPack(b)...)

	var big bytes.Buffer
	if err := png.Encode(&big, withAlpha(photo(128, 128))); err != nil {
		return err
	}
	entries := [][2][]byte{
		{[]byte("is32"), is32},
		{[]byte("s8mk"), a},
		{[]byte("ic07"), big.Bytes()},
	}
	var toc, body bytes.Buffer
	for _, e := range entries {
		var hdr [8]byte
		copy(hdr[:], e[0])
		binary.BigEndian.PutUint32(hdr[4:], uint32(8+len(e[1])))
		toc.Write(hdr[:])
		body.Write(hdr[:])
		body.Write(e[1])
	}
	var f bytes.Buffer
	f.WriteString("icns")
	_ = binary.Write(&f, binary.BigEndian, uint32(8+8+toc.Len()+body.Len()))
	f.WriteString("TOC ")
	_ = binary.Write(&f, binary.BigEndian, uint32(8+toc.Len()))
	f.Write(toc.Bytes())
	f.Write(body.Bytes())
	return os.WriteFile(out, f.Bytes(), 0o644)
}

// icnsPack is Apple's icon run-length coding: runs of three to 130 equal
// bytes become (0x80 + n - 3, value), everything else literal groups of up
// to 128 bytes (n - 1, bytes...).
func icnsPack(src []byte) []byte {
	var out []byte
	for i := 0; i < len(src); {
		run := 1
		for i+run < len(src) && src[i+run] == src[i] && run < 130 {
			run++
		}
		if run >= 3 {
			out = append(out, byte(0x80+run-3), src[i])
			i += run
			continue
		}
		start := i
		for i < len(src) && i-start < 128 {
			if i+2 < len(src) && src[i] == src[i+1] && src[i] == src[i+2] {
				break
			}
			i++
		}
		out = append(out, byte(i-start-1))
		out = append(out, src[start:i]...)
	}
	return out
}
