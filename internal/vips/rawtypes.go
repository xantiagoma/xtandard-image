package vips

import "time"

// RawHeader is what LibRaw reads from a camera RAW file without unpacking
// it (ProbeRaw).
type RawHeader struct {
	// Width and Height are the size of the full demosaiced image in the
	// stored orientation (before Flip); RawLoad with HalfSize halves each
	// side, rounding up.
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
