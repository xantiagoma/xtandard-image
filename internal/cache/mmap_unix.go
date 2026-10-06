//go:build unix

package cache

import (
	"os"
	"syscall"
)

// mapFile maps n bytes of f at off read-only: the page-aligned mapping
// and the n bytes inside it.
func mapFile(f *os.File, off, n int64) (mapping, view []byte, err error) {
	page := int64(os.Getpagesize())
	start := off &^ (page - 1)
	rc, err := f.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var m []byte
	cerr := rc.Control(func(fd uintptr) {
		m, err = syscall.Mmap(int(fd), start, int(off-start+n), syscall.PROT_READ, syscall.MAP_SHARED) //nolint:gosec // G115: a descriptor and a mapped length fit an int
	})
	if cerr != nil {
		return nil, nil, cerr
	}
	if err != nil {
		return nil, nil, err
	}
	return m, m[off-start : off-start+n], nil
}

func unmapFile(m []byte) error { return syscall.Munmap(m) }
