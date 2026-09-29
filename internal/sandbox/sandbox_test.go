package sandbox

import (
	"os"
	"testing"
)

// TestMain doubles as the sandbox helper: on Linux New re-executes the test
// binary with HelperArg, as the server re-executes itself.
func TestMain(m *testing.M) {
	if IsHelper(os.Args) {
		os.Exit(RunHelper(os.Args))
	}
	os.Exit(m.Run())
}

const (
	cpuinfoX86 = "processor\t: 0\nvendor_id\t: GenuineIntel\ncpu family\t: 6\nflags\t\t: fpu sse2\n"
	// Rosetta for Linux (OrbStack, 2026-09-29).
	cpuinfoRosetta = "processor\t: 0\nvendor_id\t: VirtualApple\ncpu family\t: 6\nmodel name\t: VirtualApple @ 2.50GHz\n"
	cpuinfoARM     = "processor\t: 0\nBogoMIPS\t: 48.00\nFeatures\t: fp asimd\nCPU implementer\t: 0x61\n"
)

func TestDetectEmulation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		goarch  string
		cpuinfo string
		rosetta bool
		want    Emulation
	}{
		{"amd64 native", "amd64", cpuinfoX86, false, EmulationNone},
		{"arm64 native", "arm64", cpuinfoARM, false, EmulationNone},
		{"arm64 native with a rosetta handler", "arm64", cpuinfoARM, true, EmulationNone},
		{"amd64 under rosetta", "amd64", cpuinfoRosetta, false, EmulationRosetta},
		{"amd64 rosetta handler", "amd64", cpuinfoARM, true, EmulationRosetta},
		{"amd64 under qemu", "amd64", cpuinfoARM, false, EmulationQEMU},
		{"arm64 under qemu", "arm64", cpuinfoX86, false, EmulationQEMU},
		{"amd64 unreadable cpuinfo", "amd64", "", false, EmulationNone},
		{"riscv64", "riscv64", cpuinfoX86, false, EmulationNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := detectEmulation(tc.goarch, tc.cpuinfo, tc.rosetta); got != tc.want {
				t.Errorf("detectEmulation = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAddressSpaceBase(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		as      AddressSpace
		emu     Emulation
		want    uint64
		wantOff string
	}{
		{"default", AddressSpace{}, EmulationNone, DefaultAddressSpace, ""},
		{"configured", AddressSpace{Base: 2 << 30}, EmulationNone, 2 << 30, ""},
		{"off", AddressSpace{Off: true}, EmulationNone, 0, "disabled"},
		{"off wins over keep", AddressSpace{Off: true, KeepUnderEmulation: true}, EmulationRosetta, 0, "disabled"},
		{"rosetta", AddressSpace{}, EmulationRosetta, 0, "emulation: rosetta"},
		{"qemu", AddressSpace{Base: 2 << 30}, EmulationQEMU, 0, "emulation: qemu"},
		{"kept under emulation", AddressSpace{KeepUnderEmulation: true}, EmulationRosetta, DefaultAddressSpace, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, off := tc.as.base(tc.emu)
			if got != tc.want || off != tc.wantOff {
				t.Errorf("base = %d, %q; want %d, %q", got, off, tc.want, tc.wantOff)
			}
		})
	}
}

func TestAddressSpaceLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		base uint64
		out  int64
		want uint64
	}{
		{"none stays none", 0, 1 << 30, 0},
		{"base only", DefaultAddressSpace, 0, DefaultAddressSpace},
		{"negative output", DefaultAddressSpace, -1, DefaultAddressSpace},
		{"output allowance", DefaultAddressSpace, 100 << 20, DefaultAddressSpace + 200<<20},
		{"capped", DefaultAddressSpace, 100 << 30, maxAddressSpace},
		{"base above the cap", 32 << 30, 1 << 30, 32 << 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := addressSpaceLimit(tc.base, tc.out); got != tc.want {
				t.Errorf("addressSpaceLimit(%d, %d) = %d, want %d", tc.base, tc.out, got, tc.want)
			}
		})
	}
}
