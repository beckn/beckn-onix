package fetch

// guard_test.go — the address side of the SSRF guard. isPublicIP is the single
// decision both the URL-level pre-check and the connect-time dialer make, so
// every non-routable range it misses is a host the crawler will actually dial.

import (
	"context"
	"net"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
)

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		// Loopback.
		{name: "ipv4 loopback", ip: "127.0.0.1"},
		{name: "ipv4 loopback high", ip: "127.255.255.254"},
		{name: "ipv6 loopback", ip: "::1"},

		// RFC1918 private and IPv6 unique-local.
		{name: "private 10/8", ip: "10.0.0.1"},
		{name: "private 172.16/12", ip: "172.16.0.1"},
		{name: "private 192.168/16", ip: "192.168.1.1"},
		{name: "ipv6 unique local", ip: "fd00::1"},

		// Link-local unicast, including the cloud metadata address.
		{name: "link local unicast", ip: "169.254.169.254"},
		{name: "ipv6 link local unicast", ip: "fe80::1"},

		// Unspecified.
		{name: "ipv4 unspecified", ip: "0.0.0.0"},
		{name: "ipv6 unspecified", ip: "::"},

		// CGNAT 100.64.0.0/10.
		{name: "cgnat low", ip: "100.64.0.1"},
		{name: "cgnat high", ip: "100.127.255.255"},

		// 0.0.0.0/8. Only 0.0.0.0 is IsUnspecified; the rest of the block used to
		// pass as public and Linux routes it to the local host.
		{name: "this network low", ip: "0.0.0.1"},
		{name: "this network high", ip: "0.255.255.255"},

		// Multicast. Link-local multicast was already caught; global and
		// organisational multicast were not.
		{name: "link local multicast", ip: "224.0.0.1"},
		{name: "global multicast", ip: "224.0.1.1"},
		{name: "ssdp multicast", ip: "239.255.255.250"},
		{name: "ipv6 multicast", ip: "ff02::1"},

		// Reserved 240.0.0.0/4, plus the broadcast address at its top.
		{name: "reserved class e low", ip: "240.0.0.1"},
		{name: "reserved class e high", ip: "255.255.255.254"},
		{name: "broadcast", ip: "255.255.255.255"},

		// Public — the regression guard. Each sits just outside a blocked range.
		{name: "public ipv4", ip: "93.184.216.34", want: true},
		{name: "public dns", ip: "8.8.8.8", want: true},
		{name: "just below cgnat", ip: "100.63.255.255", want: true},
		{name: "just above cgnat", ip: "100.128.0.0", want: true},
		{name: "just below multicast", ip: "223.255.255.255", want: true},
		{name: "just below this network", ip: "1.0.0.1", want: true},
		{name: "public ipv6", ip: "2606:2800:220:1::248", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("ParseIP(%q) = nil, the test case is malformed", tt.ip)
			}
			if got := isPublicIP(ip); got != tt.want {
				t.Fatalf("isPublicIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

// The URL-level guard must turn a non-routable literal into a PERMANENT SSRF
// fault, so it parks and alerts rather than retrying an internal address on a
// five-minute loop.
func TestCheckPublicURL_NonRoutableRanges(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "this network", url: "http://0.0.0.1/x"},
		{name: "this network high", url: "http://0.255.255.255/x"},
		{name: "reserved class e", url: "http://240.0.0.1/x"},
		{name: "broadcast", url: "http://255.255.255.255/x"},
		{name: "global multicast", url: "http://224.0.1.1/x"},
		{name: "ssdp multicast", url: "http://239.255.255.250/x"},
		{name: "ipv6 multicast", url: "http://[ff02::1]/x"},
		{name: "loopback", url: "http://127.0.0.1/x"},
		{name: "cgnat", url: "http://100.64.0.1/x"},
		{name: "metadata service", url: "http://169.254.169.254/latest/meta-data/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertPermanentFault(t, checkPublicURL(context.Background(), tt.url), catalog.FaultSSRF)
		})
	}
}
