//go:build unix && !linux

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Outside Linux (developer machines) there is no sandbox helper: the
// program runs directly, in its own process group, with an empty
// environment but TMPDIR. The images are Linux; this exists so the
// packages build and their tests run on macOS.
type Sandbox struct {
	prog string
}

const checkTimeout = 15 * time.Second

// New checks that `<program> --version` runs and returns its first line.
func New(ctx context.Context, c *Config) (*Sandbox, Isolation, string, error) {
	s := &Sandbox{prog: c.Program}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cmd, err := s.Command(ctx, os.TempDir(), 0, []string{"--version"})
	if err != nil {
		return nil, Isolation{}, "", err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, Isolation{}, "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	version, _, _ := strings.Cut(strings.TrimSpace(stdout.String()), "\n")
	if version == "" {
		return nil, Isolation{}, "", fmt.Errorf("%s --version printed nothing", s.prog)
	}
	iso := Isolation{
		ProcessGroup: true, UID: os.Geteuid(), GID: os.Getegid(),
		AddressSpaceOff: "not linux", NetNamespaceError: "not linux",
	}
	return s, iso, version, nil
}

// Command runs the program with args in its own process group.
func (s *Sandbox) Command(ctx context.Context, tmp string, _ int64, args []string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, s.prog, args...) //nolint:gosec // G204: resolved program, arguments built by the caller
	cmd.Env = []string{"TMPDIR=" + tmp, "TEMP=" + tmp}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		KillGroup(cmd.Process.Pid)
		return nil
	}
	cmd.WaitDelay = WaitDelay
	return cmd, nil
}

// DetectEmulation reports EmulationNone: the images are Linux, and outside
// Linux there are no resource limits to relax.
func DetectEmulation() Emulation { return EmulationNone }

// RunHelper exists on Linux only.
func RunHelper([]string) int {
	fmt.Fprintln(os.Stderr, HelperArg+": only on linux")
	return 2
}
