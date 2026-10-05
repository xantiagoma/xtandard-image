//go:build linux

package sandbox

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
	"runtime/debug"
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

// Sandbox starts the program through the helper: this binary re-executed
// with HelperArg, which confines itself and execs the program (RunHelper).
type Sandbox struct {
	helper string
	prog   string
	// cpu is RLIMIT_CPU in seconds: the wall-clock timeout, rounded up,
	// plus one.
	cpu uint64
	// uid and gid are what the helper switches to before exec (nobody
	// when the server runs as root), -1 to keep the server's.
	uid, gid int
	// netns runs the helper in new user and network namespaces.
	netns bool
	// as is the base RLIMIT_AS, 0 for none (AddressSpace).
	as uint64
}

// DetectEmulation reports the CPU emulator this process runs under
// (EmulationNone when native): /proc/cpuinfo and the Rosetta binfmt_misc
// handler, read once per call.
func DetectEmulation() Emulation {
	cpuinfo, _ := os.ReadFile("/proc/cpuinfo")
	_, err := os.Stat("/proc/sys/fs/binfmt_misc/rosetta")
	return detectEmulation(runtime.GOARCH, string(cpuinfo), err == nil)
}

// New builds the sandbox for c.Program and checks it: `<program>
// --version` through the helper, first in a new network namespace and,
// when the kernel or the seccomp profile refuses one (Docker's default
// profile does), without. It returns the layers that hold and the first
// line the program printed (its version).
func New(ctx context.Context, c *Config) (*Sandbox, Isolation, string, error) {
	helper := c.Helper
	if helper == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, Isolation{}, "", fmt.Errorf("sandbox helper: %w", err)
		}
		helper = exe
	}
	s := &Sandbox{helper: helper, prog: c.Program, cpu: uint64(math.Ceil(c.Timeout.Seconds())) + 1, uid: -1, gid: -1}
	iso := Isolation{Helper: true, ProcessGroup: true, Emulation: DetectEmulation()}
	s.as, iso.AddressSpaceOff = c.AddressSpace.base(iso.Emulation)
	iso.AddressSpace = s.as
	// The program runs as the server's user when the server is not root:
	// keep that user from reading the server's memory and environment
	// (secrets).
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
			report.apply(&iso)
			return s, iso, version, nil
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
	report.apply(&iso)
	return s, iso, version, nil
}

// helperReport is what the helper prints with -report: the layers it
// applied, read back from the kernel.
type helperReport struct {
	nnp, pdeathsig, uid, gid int
	rlimits                  bool
}

// apply records the layers the helper reported in iso.
func (h helperReport) apply(iso *Isolation) {
	iso.NoNewPrivs = h.nnp == 1
	iso.ParentDeathSignal = h.pdeathsig == int(syscall.SIGKILL)
	iso.UID, iso.GID = h.uid, h.gid
	iso.Rlimits = h.rlimits
}

// check runs `<program> --version` through the helper with -report.
func (s *Sandbox) check(ctx context.Context, tempDir string) (helperReport, string, error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	tmp, err := os.MkdirTemp(tempDir, "xi-sandbox-")
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
			KillGroup(cmd.Process.Pid)
		}
		return helperReport{}, "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	sc := bufio.NewScanner(&stdout)
	var rep helperReport
	var version string
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, HelperArg+" "); ok {
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
		return helperReport{}, "", fmt.Errorf("%s --version printed nothing", s.prog)
	}
	return rep, version, nil
}

// Command is the helper invocation that runs the program with args, in its
// own process group, with TMPDIR set to tmp (chowned to the sandbox user
// when the server runs as root). outLimit is the output the caller expects
// and sizes the address-space limit. Cancelling ctx kills the process group.
// The caller locks its OS thread from Start to Wait (the parent-death signal
// follows the thread that forked).
func (s *Sandbox) Command(ctx context.Context, tmp string, outLimit int64, args []string) (*exec.Cmd, error) {
	return s.command(ctx, tmp, outLimit, args, false)
}

// command is Command, with -report for the startup check.
func (s *Sandbox) command(ctx context.Context, tmp string, outLimit int64, progArgs []string, report bool) (*exec.Cmd, error) {
	if s.uid >= 0 {
		if err := os.Chown(tmp, s.uid, s.gid); err != nil {
			return nil, err
		}
	}
	as := addressSpaceLimit(s.as, outLimit)
	argv := []string{
		HelperArg,
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
	argv = append(append(argv, "--", s.prog), progArgs...)
	// The helper is this binary and the program the resolved executable;
	// every argument is built by the caller, none comes from the request.
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
		KillGroup(cmd.Process.Pid)
		return nil
	}
	cmd.WaitDelay = WaitDelay
	return cmd, nil
}

