//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// openDir returns a t.TempDir() that other users can traverse: when the
// tests run as root the helper drops to nobody.
func openDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{dir, filepath.Dir(dir)} {
		if err := os.Chmod(d, 0o755); err != nil { //nolint:gosec // G302: a test directory the sandbox user must traverse
			t.Fatal(err)
		}
	}
	return dir
}

// TestAddressSpaceApplied runs a program through the real helper that
// prints its RLIMIT_AS (`ulimit -v`, KiB): the configured limit, or none
// when it is off or the CPU is emulated.
func TestAddressSpaceApplied(t *testing.T) {
	t.Parallel()
	dir := openDir(t)
	prog := filepath.Join(dir, "probe")
	script := "#!/bin/sh\necho 'probe 1.0'\nulimit -v\n"
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil { //nolint:gosec // G306: the probe must be executable
		t.Fatal(err)
	}
	emulated := DetectEmulation() != EmulationNone
	kib := func(b uint64) string { return strconv.FormatUint(b>>10, 10) }
	wantDefault := kib(DefaultAddressSpace)
	if emulated {
		wantDefault = "unlimited"
	}
	for _, tc := range []struct {
		name string
		as   AddressSpace
		out  int64
		want string
	}{
		{"default", AddressSpace{}, 0, wantDefault},
		{"output allowance", AddressSpace{KeepUnderEmulation: true}, 64 << 20, kib(DefaultAddressSpace + 128<<20)},
		{"configured", AddressSpace{Base: 2 << 30, KeepUnderEmulation: true}, 0, kib(2 << 30)},
		{"off", AddressSpace{Off: true}, 0, "unlimited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if emulated && tc.as.KeepUnderEmulation {
				t.Skip("an address-space limit breaks the program under CPU emulation")
			}
			s, iso, version, err := New(t.Context(), &Config{
				Program: prog, Timeout: 5 * time.Second, TempDir: dir, NoNetNamespace: true, AddressSpace: tc.as,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if version != "probe 1.0" || !iso.Rlimits || !iso.NoNewPrivs {
				t.Errorf("version %q, isolation %+v", version, iso)
			}
			if (iso.AddressSpace == 0) != (tc.want == "unlimited") || (iso.AddressSpace == 0) != (iso.AddressSpaceOff != "") {
				t.Errorf("isolation address space %d, off %q; want limit %s", iso.AddressSpace, iso.AddressSpaceOff, tc.want)
			}
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			cmd, err := s.Command(t.Context(), dir, tc.out, nil)
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v: %s", err, stderr.String())
			}
			lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
			if got := lines[len(lines)-1]; got != tc.want {
				t.Errorf("ulimit -v = %s, want %s", got, tc.want)
			}
		})
	}
}
