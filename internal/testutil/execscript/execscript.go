// Package execscript writes the fake executables (shell scripts standing in
// for gs, xi-metafile2svg, ...) that tests run, without the ETXTBSY race.
//
// Why a plain os.WriteFile flakes: the write descriptor carries O_CLOEXEC,
// but a fork by another goroutine (a parallel test starting its own
// subprocess) between the open and the close copies it into the child,
// where it stays open until the child execs. An execve of the script while
// any process holds it open for writing fails with ETXTBSY ("text file
// busy"), golang/go#22315; on a loaded machine the child's fork-to-exec
// window is wide enough to hit.
//
// The fix is the one syscall.ForkLock documents: every fork holds it for
// writing, so creating and closing the descriptor under it for reading
// means no child can inherit it.
package execscript

import (
	"os"
	"syscall"
	"testing"
)

// Write creates (or replaces) path with script, mode 0755, and fails tb
// on error. The descriptor is never visible to a forked child.
func Write(tb testing.TB, path, script string) {
	tb.Helper()
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, []byte(script), 0o755) //nolint:gosec // G306: the script must be executable
	syscall.ForkLock.RUnlock()
	if err != nil {
		tb.Fatal(err)
	}
}
