package fetch

import (
	"fmt"
	"net"
	"net/url"
)

// checkPublicURL rejects non-HTTP(S) schemes and hosts that resolve to
// loopback/private/link-local addresses (the spec's "refuses private-address
// URLs" rule for untrusted publisher content).
func checkPublicURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("catalogcrawler: bad url %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("catalogcrawler: unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("catalogcrawler: missing host in %q", raw)
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("catalogcrawler: resolving %q: %w", host, err)
		}
		ips = resolved
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || isCGNAT(ip) {
			return fmt.Errorf("catalogcrawler: refusing private/loopback host %q", host)
		}
	}
	return nil
}

// isCGNAT reports whether ip is in the carrier-grade NAT range 100.64.0.0/10.
func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}
