package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
)

// ---- WMF and EMF (WP-108) ---------------------------------------------------
//
// Own writers for two small Windows metafiles drawing the same scene: a
// blue background rectangle, an orange ellipse with a dark outline and a
// green rectangle. The WMF is placeable (bounding box and units per inch
// in its header), 200×150 logical units at 96 per inch; the EMF uses the
// default MM_TEXT mapping, 200×150 device pixels on a 96 DPI reference
// device.

// colorref is a Windows COLORREF (0x00BBGGRR).
func colorref(r, g, b byte) uint32 { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }

// shape is one filled, outlined figure: 'r' rectangle or 'e' ellipse.
type shape struct {
	kind                     byte
	left, top, right, bottom int16
	fill                     uint32
}

var metafileScene = []shape{
	{'r', 0, 0, 200, 150, colorref(40, 90, 200)},
	{'e', 30, 20, 150, 130, colorref(245, 150, 30)},
	{'r', 120, 70, 185, 135, colorref(40, 170, 80)},
}

// metafileFixtures writes wmf/shapes.wmf and emf/shapes.emf under dir.
func metafileFixtures(dir string) error {
	if err := writeWMF(filepath.Join(dir, "wmf", "shapes.wmf")); err != nil {
		return err
	}
	return writeEMF(filepath.Join(dir, "emf", "shapes.emf"))
}

// writeWMF writes the scene as a placeable WMF.
func writeWMF(out string) error {
	le := binary.LittleEndian
	var recs []byte
	rec := func(fn uint16, params ...int16) {
		recs = le.AppendUint32(recs, uint32(3+len(params))) //nolint:gosec // small records
		recs = le.AppendUint16(recs, fn)
		for _, p := range params {
			recs = le.AppendUint16(recs, uint16(p)) //nolint:gosec // WMF stores int16
		}
	}
	lo := func(v uint32) int16 { return int16(v & 0xFFFF) } //nolint:gosec // split into words
	hi := func(v uint32) int16 { return int16(v >> 16) }    //nolint:gosec // split into words
	const (
		metaSetWindowOrg               = 0x020B
		metaSetWindowExt               = 0x020C
		metaCreatePenIndirect          = 0x02FA
		metaCreateBrushIndirect        = 0x02FC
		metaSelectObject               = 0x012D
		metaDeleteObject               = 0x01F0
		metaRectangle                  = 0x041B
		metaEllipse                    = 0x0418
		metaEOF                        = 0x0000
		psSolid, bsSolid, hsNone       = 0, 0, 0
		penWidth                       = 3
		unitsPerInch                   = 96
		wmfWidth, wmfHeight      int16 = 200, 150
	)
	rec(metaSetWindowOrg, 0, 0)
	rec(metaSetWindowExt, wmfHeight, wmfWidth)
	dark := colorref(30, 30, 40)
	rec(metaCreatePenIndirect, psSolid, penWidth, 0, lo(dark), hi(dark)) // object 0
	rec(metaSelectObject, 0)
	for _, s := range metafileScene {
		rec(metaCreateBrushIndirect, bsSolid, lo(s.fill), hi(s.fill), hsNone) // object 1
		rec(metaSelectObject, 1)
		fn := uint16(metaRectangle)
		if s.kind == 'e' {
			fn = metaEllipse
		}
		rec(fn, s.bottom, s.right, s.top, s.left)
		rec(metaDeleteObject, 1)
	}
	rec(metaEOF)
	var head []byte
	head = le.AppendUint16(head, 1)                     // memory metafile
	head = le.AppendUint16(head, 9)                     // header size in words
	head = le.AppendUint16(head, 0x0300)                // version
	head = le.AppendUint32(head, uint32(9+len(recs)/2)) //nolint:gosec // total size in words
	head = le.AppendUint16(head, 2)                     // objects
	head = le.AppendUint32(head, 8)                     // largest record (the pen), words
	head = le.AppendUint16(head, 0)
	var p []byte
	p = le.AppendUint32(p, 0x9AC6CDD7)
	p = le.AppendUint16(p, 0) // handle
	for _, v := range []int16{0, 0, wmfWidth, wmfHeight} {
		p = le.AppendUint16(p, uint16(v)) //nolint:gosec // WMF stores int16
	}
	p = le.AppendUint16(p, unitsPerInch)
	p = le.AppendUint32(p, 0)
	var sum uint16
	for i := 0; i < 20; i += 2 {
		sum ^= le.Uint16(p[i:])
	}
	p = le.AppendUint16(p, sum)
	return writeFile(out, append(append(p, head...), recs...))
}

