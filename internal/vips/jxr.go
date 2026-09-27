package vips

/*
#cgo pkg-config: libjxr
#cgo nocallback xi_jxr_live
#include "shim.h"
*/
import "C"

// JPEG XR is decoded by jxr.c, a libvips loader class (jxrload_buffer)
// that the shim registers over jxrlib. It needs no Go API of its own: the
// format goes through Probe, Load and Thumbnail like any libvips format.

// jxrLive counts the live jxrlib decoders and decoded buffers, whose memory
// libvips does not track (leak tests, through export_test.go).
func jxrLive() int { return int(C.xi_jxr_live()) }
