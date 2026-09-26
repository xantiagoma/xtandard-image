// Package ssrf builds the outbound HTTP client used for untrusted source
// URLs (docs/ARCHITECTURE.md §10, ADR 0003 §2).
//
// Defence in depth, per hop:
//
//  1. The request URL must be http or https on an allowlisted port.
//  2. The dialer resolves the host itself and validates every resolved
//     address against the Policy before dialing any of them.
//  3. The dial goes to the validated IP literal, and net.Dialer's
//     ControlContext re-checks the address the socket is about to connect to,
//     so a resolver that changes its answer (DNS rebinding) cannot slip an
//     internal address past the check.
//  4. Redirects are capped, re-validated hop by hop, never downgrade from
//     HTTPS to HTTP unless allowed, and drop credentials on a host change.
//
// Errors are *errs.Error values: blocked destinations are KindForbidden,
// timeouts KindUpstreamTimeout, and other transport failures
// KindUpstreamError.
package ssrf

import (
	"net/netip"
	"strings"
)

// Class is a bit set of address classes. The zero value means a public
// unicast address.
type Class uint32

// Address classes (docs/ARCHITECTURE.md §10). An address can belong to
// several classes, e.g. ::ffff:127.0.0.1 is ClassLoopback and 6to4 forms
// carry the classes of the IPv4 address they embed.
const (
	ClassLoopback    Class = 1 << iota // 127.0.0.0/8, ::1
	ClassPrivate                       // RFC 1918: 10/8, 172.16/12, 192.168/16
	ClassLinkLocal                     // 169.254/16 (incl. 169.254.169.254 metadata), fe80::/10
	ClassULA                           // fc00::/7 (incl. fd00:ec2::254 metadata)
	ClassSiteLocal                     // fec0::/10 (deprecated IPv6 site-local)
	ClassCGNAT                         // RFC 6598 100.64.0.0/10
	ClassReserved                      // 0/8, 192.0.0/24, 198.18/15, 240/4, 100::/64
	ClassMulticast                     // 224/4, ff00::/8
	ClassUnspecified                   // 0.0.0.0, ::
	ClassBroadcast                     // 255.255.255.255
	classEnd
)

// ClassPrivateNetworks is the set enabled by XI_FETCH_ALLOW_PRIVATE_NETWORKS:
// routable internal ranges, but not loopback or link-local (metadata).
const ClassPrivateNetworks = ClassPrivate | ClassULA | ClassSiteLocal | ClassCGNAT

// classNever lists classes that are never a valid unicast TCP destination.
// They are blocked even when Policy.Allow or Policy.AllowCIDRs covers them.
const classNever = ClassMulticast | ClassUnspecified | ClassBroadcast

type className struct {
	c    Class
	name string
}

// classNames is a function, not a table var, so the package has no globals.
func classNames() [10]className {
	return [...]className{
		{ClassLoopback, "loopback"},
		{ClassPrivate, "private"},
		{ClassLinkLocal, "link_local"},
		{ClassULA, "ula"},
		{ClassSiteLocal, "site_local"},
		{ClassCGNAT, "cgnat"},
		{ClassReserved, "reserved"},
		{ClassMulticast, "multicast"},
		{ClassUnspecified, "unspecified"},
		{ClassBroadcast, "broadcast"},
	}
}

// String returns the snake_case class names joined by "|", or "public".
func (c Class) String() string {
	if c == 0 {
		return "public"
	}
	var b strings.Builder
	for _, cn := range classNames() {
		if c&cn.c == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('|')
		}
		b.WriteString(cn.name)
	}
	if rest := c &^ (classEnd - 1); rest != 0 {
		if b.Len() > 0 {
			b.WriteByte('|')
		}
		b.WriteString("unknown")
	}
	return b.String()
}

