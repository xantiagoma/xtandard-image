package vips

// Types of the WP-108 long-tail entry points (LoadMagickCoder,
// BasisKTX2Info, DecodeBasisKTX2), shared by the cgo implementation and the
// !cgo stubs.

// MagickCoder is an ImageMagick coder the app names explicitly
// (LoadMagickCoder) instead of letting ImageMagick guess it from the first
// bytes. The values are the shim's XI_MAGICK_* (checked in enums_check.go).
type MagickCoder int

// Named coders.
const (
	// MagickPICT is Macintosh PICT (the 512-byte header variant).
	MagickPICT MagickCoder = 1
	// MagickPCD is a Kodak Photo CD image pack, decoded at its Base size
	// (768×512).
	MagickPCD MagickCoder = 2
)

// BasisInfo is what the Basis Universal transcoder reads from a KTX2
// header: level 0's size and the payload.
type BasisInfo struct {
	Width, Height int
	Alpha         bool
	// HDR reports half-float pixels (UASTC HDR, ASTC HDR).
	HDR bool
	// Format is basist::basis_tex_format.
	Format int
}

// BasisImage is a transcoded first image: straight RGBA, either 8-bit
// (RGBA8) or, for HDR payloads, linear half floats (RGBAHalf, IEEE 754
// binary16).
type BasisImage struct {
	BasisInfo
	RGBA8    []byte
	RGBAHalf []uint16
}
