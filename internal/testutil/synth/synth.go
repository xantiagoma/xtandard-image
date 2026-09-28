// Package synth builds test inputs too large to commit as fixtures (WP-108):
// a Kodak Photo CD image pack is at least 768 KiB because its Base image
// starts at sector 96, so the tests write one instead of reading it.
package synth

// Photo CD layout (ImageMagick coders/pcd.c): three 2 KiB header sectors
// ("PCD_IPI" at 2048, the rotation in the low two bits of byte 0x0E02),
// then, 93 sectors later, the Base image: for every pair of rows, two
// 768-byte luma rows and a 384-byte row of each chroma channel (PhotoYCC
// 4:2:0).
const (
	pcdSector   = 0x800
	pcdBaseAt   = 96 * pcdSector
	pcdWidth    = 768
	pcdHeight   = 512
	pcdRotation = 0x0E02
	// PCDSize is the size of the image pack PCD writes.
	PCDSize = pcdBaseAt + pcdWidth*pcdHeight*3/2
)

// PCD returns a Photo CD image pack whose Base image is a horizontal luma
// ramp with neutral chroma (PhotoYCC C1 = 156, C2 = 137), with the given
// rotation code (0-3: ImageMagick turns 1 and 3 by 90°). The first 2 KiB
// are 0xFF, as Kodak's writers fill them.
func PCD(rotation byte) []byte {
	b := make([]byte, PCDSize)
	for i := range pcdSector {
		b[i] = 0xFF
	}
	copy(b[pcdSector:], "PCD_IPI")
	b[pcdRotation] = rotation & 0x03
	p := pcdBaseAt
	luma := make([]byte, pcdWidth)
	for x := range luma {
		luma[x] = byte(16 + x*200/pcdWidth)
	}
	for range pcdHeight / 2 {
		p += copy(b[p:], luma)
		p += copy(b[p:], luma)
		for range pcdWidth / 2 {
			b[p] = 156
			p++
		}
		for range pcdWidth / 2 {
			b[p] = 137
			p++
		}
	}
	return b
}
