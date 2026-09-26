package config

import (
	"math"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestParseByteSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    ByteSize
		wantErr bool
	}{
		{"0", 0, false},
		{"1048576", 1 << 20, false},
		{"64B", 64, false},
		{"32KiB", 32 << 10, false},
		{"10MiB", 10 << 20, false},
		{" 512 mib ", 512 << 20, false},
		{"10GiB", 10 << 30, false},
		{"2TiB", 2 << 40, false},
		{"8388607TiB", 8388607 << 40, false},
		{"8388608TiB", 0, true},
		{"9223372036854775808", 0, true},
		{"10MB", 0, true},
		{"1.5GiB", 0, true},
		{"-1", 0, true},
		{"+1", 0, true},
		{"MiB", 0, true},
		{"", 0, true},
		{"1 0", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseByteSize(tc.in)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("ParseByteSize(%q) = %d, %v; want %d, err %v", tc.in, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestByteSizeString(t *testing.T) {
	t.Parallel()
	for in, want := range map[ByteSize]string{0: "0", 5: "5", 1024: "1KiB", 1536: "1536", 50 << 20: "50MiB", 10 << 30: "10GiB", 3 << 40: "3TiB", math.MaxInt64: "9223372036854775807"} {
		if got := in.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int64(in), got, want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{{"0", 0}, {"5s", 5 * time.Second}, {" 1m30s ", 90 * time.Second}, {"31536000s", 8760 * time.Hour}} {
		if got, err := ParseDuration(tc.in); err != nil || got != tc.want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, in := range []string{"", "5", "-1s", "5x", "1e3s"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) succeeded", in)
		}
	}
}

func TestParseCIDR(t *testing.T) {
	t.Parallel()
	for _, tc := range [][2]string{
		{"10.0.0.0/8", "10.0.0.0/8"},
		{"192.168.1.7", "192.168.1.7/32"},
		{"fd00::/8", "fd00::/8"},
		{"2001:db8::1", "2001:db8::1/128"},
		{" 100.64.0.0/10 ", "100.64.0.0/10"},
		{"0.0.0.0/0", "0.0.0.0/0"},
	} {
		got, err := ParseCIDR(tc[0])
		if err != nil || got != netip.MustParsePrefix(tc[1]) {
			t.Errorf("ParseCIDR(%q) = %v, %v; want %s", tc[0], got, err, tc[1])
		}
	}
	for _, in := range []string{"", "10.0.0.1/8", "10.0.0.0/33", "fe80::1%eth0", "::ffff:10.0.0.1", "::ffff:10.0.0.0/104", "example.com", "10.0.0.0/8/8"} {
		if _, err := ParseCIDR(in); err == nil {
			t.Errorf("ParseCIDR(%q) succeeded", in)
		}
	}
}

func TestIntList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, str string
		want    []int
	}{
		{"16,32", "16,32", []int{16, 32}},
		{"5-100/5", "5-100/5", []int{5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60, 65, 70, 75, 80, 85, 90, 95, 100}},
		{"1-3, 10", "1-3,10", []int{1, 2, 3, 10}},
		{"7-7", "7", []int{7}},
		{"640-1100/220", "640-1080/220", []int{640, 860, 1080}},
		{"1,2", "1,2", []int{1, 2}},
		{"16,32,48,64,96,128", "16-64/16,96,128", []int{16, 32, 48, 64, 96, 128}},
	}
	for _, tc := range tests {
		got, err := ParseIntList(tc.in)
		if err != nil {
			t.Fatalf("ParseIntList(%q): %v", tc.in, err)
		}
		if d := diff(tc.want, []int(got)); d != "" {
			t.Errorf("ParseIntList(%q) (-want +got):\n%s", tc.in, d)
		}
		if s := got.String(); s != tc.str {
			t.Errorf("String() = %q, want %q", s, tc.str)
		}
	}
	for _, in := range []string{"", "a", "1,,2", "5-1", "1/2", "1-10/0", "-1", "0-100000000"} {
		if _, err := ParseIntList(in); err == nil {
			t.Errorf("ParseIntList(%q) succeeded", in)
		}
	}
}

func TestYAMLTypedValues(t *testing.T) {
	t.Parallel()
	const doc = `
security:
  allowed_widths: [100, "200-400/100"]
  allowed_qualities: "50-60/5"
signing:
  required: false
fetch:
  allowed_networks: [10.1.0.0/16, "fd00::1"]
formats:
  preferred: [JPG, webp]
`
	cfg := mustParse(t, files{"/c.yaml": doc}, "XI_CONFIG_FILE=/c.yaml")
	if d := diff([]int{100, 200, 300, 400}, []int(cfg.Security.AllowedWidths)); d != "" {
		t.Error(d)
	}
	if d := diff([]int{50, 55, 60}, []int(cfg.Security.AllowedQualities)); d != "" {
		t.Error(d)
	}
	if cfg.Signing.Required != False {
		t.Errorf("required = %v", cfg.Signing.Required)
	}
	if got := cfg.Fetch.AllowedNetworks[1].Prefix(); got != netip.MustParsePrefix("fd00::1/128") {
		t.Errorf("network = %v", got)
	}
	if d := diff([]Format{FormatJPEG, FormatWebP}, cfg.Formats.Preferred); d != "" {
		t.Error(d)
	}
	for _, bad := range []string{"server: {request_timeout: 10}", "limits: {max_src_bytes: 10MB}", "security: {unsigned_policy: sometimes}", "signing: {keys: [nothex]}"} {
		if _, _, err := load(opts(files{"/c.yaml": bad}, "XI_CONFIG_FILE=/c.yaml")); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestHexKeyErrorDoesNotLeak(t *testing.T) {
	t.Parallel()
	var k HexKey
	err := k.UnmarshalText([]byte("abcdefXYZsecret"))
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "X") {
		t.Fatalf("err = %v", err)
	}
}

func TestVideoSecond(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]VideoSecond{"auto": VideoSecondAuto, "AUTO": VideoSecondAuto, "": VideoSecondAuto, "1": "1", "2.50": "2.5", "-1.5": "-1.5", "+3": "3"} {
		var got VideoSecond
		if err := got.UnmarshalText([]byte(in)); err != nil || got != want {
			t.Errorf("UnmarshalText(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"soon", "1s", "inf", "NaN", "1e999"} {
		var got VideoSecond
		if err := got.UnmarshalText([]byte(in)); err == nil {
			t.Errorf("UnmarshalText(%q) = %q, want an error", in, got)
		}
	}
	if s, ok := VideoSecond("-2.5").Seconds(); !ok || s != -2.5 {
		t.Errorf("Seconds() = %v, %v", s, ok)
	}
	if _, ok := VideoSecondAuto.Seconds(); ok {
		t.Error("auto has seconds")
	}
	if b, _ := VideoSecond("").MarshalText(); string(b) != "auto" {
		t.Errorf("MarshalText(\"\") = %q", b)
	}
}

func TestTristate(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Tristate{"auto": Auto, "AUTO": Auto, "true": True, "1": True, "false": False, "F": False} {
		var got Tristate
		if err := got.UnmarshalText([]byte(in)); err != nil || got != want {
			t.Errorf("%q → %v, %v; want %v", in, got, err, want)
		}
	}
	var t3 Tristate
	if err := t3.UnmarshalText([]byte("maybe")); err == nil {
		t.Error("maybe accepted")
	}
}
