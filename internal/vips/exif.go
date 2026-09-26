package vips

/*
#cgo pkg-config: libexif
#cgo noescape xi_exif_title
#cgo nocallback xi_exif_title
#include "shim.h"
*/
import "C"

// EXIFTitle returns libexif's human-readable title of an EXIF tag ("Date
// and Time (Original)" for DateTimeOriginal in the EXIF IFD), the names
// imgproxy's /info endpoint reports. ifd and name are the parts of a
// libvips EXIF field "exif-ifd<ifd>-<name>" (see Image.Fields). ok is false
// for tags libexif does not know in that IFD. It does not need vips.Run.
func EXIFTitle(ifd int, name string) (string, bool) {
	cname := cstr(name)
	p := C.xi_exif_title(cptr(cname), C.int(ifd))
	if p == nil {
		return "", false
	}
	return C.GoString(p), true
}
