//go:build unix

package fssrc

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// openFlags opens read-only and non-blocking, so a path swapped for a FIFO
// between Stat and Open cannot block the fetch (Fetch then refuses it as not
// a regular file). O_NONBLOCK has no effect on reads from regular files.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK

// inode returns the file's inode number, or 0 when unavailable.
func inode(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

// isSymlinkRefused reports a symlink loop (too many levels of links).
func isSymlinkRefused(err error) bool {
	return errors.Is(err, syscall.ELOOP)
}
