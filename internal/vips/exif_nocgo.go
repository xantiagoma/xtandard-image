//go:build !cgo

package vips

// EXIFTitle knows no titles without libexif.
func EXIFTitle(int, string) (string, bool) { return "", false }
