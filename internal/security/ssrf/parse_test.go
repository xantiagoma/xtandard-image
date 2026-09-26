package ssrf

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestParsePrefixes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"blank", " ,\t, ", nil, false},
		{"cidr v4", "10.0.0.0/8", []string{"10.0.0.0/8"}, false},
		{"bare v4 is host", "10.1.2.3", []string{"10.1.2.3/32"}, false},
		{"bare v6 is host", "fd00::1", []string{"fd00::1/128"}, false},
		{"masked", "10.1.2.3/8", []string{"10.0.0.0/8"}, false},
		{"list with spaces", "10.0.0.0/8, 192.168.0.0/16\n::1", []string{"10.0.0.0/8", "192.168.0.0/16", "::1/128"}, false},
		{"dedup", "10.0.0.0/8,10.0.0.0/8", []string{"10.0.0.0/8"}, false},
		{"mapped prefix unmapped", "::ffff:10.0.0.0/104", []string{"10.0.0.0/8"}, false},
		{"mapped host unmapped", "::ffff:10.0.0.1", []string{"10.0.0.1/32"}, false},
		{"mapped prefix too short", "::ffff:0:0/90", nil, true},
		{"zone rejected", "fe80::1%en0", nil, true},
		{"bad bits", "10.0.0.0/33", nil, true},
		{"garbage", "example.com", nil, true},
		{"one bad entry fails all", "10.0.0.0/8,nope", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParsePrefixes(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParsePrefixes(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			gotS := make([]string, 0, len(got))
			for _, p := range got {
				gotS = append(gotS, p.String())
			}
			if len(tc.want) == 0 && len(gotS) == 0 {
				return
			}
			if !slices.Equal(gotS, tc.want) {
				t.Errorf("ParsePrefixes(%q) = %v, want %v", tc.in, gotS, tc.want)
			}
		})
	}
}

func TestParsePorts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    []uint16
		wantErr bool
	}{
		{"", nil, false},
		{"80,443", []uint16{80, 443}, false},
		{" 8080 , 80 80", []uint16{8080, 80}, false},
		{"0", nil, true},
		{"65536", nil, true},
		{"-1", nil, true},
		{"http", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParsePorts(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParsePorts(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("ParsePorts(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseClasses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		want    Class
		wantErr bool
	}{
		{"", 0, false},
		{"private", ClassPrivate, false},
		{"private, CGNAT ula,site_local", ClassPrivateNetworks, false},
		{"loopback,link_local", ClassLoopback | ClassLinkLocal, false},
		{"public", 0, true},
		{"everything", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseClasses(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseClasses(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("ParseClasses(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
	// Every class name round-trips through String.
	for _, cn := range classNames() {
		if got, err := ParseClasses(cn.c.String()); err != nil || got != cn.c {
			t.Errorf("ParseClasses(%q) = %s, %v", cn.c.String(), got, err)
		}
	}
}

func FuzzParsePrefixes(f *testing.F) {
	for _, s := range []string{
		"", "10.0.0.0/8", "10.1.2.3", "fd00::/8, ::1", "::ffff:10.0.0.0/104",
		"fe80::1%en0", "0.0.0.0/0,::/0", "1.2.3.4/33", "a,b,,c", "::ffff:1.2.3.4/96",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ps, err := ParsePrefixes(s)
		if err != nil {
			if ps != nil {
				t.Fatalf("ParsePrefixes(%q) returned prefixes with an error", s)
			}
			return
		}
		parts := make([]string, 0, len(ps))
		for _, p := range ps {
			if !p.IsValid() || p != p.Masked() || p.Addr().Zone() != "" || p.Addr().Is4In6() {
				t.Fatalf("ParsePrefixes(%q) produced non-canonical %s", s, p)
			}
			parts = append(parts, p.String())
		}
		again, err := ParsePrefixes(strings.Join(parts, ","))
		if err != nil || !slices.Equal(again, ps) {
			t.Fatalf("round trip of %q: %v, %v; want %v", s, again, err, ps)
		}
	})
}

func FuzzParsePorts(f *testing.F) {
	for _, s := range []string{"", "80,443", "0", "65535", "65536", "+80", " 1\t2\n3"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ports, err := ParsePorts(s)
		if err != nil {
			return
		}
		parts := make([]string, 0, len(ports))
		for _, p := range ports {
			if p == 0 {
				t.Fatalf("ParsePorts(%q) returned port 0", s)
			}
			parts = append(parts, strconv.Itoa(int(p)))
		}
		again, err := ParsePorts(strings.Join(parts, ","))
		if err != nil || !slices.Equal(again, ports) {
			t.Fatalf("round trip of %q: %v, %v; want %v", s, again, err, ports)
		}
	})
}

// FuzzPolicy parses allow/deny CIDR lists and checks an arbitrary address,
// asserting the policy's precedence rules: deny and never-dialable classes
// always block, an allowlisted address is otherwise permitted, and any
// other address is permitted exactly when all its classes are allowed.
func FuzzPolicy(f *testing.F) {
	f.Add("10.0.0.0/8", "", []byte{10, 1, 2, 3}, uint32(0))
	f.Add("", "8.8.8.0/24", []byte{8, 8, 8, 8}, uint32(0))
	f.Add("0.0.0.0/0", "", []byte{0, 0, 0, 0}, uint32(0))
	f.Add("", "", []byte{0, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0, 127, 0, 0, 1}, uint32(ClassLoopback))
	f.Add("::/0", "", []byte{0xff, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, uint32(0))
	f.Fuzz(func(t *testing.T, allow, deny string, raw []byte, allowClasses uint32) {
		a, ok := netip.AddrFromSlice(raw)
		if !ok {
			return
		}
		allowP, err := ParsePrefixes(allow)
		if err != nil {
			return
		}
		denyP, err := ParsePrefixes(deny)
		if err != nil {
			return
		}
		p := Policy{Allow: Class(allowClasses), AllowCIDRs: allowP, DenyCIDRs: denyP}
		err = p.CheckAddr(a)
		if err != nil && !isBlocked(err) {
			t.Fatalf("CheckAddr(%s) error %v is not a forbidden ErrBlockedAddress", a, err)
		}
		c := Classify(a)
		switch {
		case containsAny(denyP, a.Unmap()) || c&classNever != 0:
			if err == nil {
				t.Fatalf("CheckAddr(%s) permitted a denied/never address (class %s)", a, c)
			}
		case containsAny(allowP, a.Unmap()), c&^Class(allowClasses) == 0:
			if err != nil {
				t.Fatalf("CheckAddr(%s) = %v, want permitted (class %s)", a, err, c)
			}
		default:
			if err == nil {
				t.Fatalf("CheckAddr(%s) permitted class %s not in %s", a, c, Class(allowClasses))
			}
		}
	})
}
