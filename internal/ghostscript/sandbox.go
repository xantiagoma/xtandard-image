package ghostscript

import (
	"log/slog"
	"syscall"
)

// helperArg is argv[1] of the sandbox helper: this binary re-executed to
// confine itself before it execs gs (sandbox_linux.go).
const helperArg = "__xi-gs-sandbox"

// IsSandboxHelper reports whether args (os.Args) start the sandbox helper.
// main must then call RunSandboxHelper before anything else and exit with
// its status.
func IsSandboxHelper(args []string) bool {
	return len(args) > 1 && args[1] == helperArg
}

// Isolation lists the layers a render runs under (ADR 0011). The server
// logs it once at startup.
type Isolation struct {
	// Helper: renders go through the sandbox helper (Linux).
	Helper bool
	// ProcessGroup: gs leads its own process group, killed as a whole.
	ProcessGroup bool
	// ParentDeathSignal: gs gets SIGKILL when the server dies.
	ParentDeathSignal bool
	// NoNewPrivs: PR_SET_NO_NEW_PRIVS, so no exec can gain privileges.
	NoNewPrivs bool
	// Rlimits: address space, CPU time, file size, open files and
	// processes are limited.
	Rlimits bool
	// UID and GID gs runs as; DroppedRoot when the server runs as root and
	// the helper switched to nobody.
	UID, GID    int
	DroppedRoot bool
	// NetNamespace: gs runs in a new, empty network namespace (a new user
	// namespace too); NetNamespaceError says why not when it is false.
	NetNamespace      bool
	NetNamespaceError string
	// ServerNonDumpable: the server process is PR_SET_DUMPABLE 0, so a
	// process of the same user (a compromised gs) cannot ptrace it or read
	// its /proc memory and environment.
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

// killGroup kills the process group pgid (gs and anything it started).
func killGroup(pgid int) {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
