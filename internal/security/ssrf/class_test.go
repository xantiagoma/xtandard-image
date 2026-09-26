package ssrf

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// classCase is one row of the ARCHITECTURE §10 address-class table.
type classCase struct {
	name string
	addr string
	want Class
}

func classCases() []classCase {
	return []classCase{
		// public
		{"public v4", "8.8.8.8", 0},
		{"public v6", "2606:4700:4700::1111", 0},
		{"documentation range is not special", "198.51.100.7", 0},
		{"just outside rfc1918 172", "172.32.0.1", 0},
		{"just outside cgnat", "100.128.0.1", 0},
		// loopback
		{"loopback v4", "127.0.0.1", ClassLoopback},
		{"loopback v4 top", "127.255.255.254", ClassLoopback},
		{"loopback v6", "::1", ClassLoopback},
		// RFC 1918
		{"private 10/8", "10.0.0.1", ClassPrivate},
		{"private 172.16/12 low", "172.16.0.1", ClassPrivate},
		{"private 172.16/12 high", "172.31.255.255", ClassPrivate},
		{"private 192.168/16", "192.168.1.1", ClassPrivate},
		// link-local incl. metadata
		{"metadata 169.254.169.254", "169.254.169.254", ClassLinkLocal},
		{"link-local v4", "169.254.0.1", ClassLinkLocal},
		{"link-local v6", "fe80::1", ClassLinkLocal},
		{"link-local v6 with zone", "fe80::1%en0", ClassLinkLocal},
		// ULA and site-local
		{"ula fc00::/7", "fc00::1", ClassULA},
		{"ula aws imds v6", "fd00:ec2::254", ClassULA},
		{"site-local fec0::/10", "fec0::1", ClassSiteLocal},
		// CGNAT
		{"cgnat low", "100.64.0.1", ClassCGNAT},
		{"cgnat high", "100.127.255.254", ClassCGNAT},
		{"cgnat alibaba metadata", "100.100.100.200", ClassCGNAT},
		// reserved
		{"this network 0/8", "0.1.2.3", ClassReserved},
		{"class e 240/4", "240.0.0.1", ClassReserved},
		{"benchmarking 198.18/15", "198.18.0.1", ClassReserved},
		{"ietf 192.0.0/24", "192.0.0.170", ClassReserved},
		{"discard 100::/64", "100::1", ClassReserved},
		// multicast, unspecified, broadcast
		{"multicast v4", "224.0.0.1", ClassMulticast},
		{"multicast ssdp", "239.255.255.250", ClassMulticast},
		{"multicast v6", "ff02::1", ClassMulticast},
		{"unspecified v4", "0.0.0.0", ClassUnspecified},
		{"unspecified v6", "::", ClassUnspecified},
		{"broadcast", "255.255.255.255", ClassBroadcast},
		// IPv4-mapped IPv6
		{"mapped loopback", "::ffff:127.0.0.1", ClassLoopback},
		{"mapped private", "::ffff:10.0.0.1", ClassPrivate},
		{"mapped metadata", "::ffff:169.254.169.254", ClassLinkLocal},
		{"mapped public", "::ffff:8.8.8.8", 0},
		// IPv4-compatible
		{"compat loopback", "::127.0.0.1", ClassLoopback},
		// NAT64
		{"nat64 loopback", "64:ff9b::7f00:1", ClassLoopback},
		{"nat64 metadata", "64:ff9b::a9fe:a9fe", ClassLinkLocal},
		{"nat64 cgnat", "64:ff9b::6440:1", ClassCGNAT},
		{"nat64 public", "64:ff9b::808:808", 0},
		{"nat64 local-use loopback", "64:ff9b:1:7f00:0:100::", ClassLoopback},
		// other embeddings
		{"6to4 loopback", "2002:7f00:1::", ClassLoopback},
		{"6to4 private", "2002:a00:1::", ClassPrivate},
		{"teredo loopback client", "2001:0:4136:e378:8000:63bf:80ff:fffe", ClassLoopback},
		{"isatap link-local private", "fe80::5efe:a00:1", ClassLinkLocal | ClassPrivate},
		{"isatap global private", "2001:db8::5efe:c0a8:101", ClassPrivate},
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	for _, tc := range classCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Classify(netip.MustParseAddr(tc.addr)); got != tc.want {
				t.Errorf("Classify(%s) = %s, want %s", tc.addr, got, tc.want)
			}
		})
	}
}

func TestClassifyInvalid(t *testing.T) {
	t.Parallel()
	if got := Classify(netip.Addr{}); got != ClassUnspecified {
		t.Errorf("Classify(zero) = %s, want unspecified", got)
	}
}

func TestClassString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		c    Class
		want string
	}{
		{0, "public"},
		{ClassLoopback, "loopback"},
		{ClassLinkLocal | ClassPrivate, "private|link_local"},
		{ClassPrivateNetworks, "private|ula|site_local|cgnat"},
		{classEnd, "unknown"},
	}
	for _, tc := range tests {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("Class(%d).String() = %q, want %q", tc.c, got, tc.want)
		}
	}
}

func isBlocked(err error) bool {
	return errors.Is(err, ErrBlockedAddress) && errs.KindOf(err) == errs.KindForbidden
}

