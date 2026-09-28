//go:build linux

package ghostscript

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Linux constants the syscall package does not name.
const (
	prSetPdeathsig   = 1
	prGetPdeathsig   = 2
	prSetDumpable    = 4
	prSetNoNewPrivs  = 38
	prGetNoNewPrivs  = 39
	rlimitNPROC      = 6
	nobody           = 65534
	helperFailStatus = 126
	checkTimeout     = 15 * time.Second
)

// sandbox starts gs through the helper: this binary re-executed with
// helperArg, which confines itself and execs gs (RunSandboxHelper).
type sandbox struct {
	helper string
	gs     string
	// cpu is RLIMIT_CPU in seconds: the wall-clock timeout, rounded up,
	// plus one.
	cpu uint64
	// uid and gid are what the helper switches to before exec (nobody
	// when the server runs as root), -1 to keep the server's.
	uid, gid int
	// netns runs the helper in new user and network namespaces.
	netns bool
}

// newSandbox builds the sandbox and checks it: `gs --version` through the
// helper, first in a new network namespace and, when the kernel or the
// seccomp profile refuses one (Docker's default profile does), without.
func newSandbox(ctx context.Context, c *Config) (*sandbox, Isolation, string, error) {
	helper := c.Helper
	if helper == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, Isolation{}, "", fmt.Errorf("sandbox helper: %w", err)
		}
		helper = exe
	}
	s := &sandbox{helper: helper, gs: c.Path, cpu: uint64(math.Ceil(c.Timeout.Seconds())) + 1, uid: -1, gid: -1}
	iso := Isolation{Helper: true, ProcessGroup: true}
	// gs runs as the server's user when the server is not root: keep that
	// user from reading the server's memory and environment (secrets).
	if err := prctl(prSetDumpable, 0); err != nil {
		return nil, Isolation{}, "", fmt.Errorf("PR_SET_DUMPABLE: %w", err)
	}
	iso.ServerNonDumpable = true
	if os.Geteuid() == 0 {
		s.uid, s.gid, iso.DroppedRoot = nobody, nobody, true
	}
	var netErr error
	if !c.NoNetNamespace {
		s.netns = true
		report, version, err := s.check(ctx, c.TempDir)
		if err == nil {
			iso.NetNamespace = true
			return s, report.apply(iso), version, nil
		}
		netErr = err
		s.netns = false
	}
	report, version, err := s.check(ctx, c.TempDir)
	if err != nil {
		return nil, Isolation{}, "", err
	}
	if netErr != nil {
		iso.NetNamespaceError = netErr.Error()
	} else {
		iso.NetNamespaceError = "disabled"
	}
	return s, report.apply(iso), version, nil
}

// helperReport is what the helper prints with -report: the layers it
// applied, read back from the kernel.
type helperReport struct {
	nnp, pdeathsig, uid, gid int
	rlimits                  bool
}

func (h helperReport) apply(iso Isolation) Isolation {
	iso.NoNewPrivs = h.nnp == 1
	iso.ParentDeathSignal = h.pdeathsig == int(syscall.SIGKILL)
	iso.UID, iso.GID = h.uid, h.gid
	iso.Rlimits = h.rlimits
	return iso
}

// check runs `gs --version` through the helper with -report.
func (s *sandbox) check(ctx context.Context, tempDir string) (helperReport, string, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	tmp, err := os.MkdirTemp(tempDir, "xi-gs-")
	if err != nil {
		return helperReport{}, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd, err := s.command(ctx, tmp, 0, []string{"--version"}, true)
	if err != nil {
		return helperReport{}, "", err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if cmd.Process != nil {
			killGroup(cmd.Process.Pid)
		}
		return helperReport{}, "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	sc := bufio.NewScanner(&stdout)
	var rep helperReport
	var version string
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, helperArg+" "); ok {
			for f := range strings.FieldsSeq(rest) {
				k, v, _ := strings.Cut(f, "=")
				n, _ := strconv.Atoi(v)
				switch k {
				case "nnp":
					rep.nnp = n
				case "pdeathsig":
					rep.pdeathsig = n
				case "uid":
					rep.uid = n
				case "gid":
					rep.gid = n
				case "rlimits":
					rep.rlimits = n == 1
				}
			}
			continue
		}
		if version == "" {
			version = strings.TrimSpace(line)
		}
	}
	if version == "" {
		return helperReport{}, "", errors.New("gs --version printed nothing")
	}
	return rep, version, nil
}

// command is the helper invocation that runs gs with gsArgs. outLimit
// sizes the address-space limit (0 for the startup check).
func (s *sandbox) command(ctx context.Context, tmp string, outLimit int64, gsArgs []string, report bool) (*exec.Cmd, error) {
	if s.uid >= 0 {
		if err := os.Chown(tmp, s.uid, s.gid); err != nil {
			return nil, err
		}
	}
	as := min(baseAddressSpace+2*uint64(max(outLimit, 0)), maxAddressSpace)
	argv := []string{
		helperArg,
		"-as=" + strconv.FormatUint(as, 10),
		"-cpu=" + strconv.FormatUint(s.cpu, 10),
		"-nofile=" + strconv.Itoa(maxOpenFiles),
		"-uid=" + strconv.Itoa(s.uid),
		"-gid=" + strconv.Itoa(s.gid),
		"-ppid=" + strconv.Itoa(os.Getpid()),
		"-tmp=" + tmp,
	}
	if report {
		argv = append(argv, "-report")
	}
	argv = append(append(argv, "--", s.gs), gsArgs...)
	// The helper is this binary and gs the resolved executable; every
	// argument is built here, none comes from the request.
	cmd := exec.CommandContext(ctx, s.helper, argv...) //nolint:gosec // G204: fixed executables, arguments built by this package
	attr := &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if s.netns {
		attr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET
		uid, gid := os.Geteuid(), os.Getegid()
		attr.UidMappings = []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}}
		attr.GidMappings = []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}}
		if s.uid >= 0 && s.uid != uid {
			attr.UidMappings = append(attr.UidMappings, syscall.SysProcIDMap{ContainerID: s.uid, HostID: s.uid, Size: 1})
			attr.GidMappings = append(attr.GidMappings, syscall.SysProcIDMap{ContainerID: s.gid, HostID: s.gid, Size: 1})
			attr.GidMappingsEnableSetgroups = true
		}
	}
	cmd.SysProcAttr = attr
	cmd.Cancel = func() error {
		killGroup(cmd.Process.Pid)
		return nil
	}
	cmd.WaitDelay = waitDelay
	return cmd, nil
}

