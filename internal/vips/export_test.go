//go:build cgo

package vips

// LoadWith exposes loadWith to the external tests.
var LoadWith = loadWith

// RawLive exposes rawLive to the external tests.
func RawLive() int { return rawLive() }
