//go:build unix && !linux

package ghostscript

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Outside Linux (developer machines) there is no sandbox helper: gs runs
// directly, in its own process group, under -dSAFER only. The images are
// Linux; this exists so the package builds and its tests run on macOS.
type sandbox struct {
	gs string
}

const checkTimeout = 15 * time.Second

func newSandbox(ctx context.Context, c *Config) (*sandbox, Isolation, string, error) {
	s := &sandbox{gs: c.Path}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cmd, err := s.command(ctx, os.TempDir(), 0, []string{"--version"}, false)
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
		return nil, Isolation{}, "", errors.New("gs --version printed nothing")
	}
	iso := Isolation{ProcessGroup: true, UID: os.Geteuid(), GID: os.Getegid(), NetNamespaceError: "not linux"}
	return s, iso, version, nil
}

func (s *sandbox) command(ctx context.Context, tmp string, _ int64, gsArgs []string, _ bool) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, s.gs, gsArgs...) //nolint:gosec // G204: resolved gs path, arguments built by this package
	cmd.Env = []string{"TMPDIR=" + tmp, "TEMP=" + tmp}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		killGroup(cmd.Process.Pid)
		return nil
	}
	cmd.WaitDelay = waitDelay
	return cmd, nil
}

// RunSandboxHelper exists on Linux only.
func RunSandboxHelper([]string) int {
	fmt.Fprintln(os.Stderr, helperArg+": only on linux")
	return 2
}
