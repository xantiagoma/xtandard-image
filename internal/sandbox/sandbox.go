// Package sandbox runs a helper program (Ghostscript's gs, WP-99; the
// metafile converter xi-metafile2svg, WP-108) as a confined subprocess
// (ADR 0011). It is pure Go (os/exec, no cgo).
//
// On Linux every run goes through a re-exec of this binary, the sandbox
// helper (main calls RunHelper first thing when IsHelper(os.Args)), which
// switches to an unprivileged user when the server runs as root, sets the
// parent-death signal, no-new-privileges and resource limits (no core
// file, open files, no file writes, CPU seconds, no new processes, and the
// address space unless it is off or the CPU is emulated: AddressSpace) and
// then execs the program with an empty environment but TMPDIR, in a new
// network namespace when the kernel and the container's seccomp profile
// allow one. The program leads its own process group, killed as a whole on
// cancellation. Elsewhere (developer machines) the program runs directly in
// its own process group.
//
// New checks the sandbox by running `<program> --version` through it, so
// every program run this way must answer --version.
package sandbox

import (
	"log/slog"
	"strings"
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
	// DefaultAddressSpace is the base RLIMIT_AS (XI_SANDBOX_MAX_AS):
	// interpreter, fonts, band list.
	DefaultAddressSpace = 1 << 30
	maxAddressSpace     = 16 << 30
	maxOpenFiles        = 64
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
	// AddressSpace configures RLIMIT_AS.
	AddressSpace AddressSpace
}

// AddressSpace configures the address-space limit (RLIMIT_AS) of the
// confined program (XI_SANDBOX_MAX_AS, XI_SANDBOX_DETECT_EMULATION). The
// zero value is the default: DefaultAddressSpace plus twice the expected
// output, and no limit under CPU emulation.
type AddressSpace struct {
	// Base is the limit before the output allowance (0 =
	// DefaultAddressSpace).
	Base uint64
	// Off turns RLIMIT_AS off (XI_SANDBOX_MAX_AS=0).
	Off bool
	// KeepUnderEmulation applies it under detected CPU emulation too
	// (XI_SANDBOX_DETECT_EMULATION=false). Rosetta and QEMU user mode map
	// the translator's own memory inside the process, so the limit fails
	// the program before it runs ("rosetta error: mmap_anonymous_rw mmap
	// failed").
	KeepUnderEmulation bool
}

// base returns the base RLIMIT_AS under the emulator emu (0 = no limit)
// and, when there is none, why.
func (a AddressSpace) base(emu Emulation) (uint64, string) {
	switch {
	case a.Off:
		return 0, "disabled"
	case emu != EmulationNone && !a.KeepUnderEmulation:
		return 0, "emulation: " + string(emu)
	case a.Base == 0:
		return DefaultAddressSpace, ""
	default:
		return a.Base, ""
	}
}

// addressSpaceLimit is RLIMIT_AS for a run expecting outLimit bytes of
// output: base plus twice that, capped at maxAddressSpace (or at base when
// it is larger). No limit (0) stays 0.
func addressSpaceLimit(base uint64, outLimit int64) uint64 {
	if base == 0 {
		return 0
	}
	return min(base+2*uint64(max(outLimit, 0)), max(maxAddressSpace, base))
}

// Emulation names the CPU emulator this process runs under.
type Emulation string

// The emulators DetectEmulation recognises.
const (
	EmulationNone    Emulation = ""
	EmulationRosetta Emulation = "rosetta"
	EmulationQEMU    Emulation = "qemu"
)

// detectEmulation tells from the process's architecture (runtime.GOARCH),
// /proc/cpuinfo and whether the kernel has a Rosetta binfmt_misc handler
// (visible only when binfmt_misc is mounted, rarely in a container)
// whether this process is translated. Rosetta for Linux (OrbStack, Docker
// Desktop, Apple's Virtualization framework) reports a "VirtualApple" x86
// CPU; QEMU user mode passes the host's cpuinfo through, so an x86 process
// sees an ARM CPU (no vendor_id) and an ARM process an x86 one.
func detectEmulation(goarch, cpuinfo string, rosettaBinfmt bool) Emulation {
	x86CPU := strings.Contains(cpuinfo, "vendor_id")
	switch goarch {
	case "amd64", "386":
		switch {
		case strings.Contains(cpuinfo, "VirtualApple"), rosettaBinfmt:
			return EmulationRosetta
		case cpuinfo != "" && !x86CPU:
			return EmulationQEMU
		}
	case "arm64", "arm":
		if x86CPU {
			return EmulationQEMU
		}
	}
	return EmulationNone
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
	// Rlimits: CPU time, file size, open files and processes are limited
	// and core files are off.
	Rlimits bool
	// AddressSpace is the base RLIMIT_AS (the output allowance comes on
	// top), 0 when there is none; AddressSpaceOff then says why
	// ("disabled", or "emulation: <emulator>").
	AddressSpace    uint64
	AddressSpaceOff string
	// Emulation is the CPU emulator detected ("" = native).
	Emulation Emulation
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
		slog.Uint64("rlimit_as", i.AddressSpace),
		slog.String("emulation", string(i.Emulation)),
		slog.Int("uid", i.UID),
		slog.Int("gid", i.GID),
		slog.Bool("dropped_root", i.DroppedRoot),
		slog.Bool("net_namespace", i.NetNamespace),
		slog.Bool("server_non_dumpable", i.ServerNonDumpable),
	}
	if i.AddressSpaceOff != "" {
		attrs = append(attrs, slog.String("rlimit_as_off", i.AddressSpaceOff))
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
