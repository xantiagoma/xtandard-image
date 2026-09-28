//go:build cgo

package vips

// LoadWith exposes loadWith to the external tests.
var LoadWith = loadWith

// RawLive exposes rawLive to the external tests.
func RawLive() int { return rawLive() }

// JXRLive exposes jxrLive to the external tests.
func JXRLive() int { return jxrLive() }

// JXLCMYKLive exposes jxlCMYKLive to the external tests.
func JXLCMYKLive() int { return jxlCMYKLive() }

// JPEGHPLive exposes jpegHPLive to the external tests.
func JPEGHPLive() int { return jpegHPLive() }