// RunHelper is the sandbox helper (os.Args start with HelperArg):
// it switches to the unprivileged user it was given, sets the
// parent-death signal (and exits if the server is already gone),
// no-new-privileges and the resource limits (no core file, open files, no
// file writes, CPU seconds, no new processes, address space unless -as=0), then execs
// the program with an empty environment but TMPDIR. It returns only on failure,
// with the status for os.Exit.
func RunHelper(args []string) int {
	// prctl settings are per thread: set them on the thread that execs.
	runtime.LockOSThread()
	// RLIMIT_NPROC 0 (below) forbids new threads: a Go runtime that
	// started one between the limits and execve (for another P, a GC
	// worker, or to hand off a P stuck in a syscall) would abort with
	// "pthread_create failed". One P and no GC leave it nothing to start
	// a thread for, and execConfined sets the last limits and execs
	// without a point where the goroutine could be preempted.
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(-1)
	fail := func(what string, err error) int {
		fmt.Fprintf(os.Stderr, "%s: %s: %v\n", HelperArg, what, err)
		return helperFailStatus
	}
	fs := flag.NewFlagSet(HelperArg, flag.ContinueOnError)
	as := fs.Uint64("as", 0, "RLIMIT_AS bytes (0 = none)")
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
	if len(argv) == 0 || *cpu == 0 || *nofile == 0 {
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
	type limit struct {
		res      int
		cur, max uint64
	}
	// Through syscall.Setrlimit: setting RLIMIT_NOFILE there also stops
	// syscall.Exec from restoring the soft limit the runtime raised.
	for _, l := range []limit{
		{syscall.RLIMIT_CORE, 0, 0},
		{syscall.RLIMIT_NOFILE, *nofile, *nofile},
		// Output goes to a pipe; no file may grow.
		{syscall.RLIMIT_FSIZE, 0, 0},
		// SIGXCPU at the soft limit, SIGKILL one second later.
		{syscall.RLIMIT_CPU, *cpu, *cpu + 1},
	} {
		if err := syscall.Setrlimit(l.res, &syscall.Rlimit{Cur: l.cur, Max: l.max}); err != nil {
			return fail("setrlimit "+strconv.Itoa(l.res), err)
		}
	}
	// Last (execConfined): nothing after them may start a thread.
	final := finalLimits{
		// No fork: counted per user, so the server's own threads already
		// exceed it (root is exempt, hence the switch to nobody).
		lims: [2]rawLimit{{res: rlimitNPROC}},
		n:    1,
	}
	// 0: off (XI_SANDBOX_MAX_AS=0) or under CPU emulation (AddressSpace).
	if *as > 0 {
		final.lims[1] = rawLimit{syscall.RLIMIT_AS, syscall.Rlimit{Cur: *as, Max: *as}}
		final.n = 2
	}
	env := []string{"TMPDIR=" + *tmp, "TEMP=" + *tmp}
	var line []byte
	if *report {
		nnp, _ := prctlGet(prGetNoNewPrivs)
		var sig int32
		_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, prGetPdeathsig, uintptr(unsafe.Pointer(&sig)), 0)
		line = fmt.Appendf(nil, "%s nnp=%d pdeathsig=%d uid=%d gid=%d rlimits=1\n",
			HelperArg, nnp, sig, os.Getuid(), os.Getgid())
	}
	// Everything execve needs is built before the limits.
	argv0p, err := syscall.BytePtrFromString(argv[0])
	if err != nil {
		return fail("exec "+argv[0], err)
	}
	argvp, err := syscall.SlicePtrFromStrings(argv)
	if err != nil {
		return fail("exec "+argv[0], err)
	}
	envp, err := syscall.SlicePtrFromStrings(env)
	if err != nil {
		return fail("exec "+argv[0], err)
	}
	failed, errno := execConfined(&final, line, argv0p, &argvp[0], &envp[0])
	if failed < final.n {
		return fail("setrlimit "+strconv.Itoa(final.lims[failed].res), errno)
	}
	return fail("exec "+argv[0], errno)
}

// rawLimit is one resource limit for execConfined.
type rawLimit struct {
	res int
	lim syscall.Rlimit
}

// finalLimits are the limits execConfined sets, lims[:n].
type finalLimits struct {
	lims [2]rawLimit
	n    int
}

// execConfined sets the final limits, writes report (if any) to stdout and
// execs argv0 with argv and envv (NULL-terminated), all as raw system
// calls. It returns only on failure: the index of the limit that failed,
// or l.n when execve did.
//
// Once RLIMIT_NPROC is 0 the runtime must not start a thread, and
// GOMAXPROCS(1) alone does not ensure that: if the goroutine is preempted
// (sysmon preempts any goroutine running for 10 ms of wall time, which a
// descheduled thread on a loaded machine reaches), the locked thread hands
// its P to another M so the scheduler can run, and when no idle M exists
// the runtime creates one and aborts with "pthread_create failed". This
// function is not preemptible: it is nosplit (no stack check, so no
// cooperative preemption point, and the compiler marks its body unsafe for
// asynchronous preemption but for the call instructions themselves) and
// calls only nosplit raw system calls and the assembly beneath them.
// norace keeps the race detector's instrumentation calls out of it.
//
//go:nosplit
//go:norace
func execConfined(l *finalLimits, report []byte, argv0 *byte, argv, envv **byte) (int, syscall.Errno) {
	for i := 0; i < l.n && i < len(l.lims); i++ {
		if _, _, e := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, 0, uintptr(l.lims[i].res),
			uintptr(unsafe.Pointer(&l.lims[i].lim)), 0, 0, 0); e != 0 {
			return i, e
		}
	}
	if len(report) > 0 {
		_, _, _ = syscall.RawSyscall(syscall.SYS_WRITE, 1, uintptr(unsafe.Pointer(&report[0])), uintptr(len(report)))
	}
	_, _, e := syscall.RawSyscall(syscall.SYS_EXECVE, uintptr(unsafe.Pointer(argv0)),
		uintptr(unsafe.Pointer(argv)), uintptr(unsafe.Pointer(envv)))
	return l.n, e
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
