package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"

	"golang.org/x/image/tiff"
)

// writeEPSPreview writes a DOS EPS binary file (Adobe TN 5002): the 30-byte
// header, a PostScript section drawing a 96×72 pt graphic, and a TIFF
// preview of the procedural graphic at the same size (what Illustrator and
// Photoshop embed as the screen preview). A renderer without Ghostscript
// shows the preview; Ghostscript renders the PostScript.
func writeEPSPreview(out string) error {
	const w, h = 96, 72
	ps := fmt.Appendf(nil, `%%!PS-Adobe-3.0 EPSF-3.0
%%%%Creator: xtandard-image test fixture
%%%%Title: preview.eps
%%%%BoundingBox: 0 0 %d %d
%%%%EndComments
0.2 0.4 0.8 setrgbcolor
0 0 %d %d rectfill
1 0.8 0 setrgbcolor
%d %d 20 0 360 arc fill
showpage
%%%%EOF
`, w, h, w, h, w/2, h/2)
	var preview bytes.Buffer
	if err := tiff.Encode(&preview, graphic(w, h), &tiff.Options{Compression: tiff.Deflate}); err != nil {
		return err
	}
	const header = 30
	le := binary.LittleEndian
	var f bytes.Buffer
	var hdr [header]byte
	copy(hdr[:], "\xc5\xd0\xd3\xc6")
	le.PutUint32(hdr[4:], header)                  // PostScript offset
	le.PutUint32(hdr[8:], uint32(len(ps)))         // PostScript length
	le.PutUint32(hdr[20:], uint32(header+len(ps))) // TIFF offset
	le.PutUint32(hdr[24:], uint32(preview.Len()))  // TIFF length
	le.PutUint16(hdr[28:], 0xFFFF)                 // no checksum
	f.Write(hdr[:])
	f.Write(ps)
	f.Write(preview.Bytes())
	return os.WriteFile(out, f.Bytes(), 0o644)
}
