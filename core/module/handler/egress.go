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
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/netguard"
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

// checkAddr reports why addr must not be dialled, or nil if it may be.
func (g *destinationGuard) checkAddr(addr netip.Addr) error {
	return netguard.Check(addr, g.allowPrivate)
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

// dialContext dials with the address check, except when the dial is to an
// egress proxy named in the environment: that connection is to the operator's
// own proxy, which may well be on a private address. Every other dial is
// checked, including direct ones made while proxy variables are set (NO_PROXY
// hosts, or http destinations when only HTTPS_PROXY is set).
func (g *destinationGuard) dialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	guarded := netguard.Dialer(g.allowPrivate)
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
