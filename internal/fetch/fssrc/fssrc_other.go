//go:build !unix

package fssrc

import (
	"io/fs"
	"os"
)

const openFlags = os.O_RDONLY

// inode is unavailable here; the ETag relies on size and mtime alone.
func inode(fs.FileInfo) uint64 { return 0 }

func isSymlinkRefused(error) bool { return false }
