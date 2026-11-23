// Package netguard holds the address policy for outbound connections whose
// destination was chosen by someone other than the operator.
package netguard

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
)

func prefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

var (
	// alwaysRefused are never a participant endpoint: cloud metadata services
	// that are not link-local. Link-local, multicast and unspecified addresses
	// are refused by the checks in checkOne.
	alwaysRefused = prefixes(
		"fd00:ec2::/32",      // AWS instance metadata and time sync over IPv6
		"fd20:ce::/64",       // GCP metadata over IPv6
		"100.100.100.200/32", // Alibaba Cloud metadata
	)
	// privateExtra are non-public ranges that netip's IsPrivate and IsLoopback
	// do not cover.
	privateExtra = prefixes(
		"100.64.0.0/10", // carrier-grade NAT (RFC 6598)
		"0.0.0.0/8",     // "this network"
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmarking
		"240.0.0.0/4",   // reserved, includes broadcast
		"fec0::/10",     // deprecated site-local
	)
	nat64WellKnown = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local     = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour      = netip.MustParsePrefix("2002::/16")
	teredo         = netip.MustParsePrefix("2001::/32")
	v4Compatible   = netip.MustParsePrefix("::/96")
	siit           = netip.MustParsePrefix("::ffff:0:0:0/96")
)

// embeddedIPv4 returns the IPv4 addresses carried inside an IPv6 address by a
// translation or tunnelling scheme. A network that translates such an address
// delivers the connection to the IPv4 address, so it is checked as well.
func embeddedIPv4(a netip.Addr) []netip.Addr {
	if !a.Is6() || a.Is4In6() {
		return nil
	}
	b := a.As16()
	last := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	switch {
	case nat64WellKnown.Contains(a), nat64Local.Contains(a), siit.Contains(a):
		return []netip.Addr{last}
	case v4Compatible.Contains(a) && !a.IsUnspecified() && !a.IsLoopback():
		return []netip.Addr{last}
	case sixToFour.Contains(a):
		return []netip.Addr{netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})}
	case teredo.Contains(a):
		return []netip.Addr{
			netip.AddrFrom4([4]byte{b[4], b[5], b[6], b[7]}),                                 // server
			netip.AddrFrom4([4]byte{b[12] ^ 0xff, b[13] ^ 0xff, b[14] ^ 0xff, b[15] ^ 0xff}), // client
		}
	}
	return nil
}

// Check reports why the adapter must not connect to addr when the address was
// chosen by someone else (a counterparty's message, or a document a message
// points to), or nil if it may.
//
// Link-local, multicast, unspecified and cloud metadata addresses are always
// refused. Loopback, private and reserved ranges are refused unless
// allowPrivate is true. An IPv4 address carried inside an IPv6 address by a
// translation or tunnelling scheme is checked as well.
func Check(addr netip.Addr, allowPrivate bool) error {
	addr = addr.Unmap().WithZone("")
	if err := checkOne(addr, allowPrivate); err != nil {
		return err
	}
	for _, inner := range embeddedIPv4(addr) {
		if err := checkOne(inner, allowPrivate); err != nil {
			return fmt.Errorf("address %s carries %w", addr, err)
		}
	}
	return nil
}

func checkOne(addr netip.Addr, allowPrivate bool) error {
	switch {
	case !addr.IsValid(), addr.IsUnspecified():
		return fmt.Errorf("address %s is not a usable destination", addr)
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast(), addr.IsInterfaceLocalMulticast(), addr.IsMulticast():
		// Includes 169.254.169.254 and fe80::/10, where cloud metadata
		// services listen. Never a participant endpoint.
		return fmt.Errorf("address %s is link-local or multicast", addr)
	}
	for _, p := range alwaysRefused {
		if p.Contains(addr) {
			return fmt.Errorf("address %s is a cloud metadata address", addr)
		}
	}
	if allowPrivate {
		return nil
	}
	if addr.IsLoopback() || addr.IsPrivate() {
		return fmt.Errorf("address %s is on a private or loopback network", addr)
	}
	for _, p := range privateExtra {
		if p.Contains(addr) {
			return fmt.Errorf("address %s is on a private or reserved network", addr)
		}
	}
	return nil
}

// Control returns a net.Dialer.Control hook that applies Check to the address
// a connection is about to be made to, after name resolution.
func Control(allowPrivate bool) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return fmt.Errorf("destination guard: unexpected dial address %q: %w", address, err)
		}
		if err := Check(ap.Addr(), allowPrivate); err != nil {
			return fmt.Errorf("destination guard: %w", err)
		}
		return nil
	}
}

// Dialer returns a dialler with the usual timeouts that applies Control.
func Dialer(allowPrivate bool) *net.Dialer {
	return &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: Control(allowPrivate)}
}
