package fetch

// guard.go — the SSRF guard: rejects non-HTTP(S) schemes and any host that
// resolves to a loopback/private/link-local/CGNAT/unspecified address, so a
// publisher URL can't be pointed at an internal service.

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
)

// checkPublicURL rejects non-HTTP(S) schemes and hosts that resolve to
// loopback/private/link-local addresses (the spec's "refuses private-address
// URLs" rule for untrusted publisher content).
func checkPublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("crawler: bad url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("crawler: unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("crawler: missing host in %q", raw)
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("crawler: resolving %q: %w", host, err)
		}
		ips = resolved
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			// Permanent: the published URL points at an internal address, which no
			// amount of retrying fixes. Park it so an operator sees the rejection
			// instead of it looping as "transient" forever.
			return catalog.PermanentFaultf(catalog.FaultSSRF, "crawler: refusing private/loopback host %q", host)
		}
	}
	return nil
}

// isPublicIP reports whether ip is a routable public address — i.e. NOT
// loopback/private/link-local/CGNAT/unspecified. The single source of truth for
// "is this address safe to fetch", used by both checkPublicURL (the early,
// URL-level guard) and guardedDialContext (the connect-time guard).
func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || isCGNAT(ip))
}

// isCGNAT reports whether ip is in the carrier-grade NAT range 100.64.0.0/10.
func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// guardedDialContext returns a net dialer that closes the DNS-rebinding hole in
// checkPublicURL: that guard resolves the host, but the HTTP transport would
// normally re-resolve independently at dial time, so a host that answers public
// on the first lookup and internal on the second could still be connected to.
// This dialer resolves ONCE at dial time, rejects any non-public resolved IP,
// and dials a validated IP DIRECTLY — so the connection always lands on an
// address that passed the check. It also re-guards every redirect hop (each hop
// dials afresh). allowPrivate (tests only) bypasses the check.
func guardedDialContext(allowPrivate bool, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if allowPrivate {
			return d.DialContext(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("crawler: resolving %q: %w", host, err)
		}
		var lastErr error
		for _, ipa := range ips {
			if !isPublicIP(ipa.IP) {
				// Permanent (see checkPublicURL). net/http wraps this in *url.Error,
				// and errors.As unwraps through it, so the class still reaches
				// ClassifyFault at the far end.
				lastErr = catalog.PermanentFaultf(catalog.FaultSSRF, "crawler: refusing private/loopback address %s for %q", ipa.IP, host)
				continue
			}
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ipa.IP.String(), port))
			if err != nil {
				lastErr = err
				continue
			}
			return conn, nil
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("crawler: no usable address for %q", host)
		}
		return nil, lastErr
	}
}
