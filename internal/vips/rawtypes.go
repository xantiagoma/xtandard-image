package vips

import "time"

// RawHeader is what LibRaw reads from a camera RAW file without unpacking
// it (ProbeRaw).
type RawHeader struct {
	// Undecodable says why RawLoad would refuse the file (never demosaic
	// it); RawDecodable otherwise.
	Undecodable RawUndecodable
	// Width and Height are the size of the full demosaiced image in the
	// stored orientation (before Flip); RawLoad with HalfSize halves each
	// side, rounding up. Zero for RawUnknownCamera.
	Width, Height int
	// RawWidth and RawHeight are the sensor data size, margins included.
	RawWidth, RawHeight int
	// Flip is LibRaw's sizes.flip (dcraw convention: 0 none, 3 = 180°,
	// 5 = 90° counter-clockwise, 6 = 90° clockwise, other values mirror).
	Flip int
	// Colors is the number of colours of the sensor filter: 1 for a
	// monochrome sensor (grey output), 3 or 4 for colour.
	Colors int
	DNG    bool
	// Camera metadata; zero values are unknown.
	Make, Model, Software, Lens string
	ISO                         float64
	ShutterSeconds              float64
	Aperture                    float64 // f-number
	FocalLength                 float64 // mm
	// Captured is the capture time as the camera's wall clock (the
	// location is UTC by convention); zero when unknown.
	Captured time.Time
	// Thumbnails are the embedded previews LibRaw lists, in its order.
	Thumbnails []RawThumbnail
}

// RawLibrary describes the linked LibRaw build (RawLibraryInfo).
type RawLibrary struct {
	Version string
	Cameras int
	// X3F: Sigma X3F (Foveon) through LibRaw's X3F tools.
	X3F bool
	// DNGSDK: the Adobe DNG SDK, which decodes JPEG XL raw data (DNG 1.7).
	DNGSDK bool
	// GPRSDK: the GoPro GPR SDK's VC-5 decoder (GoPro .GPR).
	GPRSDK bool
}

// RawUndecodable is why LibRaw cannot demosaic a RAW file whose header it
// read (the shim's XI_RAW_* codes).
type RawUndecodable uint8

// RawUndecodable values.
const (
	RawDecodable RawUndecodable = iota
	// RawNikonHE is a Nikon High Efficiency NEF (HE/HE*, intoPIX
	// TicoRAW): no open decoder exists.
	RawNikonHE
	// RawNoDecoder is a compression LibRaw's build has only a placeholder
	// for (GoPro VC-5 without the GPR SDK, JPEG XL DNG without the DNG
	// SDK; of the base images only gpl's LibRaw has both, WP-103).
	RawNoDecoder
	// RawUnknownCamera is a file LibRaw refused to open (typically a body
	// newer than its release) whose embedded previews it could still list.
	RawUnknownCamera
)

// String names the reason for logs and client messages.
func (u RawUndecodable) String() string {
	switch u {
	case RawDecodable:
		return "decodable"
	case RawNikonHE:
		return "Nikon High Efficiency compression"
	case RawNoDecoder:
		return "JPEG XL or GoPro VC-5 raw data needs the gpl image"
	case RawUnknownCamera:
		return "camera not supported by LibRaw"
	default:
		return "unknown"
	}
}

// RawThumbnail is one embedded preview of a RAW file: Length bytes at
// Offset in the source buffer.
type RawThumbnail struct {
	// JPEG reports that LibRaw classifies the preview as a JPEG stream
	// (the bytes still need checking: LibRaw patches a missing SOI marker
	// and some cameras store H.265 there).
	JPEG bool
	// Width and Height come from the RAW's tags and may be zero or wrong;
	// the JPEG header is authoritative.
	Width, Height int
	Offset        int64
	Length        int
}

// RawOptions are the options of RawLoad.
type RawOptions struct {
	// HalfSize demosaics at half the width and height (LibRaw half_size:
	// each 2×2 Bayer quad becomes one pixel), about four times faster.
	HalfSize bool
	// MaxPixels > 0 limits the sensor width × height that may be
	// demosaiced (XI_LIMITS_MAX_RAW_DEMOSAIC_PIXELS).
	MaxPixels int64
}

// TextField is one string metadata field for Image.SetTexts.
type TextField struct {
	Field MetaField
	Value string
}