// Prefix tables. They are functions rather than package-level vars so the
// package holds no mutable globals.
func v4Ranges() [11]struct {
	p netip.Prefix
	c Class
} {
	return [...]struct {
		p netip.Prefix
		c Class
	}{
		{netip.MustParsePrefix("127.0.0.0/8"), ClassLoopback},
		{netip.MustParsePrefix("10.0.0.0/8"), ClassPrivate},
		{netip.MustParsePrefix("172.16.0.0/12"), ClassPrivate},
		{netip.MustParsePrefix("192.168.0.0/16"), ClassPrivate},
		{netip.MustParsePrefix("169.254.0.0/16"), ClassLinkLocal},
		{netip.MustParsePrefix("100.64.0.0/10"), ClassCGNAT},
		{netip.MustParsePrefix("0.0.0.0/8"), ClassReserved},
		{netip.MustParsePrefix("192.0.0.0/24"), ClassReserved},
		{netip.MustParsePrefix("198.18.0.0/15"), ClassReserved},
		{netip.MustParsePrefix("240.0.0.0/4"), ClassReserved},
		{netip.MustParsePrefix("224.0.0.0/4"), ClassMulticast},
	}
}

func v6Ranges() [6]struct {
	p netip.Prefix
	c Class
} {
	return [...]struct {
		p netip.Prefix
		c Class
	}{
		{netip.MustParsePrefix("::1/128"), ClassLoopback},
		{netip.MustParsePrefix("fe80::/10"), ClassLinkLocal},
		{netip.MustParsePrefix("fc00::/7"), ClassULA},
		{netip.MustParsePrefix("fec0::/10"), ClassSiteLocal},
		{netip.MustParsePrefix("100::/64"), ClassReserved},
		{netip.MustParsePrefix("ff00::/8"), ClassMulticast},
	}
}

// Classify returns the classes of a, including the classes of any IPv4
// address embedded in an IPv6 form (IPv4-mapped, IPv4-compatible, NAT64
// 64:ff9b::/96 and 64:ff9b:1::/48, 6to4, Teredo, ISATAP). A zero result
// means public unicast. Invalid addresses classify as ClassUnspecified.
func Classify(a netip.Addr) Class {
	if !a.IsValid() {
		return ClassUnspecified
	}
	a = a.WithZone("").Unmap()
	c := classifyLiteral(a)
	if a.Is6() {
		for _, v4 := range embeddedV4(a) {
			c |= classifyLiteral(v4)
		}
	}
	return c
}

func classifyLiteral(a netip.Addr) Class {
	if a.IsUnspecified() {
		return ClassUnspecified
	}
	var c Class
	if a.Is4() {
		if a == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
			return ClassBroadcast
		}
		for _, r := range v4Ranges() {
			if r.p.Contains(a) {
				c |= r.c
			}
		}
		return c
	}
	for _, r := range v6Ranges() {
		if r.p.Contains(a) {
			c |= r.c
		}
	}
	return c
}

// embeddedV4 decodes IPv4 addresses carried inside an IPv6 address (the
// same embedding forms imgproxy's privatenet unwraps). a must be IPv6 and
// not IPv4-mapped.
func embeddedV4(a netip.Addr) []netip.Addr {
	b := a.As16()
	tail := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	var out []netip.Addr

	// RFC 4291 §2.5.5.1 IPv4-compatible ::a.b.c.d. Skip ::/104 so :: and
	// ::1 keep their own meaning.
	if isZero(b[:12]) && b[12] != 0 {
		out = append(out, tail)
	}
	// RFC 6052 NAT64 well-known prefix 64:ff9b::/96.
	if b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b && isZero(b[4:12]) {
		out = append(out, tail)
	}
	// RFC 8215 NAT64 local-use 64:ff9b:1::/48, RFC 6052 §2.2 /48 layout:
	// IPv4 in bytes 6,7,9,10 (byte 8 is the reserved u octet).
	if b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b && b[4] == 0x00 && b[5] == 0x01 {
		out = append(out, netip.AddrFrom4([4]byte{b[6], b[7], b[9], b[10]}))
	}
	// RFC 3056 6to4 2002::/16: IPv4 in bytes 2..5.
	if b[0] == 0x20 && b[1] == 0x02 {
		out = append(out, netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
	}
	// RFC 4380 Teredo 2001::/32: client IPv4 in the last 32 bits, inverted.
	if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x00 && b[3] == 0x00 {
		out = append(out, netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}))
	}
	// RFC 5214 ISATAP interface ID 0000:5efe or 0200:5efe, any prefix.
	if (b[8] == 0x00 || b[8] == 0x02) && b[9] == 0x00 && b[10] == 0x5e && b[11] == 0xfe {
		out = append(out, tail)
	}
	return out
}

func isZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
