// Package sandbox runs a helper program (Ghostscript's gs, WP-99; the
// metafile converter xi-metafile2svg, WP-108) as a confined subprocess
// (ADR 0011). It is pure Go (os/exec, no cgo).
//
// On Linux every run goes through a re-exec of this binary, the sandbox
// helper (main calls RunHelper first thing when IsHelper(os.Args)), which
// switches to an unprivileged user when the server runs as root, sets the
// parent-death signal, no-new-privileges and resource limits (no core
// file, open files, no file writes, CPU seconds, no new processes, address
// space) and then execs the program with an empty environment but TMPDIR,
// in a new network namespace when the kernel and the container's seccomp
// profile allow one. The program leads its own process group, killed as a
// whole on cancellation. Elsewhere (developer machines) the program runs
// directly in its own process group.
//
// New checks the sandbox by running `<program> --version` through it, so
// every program run this way must answer --version.
package sandbox

import (
	"log/slog"
	"syscall"
	"time"
)

// HelperArg is argv[1] of the sandbox helper: this binary re-executed to
// confine itself before it execs the program (sandbox_linux.go). Its value
// predates the package (Ghostscript, WP-99) and stays for compatibility.
const HelperArg = "__xi-gs-sandbox"

// Limits of the confined process. The address space grows with the
// expected output.
const (
	baseAddressSpace = 1 << 30 // interpreter, fonts, band list
	maxAddressSpace  = 16 << 30
	maxOpenFiles     = 64
	// WaitDelay bounds how long Wait waits for the pipes after the process
	// group was killed.
	WaitDelay = 2 * time.Second
)

// IsHelper reports whether args (os.Args) start the sandbox helper. main
// must then call RunHelper before anything else and exit with its status.
func IsHelper(args []string) bool {
	return len(args) > 1 && args[1] == HelperArg
}

// Config configures a Sandbox.
type Config struct {
	// Program is the executable to confine, already resolved (an absolute
	// path).
	Program string
	// Timeout is the wall-clock bound of one run; RLIMIT_CPU is it,
	// rounded up, plus one second.
	Timeout time.Duration
	// TempDir is where the check's private TMPDIR is created ("" =
	// os.TempDir()).
	TempDir string
	// Helper is the executable re-executed as the sandbox helper on Linux
	// ("" = this process's executable, which must call RunHelper first
	// thing in main; tests do it in TestMain).
	Helper string
	// NoNetNamespace skips the network-namespace attempt (tests).
	NoNetNamespace bool
}

// Isolation lists the layers a run is confined by (ADR 0011). The server
// logs it once at startup.
type Isolation struct {
	// Helper: runs go through the sandbox helper (Linux).
	Helper bool
	// ProcessGroup: the program leads its own process group, killed as a
	// whole.
	ProcessGroup bool
	// ParentDeathSignal: the program gets SIGKILL when the server dies.
	ParentDeathSignal bool
	// NoNewPrivs: PR_SET_NO_NEW_PRIVS, so no exec can gain privileges.
	NoNewPrivs bool
	// Rlimits: address space, CPU time, file size, open files and
	// processes are limited.
	Rlimits bool
	// UID and GID the program runs as; DroppedRoot when the server runs as
	// root and the helper switched to nobody.
	UID, GID    int
	DroppedRoot bool
	// NetNamespace: the program runs in a new, empty network namespace (a
	// new user namespace too); NetNamespaceError says why not when it is
	// false.
	NetNamespace      bool
	NetNamespaceError string
	// ServerNonDumpable: the server process is PR_SET_DUMPABLE 0, so a
	// process of the same user (a compromised program) cannot ptrace it or
	// read its /proc memory and environment.
	ServerNonDumpable bool
}

// LogAttrs are the startup log attributes of i.
func (i *Isolation) LogAttrs() []slog.Attr {
	attrs := []slog.Attr{
		slog.Bool("sandbox_helper", i.Helper),
		slog.Bool("process_group", i.ProcessGroup),
		slog.Bool("parent_death_signal", i.ParentDeathSignal),
		slog.Bool("no_new_privs", i.NoNewPrivs),
		slog.Bool("rlimits", i.Rlimits),
		slog.Int("uid", i.UID),
		slog.Int("gid", i.GID),
		slog.Bool("dropped_root", i.DroppedRoot),
		slog.Bool("net_namespace", i.NetNamespace),
		slog.Bool("server_non_dumpable", i.ServerNonDumpable),
	}
	if i.NetNamespaceError != "" {
		attrs = append(attrs, slog.String("net_namespace_error", i.NetNamespaceError))
	}
	return attrs
}

// KillGroup kills the process group pgid (the program and anything it
// started).
func KillGroup(pgid int) {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