// RunSandboxHelper is the sandbox helper (os.Args start with helperArg):
// it switches to the unprivileged user it was given, sets the
// parent-death signal (and exits if the server is already gone),
// no-new-privileges and the resource limits (no core file, open files, no
// file writes, CPU seconds, no new processes, address space), then execs
// gs with an empty environment but TMPDIR. It returns only on failure,
// with the status for os.Exit.
func RunSandboxHelper(args []string) int {
	// prctl settings are per thread: set them on the thread that execs.
	runtime.LockOSThread()
	fail := func(what string, err error) int {
		fmt.Fprintf(os.Stderr, "%s: %s: %v\n", helperArg, what, err)
		return helperFailStatus
	}
	fs := flag.NewFlagSet(helperArg, flag.ContinueOnError)
	as := fs.Uint64("as", 0, "RLIMIT_AS bytes")
	cpu := fs.Uint64("cpu", 0, "RLIMIT_CPU seconds")
	nofile := fs.Uint64("nofile", 0, "RLIMIT_NOFILE")
	uid := fs.Int("uid", -1, "user to switch to")
	gid := fs.Int("gid", -1, "group to switch to")
	ppid := fs.Int("ppid", 0, "expected parent")
	tmp := fs.String("tmp", "", "TMPDIR")
	report := fs.Bool("report", false, "print the applied layers")
	if len(args) < 2 {
		return fail("args", errors.New("missing"))
	}
	if err := fs.Parse(args[2:]); err != nil {
		return fail("args", err)
	}
	argv := fs.Args()
	if len(argv) == 0 || *as == 0 || *cpu == 0 || *nofile == 0 {
		return fail("args", errors.New("missing limits or program"))
	}
	if *gid >= 0 {
		if err := syscall.Setgroups(nil); err != nil {
			return fail("setgroups", err)
		}
		if err := syscall.Setgid(*gid); err != nil {
			return fail("setgid", err)
		}
	}
	if *uid >= 0 {
		if err := syscall.Setuid(*uid); err != nil {
			return fail("setuid", err)
		}
	}
	// A change of credentials clears the parent-death signal: set it now,
	// then make sure the parent did not die before it was set.
	if err := prctl(prSetPdeathsig, uintptr(syscall.SIGKILL)); err != nil {
		return fail("PR_SET_PDEATHSIG", err)
	}
	if *ppid != 0 && os.Getppid() != *ppid {
		return fail("parent", errors.New("parent exited"))
	}
	if err := prctl(prSetNoNewPrivs, 1); err != nil {
		return fail("PR_SET_NO_NEW_PRIVS", err)
	}
	limits := []struct {
		res      int
		cur, max uint64
	}{
		{syscall.RLIMIT_CORE, 0, 0},
		{syscall.RLIMIT_NOFILE, *nofile, *nofile},
		// Output goes to a pipe; no file may grow.
		{syscall.RLIMIT_FSIZE, 0, 0},
		// SIGXCPU at the soft limit, SIGKILL one second later.
		{syscall.RLIMIT_CPU, *cpu, *cpu + 1},
		// No fork: counted per user, so the server's own threads already
		// exceed it (root is exempt, hence the switch to nobody).
		{rlimitNPROC, 0, 0},
		// Last: nothing below allocates much.
		{syscall.RLIMIT_AS, *as, *as},
	}
	for _, l := range limits {
		if err := syscall.Setrlimit(l.res, &syscall.Rlimit{Cur: l.cur, Max: l.max}); err != nil {
			return fail("setrlimit "+strconv.Itoa(l.res), err)
		}
	}
	if *report {
		nnp, _ := prctlGet(prGetNoNewPrivs)
		var sig int32
		_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, prGetPdeathsig, uintptr(unsafe.Pointer(&sig)), 0)
		fmt.Fprintf(os.Stdout, "%s nnp=%d pdeathsig=%d uid=%d gid=%d rlimits=1\n",
			helperArg, nnp, sig, os.Getuid(), os.Getgid())
	}
	env := []string{"TMPDIR=" + *tmp, "TEMP=" + *tmp}
	err := syscall.Exec(argv[0], argv, env) //nolint:gosec // G204: gs path and arguments come from the parent, never from a request
	return fail("exec "+argv[0], err)
}

// prctl calls prctl(2) with one argument.
func prctl(option, arg uintptr) error {
	if _, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, option, arg, 0, 0, 0, 0); e != 0 {
		return e
	}
	return nil
}

// prctlGet returns prctl(2)'s result for a getter.
func prctlGet(option uintptr) (int, error) {
	r, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, option, 0, 0, 0, 0, 0)
	if e != 0 {
		return 0, e
	}
	return int(r), nil
}
