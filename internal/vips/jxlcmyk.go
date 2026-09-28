package vips

/*
#cgo pkg-config: libjxl
#cgo nocallback xi_jxlcmyk_live
#include "shim.h"
*/
import "C"

// CMYK JPEG XL is decoded by jxlcmyk.c, a libvips loader class
// (jxlcmykload_buffer) that the shim registers over libjxl ahead of
// jxlload, for the files whose black extra channel jxlload cannot read. It
// needs no Go API of its own: the format goes through Probe, Load and
// Thumbnail like any libvips format (FormatJXL).

// jxlCMYKLive counts the live libjxl decoders and decoded buffers, whose
// memory libvips does not track (leak tests, through export_test.go).
func jxlCMYKLive() int { return int(C.xi_jxlcmyk_live()) }
