package ssrf

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Sentinel causes. Every one is wrapped in an *errs.Error that carries the
// kind, so callers can match either errors.Is(err, ssrf.ErrBlockedAddress)
// or errors.Is(err, errs.Match(errs.KindForbidden)).
var (
	ErrBlockedAddress   = errors.New("ssrf: destination address blocked")
	ErrBlockedPort      = errors.New("ssrf: destination port blocked")
	ErrBlockedScheme    = errors.New("ssrf: url scheme not allowed")
	ErrDowngrade        = errors.New("ssrf: https to http redirect refused")
	ErrTooManyRedirects = errors.New("ssrf: too many redirects")
	ErrBodyTooLarge     = errors.New("ssrf: response body too large")
)

// DefaultPorts is the port allowlist used when Policy.Ports is empty
// (XI_FETCH_ALLOWED_PORTS default).
func DefaultPorts() []uint16 { return []uint16{80, 443} }

// Policy decides which destination addresses and ports may be dialed.
// The zero value blocks every non-public class and allows ports 80 and 443.
type Policy struct {
	// Allow lists address classes that are permitted, e.g.
	// ClassPrivateNetworks for XI_FETCH_ALLOW_PRIVATE_NETWORKS=true.
	// Multicast, unspecified and broadcast are never permitted.
	Allow Class
	// AllowCIDRs permits addresses in these prefixes whatever their class
	// (for intentional internal fetches such as an in-cluster object store).
	// DenyCIDRs and the never-permitted classes still win.
	AllowCIDRs []netip.Prefix
	// DenyCIDRs blocks addresses in these prefixes even when public.
	DenyCIDRs []netip.Prefix
	// Ports is the destination port allowlist; empty means DefaultPorts.
	Ports []uint16
}

// CheckAddr returns nil when a may be dialed and a KindForbidden error
// wrapping ErrBlockedAddress otherwise.
func (p *Policy) CheckAddr(a netip.Addr) error {
	if !a.IsValid() {
		return blocked(a, ClassUnspecified, "invalid address")
	}
	a = a.WithZone("").Unmap()
	c := Classify(a)
	switch {
	case containsAny(p.DenyCIDRs, a):
		return blocked(a, c, "deny cidr")
	case c&classNever != 0:
		return blocked(a, c, "never dialable")
	case containsAny(p.AllowCIDRs, a):
		return nil
	case c&^p.Allow != 0:
		return blocked(a, c, "class not allowed")
	default:
		return nil
	}
}

// CheckPort returns nil when port is on the allowlist and a KindForbidden
// error wrapping ErrBlockedPort otherwise.
func (p *Policy) CheckPort(port uint16) error {
	ports := p.Ports
	if len(ports) == 0 {
		ports = DefaultPorts()
	}
	if slices.Contains(ports, port) {
		return nil
	}
	return errs.Wrapf(errs.KindForbidden, ErrBlockedPort, "port %d", port)
}

// CheckAddrPort applies CheckPort and CheckAddr.
func (p *Policy) CheckAddrPort(ap netip.AddrPort) error {
	if err := p.CheckPort(ap.Port()); err != nil {
		return err
	}
	return p.CheckAddr(ap.Addr())
}

func blocked(a netip.Addr, c Class, why string) error {
	return errs.Wrapf(errs.KindForbidden, ErrBlockedAddress, "%s (%s): %s", a, c, why)
}

// containsAny reports whether a, or an IPv4 address embedded in it, is in
// one of prefixes.
func containsAny(prefixes []netip.Prefix, a netip.Addr) bool {
	if len(prefixes) == 0 {
		return false
	}
	match := func(x netip.Addr) bool {
		return slices.ContainsFunc(prefixes, func(p netip.Prefix) bool { return p.Contains(x) })
	}
	if match(a) {
		return true
	}
	if a.Is6() {
		return slices.ContainsFunc(embeddedV4(a), match)
	}
	return false
}

// ParsePrefixes parses a comma- or whitespace-separated list of CIDR
// prefixes or bare IP addresses (a bare address is a single-host prefix).
// Prefixes are masked to their canonical form; zones are rejected. The
// empty string yields nil.
func ParsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	var errList []error
	for f := range strings.FieldsFuncSeq(s, isListSep) {
		p, err := parsePrefix(f)
		if err != nil {
			errList = append(errList, err)
			continue
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if err := errors.Join(errList...); err != nil {
		return nil, err
	}
	return out, nil
}

func parsePrefix(f string) (netip.Prefix, error) {
	if strings.Contains(f, "/") {
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("cidr %q: %w", f, err)
		}
		if p.Addr().Is4In6() {
			// ::ffff:10.0.0.0/104 → 10.0.0.0/8, matching how addresses are
			// unmapped before the check.
			if p.Bits() < 96 {
				return netip.Prefix{}, fmt.Errorf("cidr %q: ipv4-mapped prefix shorter than /96", f)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(f)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("address %q: %w", f, err)
	}
	if a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("address %q: zones are not allowed", f)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// ParsePorts parses a comma- or whitespace-separated port list
// (XI_FETCH_ALLOWED_PORTS). Ports must be 1..65535. The empty string yields
// nil, which Policy treats as DefaultPorts.
func ParsePorts(s string) ([]uint16, error) {
	var out []uint16
	var errList []error
	for f := range strings.FieldsFuncSeq(s, isListSep) {
		n, err := strconv.ParseUint(f, 10, 16)
		if err != nil || n == 0 {
			errList = append(errList, fmt.Errorf("port %q: must be 1..65535", f))
			continue
		}
		if p := uint16(n); !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	if err := errors.Join(errList...); err != nil {
		return nil, err
	}
	return out, nil
}

// ParseClasses parses a comma- or whitespace-separated list of class names
// as printed by Class.String (e.g. "private,cgnat,ula").
func ParseClasses(s string) (Class, error) {
	var c Class
	var errList []error
	for f := range strings.FieldsFuncSeq(s, isListSep) {
		names := classNames()
		i := slices.IndexFunc(names[:], func(cn className) bool { return strings.EqualFold(cn.name, f) })
		if i < 0 {
			errList = append(errList, fmt.Errorf("address class %q: unknown", f))
			continue
		}
		c |= names[i].c
	}
	if err := errors.Join(errList...); err != nil {
		return 0, err
	}
	return c, nil
}

func isListSep(r rune) bool {
	return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
