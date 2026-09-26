//go:build unix

package fssrc

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/source"
)

// A FIFO must be refused without blocking on open.
func TestFetchFIFO(t *testing.T) {
	t.Parallel()
	root := fixture(t)
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.png"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	s := newSource(t, root, false)
	_, err := s.Fetch(t.Context(), key("pipe.png"), source.FetchOptions{})
	if got := errs.KindOf(err); got != errs.KindNotFound {
		t.Fatalf("kind = %v (err %v), want NotFound", got, err)
	}
}
