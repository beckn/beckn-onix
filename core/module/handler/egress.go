package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// codeDestinationNotAllowed is the error code returned when a destination
// taken from the message is refused by the destination guard.
const codeDestinationNotAllowed = "NET_DESTINATION_NOT_ALLOWED"

// defaultAllowPrivateDestinations is what an unset allowPrivateDestinations
// means. True in the 1.9 line so a patch upgrade does not cut off setups that
// reach counterparties on a private network.
const defaultAllowPrivateDestinations = true

// verdictTTL is how long an "allowed" pre-check result for a host is reused,
// so a busy route does not pay a name lookup per request. The dial-time check
// still sees every new connection.
const verdictTTL = 30 * time.Second

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

// destinationGuard decides whether the adapter may connect to a destination
// that came from a message (context.bapUri / context.bppUri), as opposed to
// one written in the routing config. A counterparty chooses those addresses,
// so they must not be able to point the adapter at its own network.
type destinationGuard struct {
	allowPrivate bool
	allowedHosts []string

	mu       sync.Mutex
	verdicts map[string]time.Time // host -> time until which it is known allowed
}

func newDestinationGuard(cfg *HttpClientConfig) (*destinationGuard, error) {
	g := &destinationGuard{allowPrivate: defaultAllowPrivateDestinations, verdicts: map[string]time.Time{}}
	if cfg.AllowPrivateDestinations != nil {
		g.allowPrivate = *cfg.AllowPrivateDestinations
	}
	for _, raw := range cfg.AllowedDestinationHosts {
		h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		name := strings.TrimPrefix(h, "*.")
		if name == "" || strings.ContainsAny(name, "/:[]@*? ") {
			return nil, fmt.Errorf("allowedDestinationHosts entry %q must be a host name or *.suffix, with no scheme, port or path", raw)
		}
		g.allowedHosts = append(g.allowedHosts, h)
	}
	return g, nil
}

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

// checkAddr reports why addr must not be dialled, or nil if it may be.
func (g *destinationGuard) checkAddr(addr netip.Addr) error {
	addr = addr.Unmap().WithZone("")
	if err := g.checkOne(addr); err != nil {
		return err
	}
	for _, inner := range embeddedIPv4(addr) {
		if err := g.checkOne(inner); err != nil {
			return fmt.Errorf("address %s carries %w", addr, err)
		}
	}
	return nil
}

func (g *destinationGuard) checkOne(addr netip.Addr) error {
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
	if g.allowPrivate {
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

// hostAllowed applies allowedDestinationHosts. An empty list allows any host.
// An entry is an exact host name, or "*.example.com" for its subdomains.
func (g *destinationGuard) hostAllowed(host string) bool {
	if len(g.allowedHosts) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, allowed := range g.allowedHosts {
		if suffix, ok := strings.CutPrefix(allowed, "*."); ok {
			if len(host) > len(suffix)+1 && !strings.HasPrefix(host, ".") && strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

// looksNumeric reports whether host is written like an IP address in a form
// netip does not accept (octal, hex, short or decimal forms such as
// 0177.0.0.1 or 2130706433). Readers disagree about what such a host means,
// so it is refused.
func looksNumeric(host string) bool {
	last := host[strings.LastIndexByte(host, '.')+1:]
	if last == "" || last[0] < '0' || last[0] > '9' {
		return false
	}
	for _, r := range host {
		if !(r >= '0' && r <= '9' || r == '.' || r == 'x' || r == 'X' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// checkURL validates a message-supplied destination before any connection is
// attempted, so a refused destination gets a NACK instead of a proxy error.
// It resolves the host and refuses if any address it resolves to is refused;
// control repeats the check on the address actually dialled.
//
// viaProxy says the request will be sent through an egress proxy. The proxy
// then resolves the name, possibly with names this host cannot resolve, so
// only literal addresses and the host allowlist are checked here and the
// proxy's own access rules must keep internal ranges out.
func (g *destinationGuard) checkURL(ctx context.Context, u *url.URL, resolver *net.Resolver, viaProxy bool) error {
	if u == nil {
		return fmt.Errorf("no destination")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q is not http or https", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("destination must not carry credentials")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("destination has no host")
	}
	if !g.hostAllowed(host) {
		return fmt.Errorf("host %q is not in allowedDestinationHosts", host)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return g.checkAddr(addr)
	}
	if looksNumeric(host) {
		return fmt.Errorf("host %q is a non-standard numeric address", host)
	}
	if viaProxy {
		return nil
	}
	key := strings.ToLower(host)
	g.mu.Lock()
	until, known := g.verdicts[key]
	g.mu.Unlock()
	if known && time.Now().Before(until) {
		return nil
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("could not resolve host %q: %w", host, err)
	}
	for _, addr := range addrs {
		if err := g.checkAddr(addr); err != nil {
			return fmt.Errorf("host %q: %w", host, err)
		}
	}
	g.mu.Lock()
	if len(g.verdicts) >= 4096 {
		g.verdicts = map[string]time.Time{}
	}
	g.verdicts[key] = time.Now().Add(verdictTTL)
	g.mu.Unlock()
	return nil
}

// control is a net.Dialer.Control hook. It sees the address a connection is
// about to be made to, after name resolution, so a host that resolves to a
// permitted address during checkURL and to a refused one at dial time is
// still refused.
func (g *destinationGuard) control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("destination guard: unexpected dial address %q: %w", address, err)
	}
	if err := g.checkAddr(ap.Addr()); err != nil {
		return fmt.Errorf("destination guard: %w", err)
	}
	return nil
}

// dialContext dials with the address check, except when the dial is to an
// egress proxy named in the environment: that connection is to the operator's
// own proxy, which may well be on a private address. Every other dial is
// checked, including direct ones made while proxy variables are set (NO_PROXY
// hosts, or http destinations when only HTTPS_PROXY is set).
func (g *destinationGuard) dialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	guarded := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: g.control}
	plain := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	proxies := egressProxyAddrs()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if proxies[strings.ToLower(addr)] {
			return plain.DialContext(ctx, network, addr)
		}
		return guarded.DialContext(ctx, network, addr)
	}
}

// destinationNotAllowedErr is the NACK for a refused message-supplied
// destination. The reason is logged by the caller; the response does not say
// which address was refused or whether the name resolved.
func destinationNotAllowedErr() *model.CodedErr {
	return model.NewCodedErr(http.StatusBadRequest, codeDestinationNotAllowed,
		fmt.Errorf("the destination URI in the message context is not allowed"))
}

// egressProxyAddrs returns the host:port of each egress proxy named in the
// environment, as the transport will dial it.
func egressProxyAddrs() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			continue
		}
		if !strings.Contains(v, "://") {
			v = "http://" + v
		}
		u, err := url.Parse(v)
		if err != nil || u.Hostname() == "" {
			continue
		}
		port := u.Port()
		if port == "" {
			switch u.Scheme {
			case "https":
				port = "443"
			case "socks5", "socks5h":
				port = "1080"
			default:
				port = "80"
			}
		}
		out[strings.ToLower(net.JoinHostPort(u.Hostname(), port))] = true
	}
	return out
}

// goesViaProxy reports whether a request to u would be sent through an egress
// proxy named in the environment.
func goesViaProxy(u *url.URL) bool {
	p, err := http.ProxyFromEnvironment(&http.Request{URL: u})
	return err == nil && p != nil
}