// TestPolicyClasses runs every address class through three policies: the
// default (blocked unless public), one that allows the class, and one that
// allowlists the exact address. Multicast, unspecified and broadcast stay
// blocked under all three.
func TestPolicyClasses(t *testing.T) {
	t.Parallel()
	for _, tc := range classCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := netip.MustParseAddr(tc.addr)
			never := tc.want&classNever != 0

			var def Policy
			if err := def.CheckAddr(a); (err == nil) != (tc.want == 0) {
				t.Errorf("default policy: CheckAddr(%s) = %v, want blocked=%v", a, err, tc.want != 0)
			} else if err != nil && !isBlocked(err) {
				t.Errorf("default policy: error %v is not a forbidden ErrBlockedAddress", err)
			}

			byClass := Policy{Allow: tc.want}
			if err := byClass.CheckAddr(a); (err == nil) == never {
				t.Errorf("Allow=%s: CheckAddr(%s) = %v, want blocked=%v", tc.want, a, err, never)
			}

			host := a.WithZone("").Unmap()
			byCIDR := Policy{AllowCIDRs: []netip.Prefix{netip.PrefixFrom(host, host.BitLen())}}
			if err := byCIDR.CheckAddr(a); (err == nil) == never {
				t.Errorf("AllowCIDRs=%s: CheckAddr(%s) = %v, want blocked=%v", host, a, err, never)
			}
		})
	}
}

func TestPolicyRules(t *testing.T) {
	t.Parallel()
	pfx := func(s ...string) []netip.Prefix {
		out := make([]netip.Prefix, 0, len(s))
		for _, x := range s {
			out = append(out, netip.MustParsePrefix(x))
		}
		return out
	}
	tests := []struct {
		name    string
		policy  Policy
		addr    string
		blocked bool
	}{
		{"deny cidr blocks public", Policy{DenyCIDRs: pfx("8.8.8.0/24")}, "8.8.8.8", true},
		{"deny beats allow cidr", Policy{AllowCIDRs: pfx("10.0.0.0/8"), DenyCIDRs: pfx("10.1.0.0/16")}, "10.1.2.3", true},
		{"deny matches embedded v4", Policy{DenyCIDRs: pfx("8.8.8.0/24")}, "64:ff9b::808:808", true},
		{"allow cidr permits mapped", Policy{AllowCIDRs: pfx("10.0.0.0/8")}, "::ffff:10.1.2.3", false},
		{"allow cidr permits nat64 embedding", Policy{AllowCIDRs: pfx("10.0.0.0/8")}, "64:ff9b::a01:203", false},
		{"allow cidr is scoped", Policy{AllowCIDRs: pfx("10.0.0.0/8")}, "192.168.0.1", true},
		{"allow cidr cannot open multicast", Policy{AllowCIDRs: pfx("224.0.0.0/4")}, "224.0.0.1", true},
		{"allow cidr cannot open unspecified", Policy{AllowCIDRs: pfx("0.0.0.0/0")}, "0.0.0.0", true},
		{"private networks: rfc1918", Policy{Allow: ClassPrivateNetworks}, "10.0.0.1", false},
		{"private networks: cgnat", Policy{Allow: ClassPrivateNetworks}, "100.64.0.1", false},
		{"private networks: ula", Policy{Allow: ClassPrivateNetworks}, "fd12::1", false},
		{"private networks: not loopback", Policy{Allow: ClassPrivateNetworks}, "127.0.0.1", true},
		{"private networks: not metadata", Policy{Allow: ClassPrivateNetworks}, "169.254.169.254", true},
		{"allow link-local does not allow isatap private", Policy{Allow: ClassLinkLocal}, "fe80::5efe:a00:1", true},
		{"invalid address", Policy{}, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var a netip.Addr
			if tc.addr != "" {
				a = netip.MustParseAddr(tc.addr)
			}
			err := tc.policy.CheckAddr(a)
			if (err != nil) != tc.blocked {
				t.Fatalf("CheckAddr(%s) = %v, want blocked=%v", tc.addr, err, tc.blocked)
			}
			if err != nil && !isBlocked(err) {
				t.Errorf("error %v is not a forbidden ErrBlockedAddress", err)
			}
		})
	}
}

func TestPolicyPorts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		ports []uint16
		port  uint16
		ok    bool
	}{
		{"default 80", nil, 80, true},
		{"default 443", nil, 443, true},
		{"default blocks 8080", nil, 8080, false},
		{"default blocks 22", nil, 22, false},
		{"custom allows 8080", []uint16{8080}, 8080, true},
		{"custom replaces defaults", []uint16{8080}, 443, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := Policy{Ports: tc.ports}
			err := p.CheckPort(tc.port)
			if (err == nil) != tc.ok {
				t.Fatalf("CheckPort(%d) = %v, want ok=%v", tc.port, err, tc.ok)
			}
			if err != nil && (!errors.Is(err, ErrBlockedPort) || errs.KindOf(err) != errs.KindForbidden) {
				t.Errorf("error %v is not a forbidden ErrBlockedPort", err)
			}
			ap := netip.AddrPortFrom(netip.MustParseAddr("8.8.8.8"), tc.port)
			if err := p.CheckAddrPort(ap); (err == nil) != tc.ok {
				t.Errorf("CheckAddrPort(%s) = %v, want ok=%v", ap, err, tc.ok)
			}
		})
	}
}
