package app

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/config"
)

func cgFile(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func TestCgroupMemoryLimit(t *testing.T) {
	t.Parallel()
	const gib = 1 << 30
	v2 := func(files fstest.MapFS) fstest.MapFS {
		files["sys/fs/cgroup/cgroup.controllers"] = cgFile("cpu memory io\n")
		return files
	}
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		want    memoryLimit
		wantErr bool
	}{
		{"nothing mounted", fstest.MapFS{}, memoryLimit{version: "v1"}, false},
		{"v2 namespace root", v2(fstest.MapFS{
			"proc/self/cgroup":         cgFile("0::/\n"),
			"sys/fs/cgroup/memory.max": cgFile("1073741824\n"),
		}), memoryLimit{bytes: gib, version: "v2"}, false},
		{"v2 max", v2(fstest.MapFS{
			"proc/self/cgroup":         cgFile("0::/\n"),
			"sys/fs/cgroup/memory.max": cgFile("max\n"),
		}), memoryLimit{version: "v2"}, false},
		{"v2 no proc", v2(fstest.MapFS{
			"sys/fs/cgroup/memory.max": cgFile("2147483648"),
		}), memoryLimit{bytes: 2 * gib, version: "v2"}, false},
		{"v2 nested, parent smaller", v2(fstest.MapFS{
			"proc/self/cgroup":                           cgFile("0::/kubepods/pod1/ctr\n"),
			"sys/fs/cgroup/kubepods/pod1/ctr/memory.max": cgFile("max\n"),
			"sys/fs/cgroup/kubepods/pod1/memory.max":     cgFile("1073741824\n"),
			"sys/fs/cgroup/kubepods/memory.max":          cgFile("8589934592\n"),
		}), memoryLimit{bytes: gib, version: "v2"}, false},
		{"v2 nested, own smaller", v2(fstest.MapFS{
			"proc/self/cgroup":                           cgFile("0::/kubepods/pod1/ctr\n"),
			"sys/fs/cgroup/kubepods/pod1/ctr/memory.max": cgFile("536870912\n"),
			"sys/fs/cgroup/kubepods/pod1/memory.max":     cgFile("1073741824\n"),
		}), memoryLimit{bytes: gib / 2, version: "v2"}, false},
		{"v2 outside namespace", v2(fstest.MapFS{
			"proc/self/cgroup":         cgFile("0::/../../other\n"),
			"sys/fs/cgroup/memory.max": cgFile("1073741824\n"),
		}), memoryLimit{bytes: gib, version: "v2"}, false},
		{"v2 garbage", v2(fstest.MapFS{
			"proc/self/cgroup":         cgFile("0::/\n"),
			"sys/fs/cgroup/memory.max": cgFile("lots\n"),
		}), memoryLimit{}, true},
		{"v1", fstest.MapFS{
			"proc/self/cgroup": cgFile("12:cpu,cpuacct:/docker/abc\n4:memory:/docker/abc\n0::/\n"),
			"sys/fs/cgroup/memory/docker/abc/memory.limit_in_bytes": cgFile("1073741824\n"),
		}, memoryLimit{bytes: gib, version: "v1"}, false},
		{"v1 unlimited", fstest.MapFS{
			"proc/self/cgroup":                           cgFile("4:memory:/\n"),
			"sys/fs/cgroup/memory/memory.limit_in_bytes": cgFile("9223372036854771712\n"),
		}, memoryLimit{version: "v1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := cgroupMemoryLimit(tc.fsys)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if d := cmp.Diff(tc.want, got, cmp.AllowUnexported(memoryLimit{})); d != "" {
				t.Errorf("limit (-want +got):\n%s", d)
			}
		})
	}
}

func TestApplyGoMemoryLimit(t *testing.T) {
	t.Parallel()
	v2 := func(limit string) fstest.MapFS {
		return fstest.MapFS{
			"sys/fs/cgroup/cgroup.controllers": cgFile("memory\n"),
			"proc/self/cgroup":                 cgFile("0::/\n"),
			"sys/fs/cgroup/memory.max":         cgFile(limit + "\n"),
		}
	}
	tests := []struct {
		name       string
		fsys       fstest.MapFS
		fraction   float64
		budget     config.ByteSize
		gomemlimit string
		want       int64 // the limit set; -1: SetMemoryLimit only queried
		wantMsgs   []string
	}{
		{"30 % of 1 GiB", v2("1073741824"), 0.3, 0, "", 322122547, []string{"go memory limit set"}},
		{"fraction 1", v2("1000"), 1, 0, "", 1000, []string{"go memory limit set"}},
		{"operator wins", v2("1073741824"), 0.3, 0, "600MiB", -1, []string{"go memory limit from GOMEMLIMIT"}},
		{"no limit", v2("max"), 0.3, 0, "", -1, []string{"go memory limit unchanged: no cgroup memory limit"}},
		{"unreadable", v2("x"), 0.3, 0, "", -1, []string{"go memory limit unchanged: cgroup memory limit unreadable"}},
		{"budget too large", v2("1073741824"), 0.3, 900 << 20, "", 322122547,
			[]string{"go memory limit set", "go memory limit and memory budget exceed the cgroup memory limit"}},
		{"budget fits", v2("1073741824"), 0.3, 512 << 20, "", 322122547, []string{"go memory limit set"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			cfg := config.Defaults()
			cfg.Processing.GoMemoryFraction = tc.fraction
			cfg.Processing.MemoryBudget = tc.budget
			set := int64(-1)
			env := memLimitEnv{fsys: tc.fsys, setLimit: func(n int64) int64 {
				if n >= 0 {
					set = n
				}
				return math.MaxInt64
			}}
			got := applyGoMemoryLimit(t.Context(), logger, &cfg, tc.gomemlimit, env)
			if set != tc.want {
				t.Errorf("SetMemoryLimit(%d), want %d", set, tc.want)
			}
			if tc.want >= 0 && got != tc.want {
				t.Errorf("returned %d, want %d", got, tc.want)
			}
			if d := cmp.Diff(tc.wantMsgs, logMessages(t, &logs)); d != "" {
				t.Errorf("log messages (-want +got):\n%s", d)
			}
		})
	}
}

// logMessages returns the msg of every JSON log line in r.
func logMessages(t *testing.T, r io.Reader) []string {
	t.Helper()
	var msgs []string
	dec := json.NewDecoder(r)
	for {
		var line struct{ Msg string }
		if err := dec.Decode(&line); err == io.EOF {
			return msgs
		} else if err != nil {
			t.Fatalf("decode log: %v", err)
		}
		msgs = append(msgs, strings.TrimSpace(line.Msg))
	}
}
