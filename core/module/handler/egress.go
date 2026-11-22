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
	"syscall"

	"github.com/beckn-one/beckn-onix/pkg/model"
)

// codeDestinationNotAllowed is the error code returned when a destination
// taken from the message is refused by the destination guard.
const codeDestinationNotAllowed = "NET_DESTINATION_NOT_ALLOWED"

// cgnatPrefix is the carrier-grade NAT range (RFC 6598), which IsPrivate does
// not cover.
var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// destinationGuard decides whether the adapter may connect to a destination
// that came from a message (context.bapUri / context.bppUri), as opposed to
// one written in the routing config. A counterparty chooses those addresses,
// so they must not be able to point the adapter at its own network.
type destinationGuard struct {
	allowPrivate bool
	allowedHosts []string
}

// defaultAllowPrivateDestinations is what an unset allowPrivateDestinations
// means. True in the 1.9 line so a patch upgrade does not cut off setups that
// reach counterparties on a private network.
const defaultAllowPrivateDestinations = true

func newDestinationGuard(cfg *HttpClientConfig) *destinationGuard {
	g := &destinationGuard{allowPrivate: defaultAllowPrivateDestinations}
	if cfg.AllowPrivateDestinations != nil {
		g.allowPrivate = *cfg.AllowPrivateDestinations
	}
	for _, h := range cfg.AllowedDestinationHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			g.allowedHosts = append(g.allowedHosts, h)
		}
	}
	return g
}

// checkAddr reports why addr must not be dialled, or nil if it may be.
func (g *destinationGuard) checkAddr(addr netip.Addr) error {
	addr = addr.Unmap()
	switch {
	case !addr.IsValid(), addr.IsUnspecified():
		return fmt.Errorf("address %s is not a usable destination", addr)
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast(), addr.IsInterfaceLocalMulticast(), addr.IsMulticast():
		// Includes 169.254.169.254 and fe80::/10, where cloud metadata
		// services listen. Never a participant endpoint.
		return fmt.Errorf("address %s is link-local or multicast", addr)
	}
	if g.allowPrivate {
		return nil
	}
	if addr.IsLoopback() || addr.IsPrivate() || cgnatPrefix.Contains(addr) {
		return fmt.Errorf("address %s is on a private or loopback network", addr)
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
			if strings.HasSuffix(host, "."+suffix) {
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

// checkURL validates a message-supplied destination before any connection is
// attempted, so a refused destination gets a NACK instead of a proxy error.
// It resolves the host and refuses if any address it resolves to is refused;
// control repeats the check on the address actually dialled.
func (g *destinationGuard) checkURL(ctx context.Context, u *url.URL, resolver *net.Resolver) error {
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
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("could not resolve host %q: %w", host, err)
	}
	for _, addr := range addrs {
		if err := g.checkAddr(addr); err != nil {
			return fmt.Errorf("host %q: %w", host, err)
		}
	}
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

// destinationNotAllowedErr is the NACK for a refused message-supplied
// destination. The reason is logged by the caller; the response names only
// the field, not which internal address was refused.
func destinationNotAllowedErr() *model.CodedErr {
	return model.NewCodedErr(http.StatusBadRequest, codeDestinationNotAllowed,
		fmt.Errorf("the destination URI in the message context is not allowed"))
}

// egressProxyConfigured reports whether outbound HTTP goes through a proxy
// set in the environment.
func egressProxyConfigured() bool {
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}
