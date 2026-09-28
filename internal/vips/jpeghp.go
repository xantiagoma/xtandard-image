package vips

/*
#cgo pkg-config: libjpeg
#cgo nocallback xi_jpeghp_live
#include "shim.h"
*/
import "C"

// High-precision JPEG (12-bit DCT and lossless, 2–16 bits) is decoded by
// jpeghp.c, a libvips loader class (jpeghpload_buffer) that the shim
// registers over libjpeg-turbo 3's 12- and 16-bit APIs ahead of jpegload,
// for the files jpegload's 8-bit API cannot read. It needs no Go API of its
// own: the format goes through Probe, Load and Thumbnail like any libvips
// format (FormatJPEG, 16-bit when the precision is above 8 bits).

// jpegHPLive counts the live libjpeg decompressors of the loader, whose
// memory libvips does not track (leak tests, through export_test.go).
func jpegHPLive() int { return int(C.xi_jpeghp_live()) }
