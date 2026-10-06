//go:build !unix

package cache

import (
	"errors"
	"os"
)

// mapFile is unavailable: FileBuffer.Bytes reads the bytes instead.
func mapFile(*os.File, int64, int64) (mapping, view []byte, err error) {
	return nil, nil, errors.New("cache: no mmap on this platform")
}

func unmapFile([]byte) error { return nil }