// writeEMF writes the scene as an EMF.
func writeEMF(out string) error {
	le := binary.LittleEndian
	var recs []byte
	n := 1 // the header
	rec := func(typ uint32, params ...uint32) {
		recs = le.AppendUint32(recs, typ)
		recs = le.AppendUint32(recs, uint32(8+4*len(params))) //nolint:gosec // small records
		recs = append(recs, u32s(params)...)
		n++
	}
	const (
		emrCreatePen           = 38
		emrCreateBrushIndirect = 39
		emrSelectObject        = 37
		emrDeleteObject        = 40
		emrRectangle           = 43
		emrEllipse             = 42
		emrEOF                 = 14
		w, h                   = 200, 150
	)
	rec(emrCreatePen, 1, 0, 3, 0, colorref(30, 30, 40)) // ihPen 1: PS_SOLID, width 3
	rec(emrSelectObject, 1)
	for _, s := range metafileScene {
		rec(emrCreateBrushIndirect, 2, 0, s.fill, 0) // ihBrush 2: BS_SOLID
		rec(emrSelectObject, 2)
		typ := uint32(emrRectangle)
		if s.kind == 'e' {
			typ = emrEllipse
		}
		rec(typ, uint32(s.left), uint32(s.top), uint32(s.right), uint32(s.bottom)) //nolint:gosec // non-negative
		rec(emrDeleteObject, 2)
	}
	rec(emrEOF, 0, 16, 20)
	const headerSize = 88
	// Device: 1920×1080 px on 508×286 mm (96 DPI); the frame in 0.01 mm.
	frame := func(px int) uint32 { return uint32(px * 2646 / 100) } //nolint:gosec // small values
	var hd []byte
	hd = le.AppendUint32(hd, 1)
	hd = le.AppendUint32(hd, headerSize)
	hd = append(hd, u32s([]uint32{0, 0, w - 1, h - 1})...)               // bounds (inclusive)
	hd = append(hd, u32s([]uint32{0, 0, frame(w) - 1, frame(h) - 1})...) // frame
	hd = le.AppendUint32(hd, 0x464D4520)                                 // " EMF"
	hd = le.AppendUint32(hd, 0x10000)                                    // version
	hd = le.AppendUint32(hd, uint32(headerSize+len(recs)))               //nolint:gosec // file size
	hd = le.AppendUint32(hd, uint32(n))                                  //nolint:gosec // records
	hd = le.AppendUint16(hd, 3)                                          // handles (0 reserved)
	hd = le.AppendUint16(hd, 0)                                          // reserved
	hd = append(hd, u32s([]uint32{0, 0, 0})...)                          // description, palette
	hd = append(hd, u32s([]uint32{1920, 1080, 508, 286})...)             // device px, mm
	return writeFile(out, append(hd, recs...))
}

func u32s(v []uint32) []byte {
	b := make([]byte, 0, 4*len(v))
	for _, x := range v {
		b = binary.LittleEndian.AppendUint32(b, x)
	}
	return b
}

func writeFile(out string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil { //nolint:gosec // generator output
		return err
	}
	return os.WriteFile(out, b, 0o644) //nolint:gosec // generator output
}
