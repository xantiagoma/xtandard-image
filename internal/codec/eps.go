package codec

import (
	"bytes"
	"encoding/binary"
	"math"
	"strconv"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// EPS, PostScript and PostScript-based Illustrator files (FormatEPS) are
// rendered by Ghostscript in a sandboxed subprocess, only in the gpl image
// and only with XI_FORMATS_EPS_ENABLED (internal/ghostscript, ADR 0011).
// Everything here is read in Go: probing never runs Ghostscript.

// EPSDisabledMessage is the public 422 message for an EPS source that
// cannot be rendered: Ghostscript is off (or absent) and the file carries
// no embedded TIFF preview.
const EPSDisabledMessage = "EPS needs the gpl image with XI_FORMATS_EPS_ENABLED=true"

// Signatures: a DOS EPS binary header (Adobe TN 5002 §"DOS EPS Binary File
// Header") and the PostScript header comment.
const (
	magicDOSEPS = "\xc5\xd0\xd3\xc6"
	magicPS     = "%!PS"
	dosEPSLen   = 30
)

// Bounds for what a header may declare. Pages beyond epsMaxSide points
// (about 14 m) are corrupt or hostile; the pixel limits bound the rest.
const (
	epsMaxSide = 40_000.0
	// epsScanLen bounds the header comments read at the start of the
	// PostScript section and, for "(atend)" values, at its end.
	epsScanLen = 64 << 10
	// Letter, Ghostscript's default page, used for a document that
	// declares no box and no media.
	epsDefaultWidth, epsDefaultHeight = 612.0, 792.0
)

// EPSBox is a rectangle in PostScript points (1/72 inch).
type EPSBox struct {
	LLX, LLY, URX, URY float64
}

// Width is the box width in points.
func (b EPSBox) Width() float64 { return b.URX - b.LLX }

// Height is the box height in points.
func (b EPSBox) Height() float64 { return b.URY - b.LLY }

// valid reports a finite box with a positive size within epsMaxSide.
func (b EPSBox) valid() bool {
	for _, v := range [...]float64{b.LLX, b.LLY, b.URX, b.URY} {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e7 {
			return false
		}
	}
	w, h := b.Width(), b.Height()
	return w > 0 && h > 0 && w <= epsMaxSide && h <= epsMaxSide
}

// EPSHeader is what the DSC header comments of an EPS or PostScript file
// declare (Adobe TN 5001 DSC 3.0, TN 5002 EPSF 3.0).
type EPSHeader struct {
	// Box is the page area rendered, in points: the %%HiResBoundingBox
	// when present (Ghostscript prefers it too), else the %%BoundingBox,
	// else a box at the origin the size of the first %%DocumentMedia, else
	// Letter. The renderer moves its lower-left corner to the origin of
	// every page and crops the page to its size.
	Box EPSBox
	// Pages is the %%Pages count, at least 1.
	Pages int
	// EPSF reports an EPS file ("%!PS-Adobe-3.0 EPSF-3.0"); plain
	// PostScript documents and Illustrator files are not.
	EPSF bool
	// Illustrator reports a PostScript-based Adobe Illustrator file (AI 8
	// and older; later versions are PDF and sniff as FormatPDF).
	Illustrator bool
	// PS is the offset and length of the PostScript section in the file:
	// the whole file, or the section a DOS EPS binary header points to.
	PSOffset, PSLength int
	// Preview is the offset and length of the TIFF preview a DOS EPS
	// binary header carries; PreviewLength is 0 without one.
	PreviewOffset, PreviewLength int
}

// PostScript returns the PostScript section of data, the part fed to the
// interpreter (a DOS EPS binary header and its previews are left out).
func (h *EPSHeader) PostScript(data []byte) []byte {
	return data[h.PSOffset : h.PSOffset+h.PSLength]
}

// TIFFPreview returns the embedded TIFF preview of a DOS EPS file, or nil.
func (h *EPSHeader) TIFFPreview(data []byte) []byte {
	if h.PreviewLength == 0 {
		return nil
	}
	return data[h.PreviewOffset : h.PreviewOffset+h.PreviewLength]
}

// isEPS recognises a DOS EPS binary file or a PostScript document: "%!PS"
// (after an optional Ctrl-D that print spoolers prepend), or a bare "%!"
// line.
func isEPS(b []byte) bool {
	if has(b, magicDOSEPS) {
		return len(b) >= dosEPSLen
	}
	if len(b) > 0 && b[0] == 0x04 {
		b = b[1:]
	}
	return has(b, magicPS) || (len(b) > 2 && b[0] == '%' && b[1] == '!' && (b[2] == '\n' || b[2] == '\r'))
}

// ParseEPSHeader reads the DOS EPS binary header, if any, and the DSC
// header comments of the PostScript section. Values deferred with
// "(atend)" are read from the trailer. Missing boxes fall back as EPSHeader
// describes; truncated binary headers, sections outside the file and
// absurd boxes fail with errs.KindCorrupt.
func ParseEPSHeader(data []byte) (EPSHeader, error) {
	h := EPSHeader{PSLength: len(data), Pages: 1}
	if has(data, magicDOSEPS) {
		if err := h.parseDOS(data); err != nil {
			return EPSHeader{}, err
		}
	}
	ps := h.PostScript(data)
	if len(ps) > 0 && ps[0] == 0x04 {
		ps = ps[1:]
	}
	if !has(ps, "%!") {
		return EPSHeader{}, errs.New(errs.KindCorrupt, "codec: eps: no PostScript header")
	}
	var d dsc
	first, _, _ := bytes.Cut(ps[:min(len(ps), 256)], []byte("\n"))
	h.EPSF = bytes.Contains(first, []byte("EPSF"))
	d.scan(ps[:min(len(ps), epsScanLen)], true)
	if d.atEnd() {
		d.scan(ps[max(len(ps)-epsScanLen, 0):], false)
	}
	h.Illustrator = d.illustrator
	if d.pages > 0 {
		h.Pages = d.pages
	}
	switch {
	case d.hiRes.valid():
		h.Box = d.hiRes
	case d.bbox.valid():
		h.Box = d.bbox
	case d.media.valid():
		h.Box = d.media
	case d.bboxSet || d.hiResSet:
		return EPSHeader{}, errs.New(errs.KindCorrupt, "codec: eps: invalid bounding box")
	default:
		h.Box = EPSBox{URX: epsDefaultWidth, URY: epsDefaultHeight}
	}
	return h, nil
}

// parseDOS reads the 30-byte DOS EPS binary header: the PostScript
// section's offset and length at 4 and 8, the TIFF preview's at 20 and 24
// (little-endian). A WMF preview (12, 16) is ignored.
func (h *EPSHeader) parseDOS(data []byte) error {
	if len(data) < dosEPSLen {
		return errs.New(errs.KindCorrupt, "codec: eps: short binary header")
	}
	le := binary.LittleEndian
	section := func(at int) (int, int, bool) {
		// int holds any uint32 on the 64-bit platforms the images build for.
		off, n := int(le.Uint32(data[at:])), int(le.Uint32(data[at+4:]))
		if n == 0 {
			return 0, 0, true
		}
		if off < dosEPSLen || off > len(data) || n > len(data)-off {
			return 0, 0, false
		}
		return off, n, true
	}
	var ok bool
	if h.PSOffset, h.PSLength, ok = section(4); !ok || h.PSLength == 0 {
		return errs.New(errs.KindCorrupt, "codec: eps: PostScript section outside the file")
	}
	if h.PreviewOffset, h.PreviewLength, ok = section(20); !ok {
		// A broken preview pointer does not spoil the PostScript.
		h.PreviewOffset, h.PreviewLength = 0, 0
	}
	if h.PreviewLength > 0 && !isTIFFHeader(data[h.PreviewOffset:]) {
		h.PreviewOffset, h.PreviewLength = 0, 0
	}
	return nil
}

// isTIFFHeader reports a TIFF byte-order mark and magic number.
func isTIFFHeader(b []byte) bool {
	return has(b, "II*\x00") || has(b, "MM\x00*")
}

// dsc collects the DSC comments ParseEPSHeader needs.
type dsc struct {
	bbox, hiRes, media EPSBox
	// set: the header gave the value (or the trailer, for a deferred
	// one); deferred: the header said "(atend)".
	bboxSet, hiResSet, pagesSet, mediaSet      bool
	bboxDeferred, hiResDeferred, pagesDeferred bool
	pages                                      int
	illustrator                                bool
}

func (d *dsc) atEnd() bool { return d.bboxDeferred || d.hiResDeferred || d.pagesDeferred }

// scan reads DSC comment lines. In the header (header == true) it stops at
// %%EndComments or at the first line that is not a comment, and the first
// occurrence of a comment wins; in the trailer it only fills the values
// the header deferred with "(atend)", the last occurrence winning (DSC
// 3.0 §4.4).
func (d *dsc) scan(b []byte, header bool) {
	for line := range bytes.Lines(b) {
		// Classic Mac files end lines with CR only.
		for part := range bytes.SplitSeq(line, []byte("\r")) {
			part = bytes.TrimRight(part, "\n")
			if len(part) == 0 {
				continue
			}
			if header && (part[0] != '%' || bytes.HasPrefix(part, []byte("%%EndComments"))) {
				return
			}
			d.comment(part, header)
		}
	}
}

// value applies one DSC value: in the header only the first occurrence
// counts and "(atend)" defers it; in the trailer only a deferred value is
// taken.
func value(header, atend bool, set, deferred *bool, apply func() bool) {
	switch {
	case header && (*set || *deferred):
	case header && atend:
		*deferred = true
	case header, *deferred && !atend:
		if apply() {
			*set = true
		}
	}
}

// comment applies one comment line.
func (d *dsc) comment(line []byte, header bool) {
	key, val, ok := bytes.Cut(line, []byte(":"))
	if !ok {
		if header && bytes.HasPrefix(line, []byte("%AI")) {
			d.illustrator = true
		}
		return
	}
	val = bytes.TrimSpace(val)
	atend := bytes.Equal(val, []byte("(atend)"))
	switch string(key) {
	case "%%BoundingBox":
		value(header, atend, &d.bboxSet, &d.bboxDeferred, func() bool { d.bbox = parseBox(val); return true })
	case "%%HiResBoundingBox":
		value(header, atend, &d.hiResSet, &d.hiResDeferred, func() bool { d.hiRes = parseBox(val); return true })
	case "%%Pages":
		value(header, atend, &d.pagesSet, &d.pagesDeferred, func() bool {
			f := bytes.Fields(val)
			if len(f) == 0 {
				return false
			}
			n, err := strconv.Atoi(string(f[0]))
			if err != nil || n < 1 {
				return false
			}
			d.pages = n
			return true
		})
	case "%%DocumentMedia":
		// %%DocumentMedia: <name> <width> <height> <weight> <colour> <type>
		if !header || d.mediaSet {
			return
		}
		if f := bytes.Fields(val); len(f) >= 3 {
			w, errW := strconv.ParseFloat(string(f[1]), 64)
			h, errH := strconv.ParseFloat(string(f[2]), 64)
			if errW == nil && errH == nil {
				d.media, d.mediaSet = EPSBox{URX: w, URY: h}, true
			}
		}
	case "%%Creator":
		if header && bytes.Contains(val, []byte("Adobe Illustrator")) {
			d.illustrator = true
		}
	default:
		if header && bytes.HasPrefix(key, []byte("%AI")) {
			d.illustrator = true
		}
	}
}

// parseBox reads "llx lly urx ury"; anything else is an invalid box.
func parseBox(val []byte) EPSBox {
	f := bytes.Fields(val)
	if len(f) != 4 {
		return EPSBox{}
	}
	var v [4]float64
	for i := range v {
		n, err := strconv.ParseFloat(string(f[i]), 64)
		if err != nil {
			return EPSBox{}
		}
		v[i] = n
	}
	return EPSBox{LLX: v[0], LLY: v[1], URX: v[2], URY: v[3]}
}
