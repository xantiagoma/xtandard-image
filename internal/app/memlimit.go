package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// cgroup files, relative to the filesystem root (fs.FS paths).
const (
	procSelfCgroup = "proc/self/cgroup"
	cgroupRoot     = "sys/fs/cgroup"
	cgroupV1Memory = cgroupRoot + "/memory"
)

// cgroupV1Unlimited: cgroup v1 reports "no limit" as a page-rounded
// math.MaxInt64; anything this large is no limit.
const cgroupV1Unlimited = 1 << 62

// memoryLimit is the cgroup memory limit of this process.
type memoryLimit struct {
	bytes   int64  // 0: no limit
	version string // "v2" or "v1"
}

// cgroupMemoryLimit reads the memory limit of this process's cgroup from
// fsys (the filesystem root): cgroup v2 memory.max, else cgroup v1
// memory.limit_in_bytes. The cgroup comes from /proc/self/cgroup; the
// smallest limit on the way from it up to the root counts (a pod's limit
// above a container without one), and the root itself (inside a cgroup
// namespace, the container's own cgroup) is read too. "max" and the v1
// "unlimited" value are no limit. A missing file is no limit, not an
// error.
func cgroupMemoryLimit(fsys fs.FS) (memoryLimit, error) {
	self, err := fs.ReadFile(fsys, procSelfCgroup)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return memoryLimit{}, fmt.Errorf("read %s: %w", procSelfCgroup, err)
	}
	v2, v1 := cgroupPaths(self)
	if _, err := fs.Stat(fsys, cgroupRoot+"/cgroup.controllers"); err == nil {
		n, err := smallestLimit(fsys, cgroupRoot, v2, "memory.max")
		return memoryLimit{bytes: n, version: "v2"}, err
	}
	n, err := smallestLimit(fsys, cgroupV1Memory, v1, "memory.limit_in_bytes")
	return memoryLimit{bytes: n, version: "v1"}, err
}

// cgroupPaths returns this process's cgroup v2 path ("0::/path") and its
// cgroup v1 memory controller path ("N:...memory...:/path") from the
// content of /proc/self/cgroup; "/" when absent.
func cgroupPaths(self []byte) (v2, v1 string) {
	v2, v1 = "/", "/"
	sc := bufio.NewScanner(bytes.NewReader(self))
	for sc.Scan() {
		id, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		controllers, p, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		switch {
		case id == "0" && controllers == "":
			v2 = p
		case containsField(controllers, "memory"):
			v1 = p
		}
	}
	return v2, v1
}

func containsField(list, name string) bool {
	for f := range strings.SplitSeq(list, ",") {
		if f == name {
			return true
		}
	}
	return false
}

// smallestLimit reads file in base/cg and in every parent up to base, and
// returns the smallest limit found (0: none). A cgroup path that leaves
// the namespace ("/.." from outside a cgroup namespace) is read as "/".
func smallestLimit(fsys fs.FS, base, cg, file string) (int64, error) {
	cg = strings.TrimPrefix(path.Clean("/"+cg), "/")
	if !fs.ValidPath(cg) || strings.HasPrefix(cg, "..") {
		cg = "."
	}
	var smallest int64
	for {
		n, err := readLimit(fsys, path.Join(base, cg, file))
		if err != nil {
			return 0, err
		}
		if n > 0 && (smallest == 0 || n < smallest) {
			smallest = n
		}
		if cg == "." {
			return smallest, nil
		}
		cg = path.Dir(cg)
	}
}

// readLimit parses one limit file: a byte count, "max" or a v1 unlimited
// value (0), or a missing file (0).
func readLimit(fsys fs.FS, name string) (int64, error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", name, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "max" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if n <= 0 || n >= cgroupV1Unlimited {
		return 0, nil
	}
	return n, nil
}

// memLimitEnv is applyGoMemoryLimit's view of the process; the zero value
// is production (the real root filesystem and debug.SetMemoryLimit).
type memLimitEnv struct {
	fsys     fs.FS                      // nil: os.DirFS("/")
	setLimit func(n int64) (prev int64) // nil: debug.SetMemoryLimit
}

// applyGoMemoryLimit sets the Go runtime's soft memory limit (GOMEMLIMIT)
// to processing.go_memory_fraction of the cgroup memory limit, once at
// startup (ARCHITECTURE §11). It covers only Go memory: libvips and the
// codecs allocate the rest in C, so it is never the whole limit. An
// operator's GOMEMLIMIT (goMemLimit, already applied by the runtime) wins,
// and without a cgroup limit nothing changes. It logs what it decided.
// It returns the Go limit in effect (math.MaxInt64: none).
func applyGoMemoryLimit(ctx context.Context, logger *slog.Logger, cfg *config.Config, goMemLimit string, env memLimitEnv) int64 {
	setLimit := env.setLimit
	if setLimit == nil {
		setLimit = debug.SetMemoryLimit
	}
	if goMemLimit != "" {
		current := setLimit(-1)
		logger.LogAttrs(ctx, slog.LevelInfo, "go memory limit from GOMEMLIMIT",
			slog.String("gomemlimit", goMemLimit), slog.Int64("go_memory_limit_bytes", current))
		return current
	}
	fsys := env.fsys
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	lim, err := cgroupMemoryLimit(fsys)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelWarn, "go memory limit unchanged: cgroup memory limit unreadable", logging.Err(err))
		return setLimit(-1)
	}
	if lim.bytes == 0 {
		logger.LogAttrs(ctx, slog.LevelInfo, "go memory limit unchanged: no cgroup memory limit",
			slog.String("cgroup", lim.version))
		return setLimit(-1)
	}
	fraction := cfg.Processing.GoMemoryFraction
	goLimit := int64(math.Floor(fraction * float64(lim.bytes)))
	setLimit(goLimit)
	logger.LogAttrs(ctx, slog.LevelInfo, "go memory limit set",
		slog.Int64("go_memory_limit_bytes", goLimit), slog.Int64("cgroup_memory_limit_bytes", lim.bytes),
		slog.Float64("go_memory_fraction", fraction), slog.String("cgroup", lim.version))
	// The memory budget bounds libvips' decoded pixels (C memory); with
	// the Go share it should leave room inside the cgroup limit.
	if budget := int64(cfg.Processing.MemoryBudget); budget > 0 && goLimit+budget > lim.bytes {
		logger.LogAttrs(ctx, slog.LevelWarn, "go memory limit and memory budget exceed the cgroup memory limit",
			slog.Int64("go_memory_limit_bytes", goLimit), slog.Int64("memory_budget_bytes", budget),
			slog.Int64("cgroup_memory_limit_bytes", lim.bytes))
	}
	return goLimit
}
