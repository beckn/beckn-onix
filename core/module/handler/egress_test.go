package handler

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

func TestDestinationGuardCheckAddr(t *testing.T) {
	tests := []struct {
		addr         string
		allowPrivate bool
		wantErr      bool
	}{
		{"8.8.8.8", false, false},
		{"2001:4860:4860::8888", false, false},
		// Never allowed, whatever the config.
		{"169.254.169.254", false, true},
		{"169.254.169.254", true, true},
		{"::ffff:169.254.169.254", true, true},
		{"fe80::1", true, true},
		{"0.0.0.0", true, true},
		{"::", true, true},
		{"224.0.0.1", true, true},
		// Private and loopback: refused unless allowPrivateDestinations.
		{"127.0.0.1", false, true},
		{"::1", false, true},
		{"10.1.2.3", false, true},
		{"172.16.0.1", false, true},
		{"192.168.1.1", false, true},
		{"100.64.0.1", false, true},
		{"fd00::1", false, true},
		{"::ffff:10.0.0.1", false, true},
		{"127.0.0.1", true, false},
		{"10.1.2.3", true, false},
		{"fd00::1", true, false},
	}
	for _, tt := range tests {
		g := &destinationGuard{allowPrivate: tt.allowPrivate, verdicts: map[string]time.Time{}}
		err := g.checkAddr(netip.MustParseAddr(tt.addr))
		if (err != nil) != tt.wantErr {
			t.Errorf("checkAddr(%s, allowPrivate=%v) err=%v, wantErr=%v", tt.addr, tt.allowPrivate, err, tt.wantErr)
		}
	}
}

func TestDestinationGuardCheckURL(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		raw     string
		guard   destinationGuard
		wantErr string
	}{
		{"metadata address", "http://169.254.169.254/latest/meta-data/", destinationGuard{verdicts: map[string]time.Time{}}, "link-local"},
		{"metadata address with private allowed", "http://169.254.169.254/", destinationGuard{allowPrivate: true, verdicts: map[string]time.Time{}}, "link-local"},
		{"loopback", "http://127.0.0.1:8080/x", destinationGuard{verdicts: map[string]time.Time{}}, "private or loopback"},
		{"localhost name", "http://localhost:8080/x", destinationGuard{verdicts: map[string]time.Time{}}, ""},
		{"rfc1918", "https://10.0.0.7/bpp", destinationGuard{verdicts: map[string]time.Time{}}, "private or loopback"},
		{"ipv6 loopback", "http://[::1]:9000/", destinationGuard{verdicts: map[string]time.Time{}}, "private or loopback"},
		{"decimal ip form", "http://2130706433/", destinationGuard{verdicts: map[string]time.Time{}}, "numeric"},
		{"octal ip form", "http://0177.0.0.1/", destinationGuard{verdicts: map[string]time.Time{}}, "numeric"},
		{"hex ip form", "http://0x7f.0.0.1/", destinationGuard{verdicts: map[string]time.Time{}}, "numeric"},
		{"file scheme", "file:///etc/passwd", destinationGuard{verdicts: map[string]time.Time{}}, "scheme"},
		{"gopher scheme", "gopher://example.com/", destinationGuard{verdicts: map[string]time.Time{}}, "scheme"},
		{"credentials", "https://user:pw@8.8.8.8/", destinationGuard{verdicts: map[string]time.Time{}}, "credentials"},
		{"no host", "http:///path", destinationGuard{verdicts: map[string]time.Time{}}, "no host"},
		{"public ip", "https://8.8.8.8/bpp", destinationGuard{verdicts: map[string]time.Time{}}, "-"},
		{"loopback allowed", "http://127.0.0.1:8080/x", destinationGuard{allowPrivate: true, verdicts: map[string]time.Time{}}, "-"},
		{"host not in allowlist", "https://8.8.8.8/", destinationGuard{allowedHosts: []string{"bpp.example.com"}, verdicts: map[string]time.Time{}}, "allowedDestinationHosts"},
		{"host in allowlist", "https://8.8.8.8/", destinationGuard{allowedHosts: []string{"8.8.8.8"}, verdicts: map[string]time.Time{}}, "-"},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = tt.guard.checkURL(ctx, u, net.DefaultResolver, false)
			switch tt.wantErr {
			case "-":
				if err != nil {
					t.Fatalf("want allowed, got %v", err)
				}
			case "":
				if err == nil {
					t.Fatalf("want refused, got allowed")
				}
			default:
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestDestinationGuardHostAllowed(t *testing.T) {
	g := mustGuard(t, HttpClientConfig{AllowedDestinationHosts: []string{"BPP.example.com.", "*.partner.net"}})
	for host, want := range map[string]bool{
		"bpp.example.com":      true,
		"BPP.Example.com.":     true,
		"x.bpp.example.com":    false,
		"a.partner.net":        true,
		"a.b.partner.net":      true,
		"partner.net":          false,
		"evilpartner.net":      false,
		"bpp.example.com.evil": false,
	} {
		if got := g.hostAllowed(host); got != want {
			t.Errorf("hostAllowed(%q)=%v, want %v", host, got, want)
		}
	}
}

// The dial-time check is what stops a name that passes checkURL and then
// resolves somewhere else. Dial a loopback listener directly through the
// guarded client and expect the connection to be refused.
func TestGuardedClientRefusesLoopbackAtDial(t *testing.T) {
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(k, "")
	}
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()

	deny := HttpClientConfig{AllowPrivateDestinations: boolPtr(false)}
	guarded := newHTTPClient(&deny, nil, mustGuard(t, deny))
	resp, err := guarded.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("guarded client connected to %s", srv.URL)
	}
	if !strings.Contains(err.Error(), "destination guard") {
		t.Fatalf("want destination guard error, got %v", err)
	}
	if hit {
		t.Fatal("request reached the loopback server")
	}

	// With private destinations allowed, and for config-defined targets
	// (unguarded client), the same address is reachable.
	for name, c := range map[string]*http.Client{
		"allowPrivate": newHTTPClient(&HttpClientConfig{}, nil, mustGuard(t, HttpClientConfig{AllowPrivateDestinations: boolPtr(true)})),
		"unguarded":    newHTTPClient(&HttpClientConfig{}, nil, nil),
	} {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp.Body.Close()
	}
}

type setRouteStep struct{ route *model.Route }

func (s setRouteStep) Run(ctx *model.StepContext) error { ctx.Route = s.route; return nil }

// A message-supplied destination on a refused network gets a NACK and no
// connection. The same URL as a config-defined target is forwarded.
func TestServeHTTPRefusesMessageSuppliedPrivateDestination(t *testing.T) {
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(k, "")
	}
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"message":{"ack":{"status":"ACK"}}}`))
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL + "/select")
	body := `{"context":{"version":"2.0.0","action":"select","message_id":"m1"},"message":{}}`

	serve := func(fromRequest bool, cfg HttpClientConfig) *httptest.ResponseRecorder {
		guard := mustGuard(t, cfg)
		h := &stdHandler{
			SubscriberID:      "test-sub",
			role:              model.RoleBAP,
			moduleName:        "test-module",
			steps:             []definition.Step{setRouteStep{&model.Route{TargetType: "url", URL: target, FromRequest: fromRequest}}},
			httpClient:        newHTTPClient(&cfg, nil, nil),
			requestDestClient: newHTTPClient(&cfg, nil, guard),
			destGuard:         guard,
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bap/caller/select", strings.NewReader(body)))
		return rr
	}

	rr := serve(true, HttpClientConfig{AllowPrivateDestinations: boolPtr(false)})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), codeDestinationNotAllowed) {
		t.Fatalf("message-supplied loopback destination: got %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "127.0.0.1") {
		t.Fatalf("NACK leaks the refused address: %s", rr.Body.String())
	}
	if hits != 0 {
		t.Fatalf("upstream was contacted %d times", hits)
	}

	if rr := serve(false, HttpClientConfig{AllowPrivateDestinations: boolPtr(false)}); rr.Code != http.StatusOK || hits != 1 {
		t.Fatalf("config-defined destination: got %d %s, hits=%d", rr.Code, rr.Body.String(), hits)
	}
	if rr := serve(true, HttpClientConfig{AllowPrivateDestinations: boolPtr(true)}); rr.Code != http.StatusOK || hits != 2 {
		t.Fatalf("allowPrivateDestinations: got %d %s, hits=%d", rr.Code, rr.Body.String(), hits)
	}
}

func boolPtr(b bool) *bool { return &b }

// An unset allowPrivateDestinations follows the release line's default, and
// the metadata address is refused whatever it is.
func TestDestinationGuardDefault(t *testing.T) {
	g := mustGuard(t, HttpClientConfig{})
	if g.allowPrivate != defaultAllowPrivateDestinations {
		t.Fatalf("allowPrivate=%v, want the default %v", g.allowPrivate, defaultAllowPrivateDestinations)
	}
	if err := g.checkAddr(netip.MustParseAddr("169.254.169.254")); err == nil {
		t.Fatal("metadata address allowed by default")
	}
	if g := mustGuard(t, HttpClientConfig{AllowPrivateDestinations: boolPtr(false)}); g.allowPrivate {
		t.Fatal("explicit false ignored")
	}
}

func mustGuard(t *testing.T, cfg HttpClientConfig) *destinationGuard {
	t.Helper()
	g, err := newDestinationGuard(&cfg)
	if err != nil {
		t.Fatalf("newDestinationGuard: %v", err)
	}
	return g
}

// Found in review: metadata services that are not link-local, and IPv4
// addresses carried inside IPv6 by NAT64, 6to4, Teredo and similar schemes.
func TestDestinationGuardMetadataAndEmbeddedAddresses(t *testing.T) {
	always := []string{
		"fd00:ec2::254", "fd00:ec2::23", "fd20:ce::254", "100.100.100.200",
		"64:ff9b::a9fe:a9fe",   // NAT64 of 169.254.169.254
		"64:ff9b:1::a9fe:a9fe", // local-use NAT64
		"2002:a9fe:a9fe::1",    // 6to4
		"::a9fe:a9fe",          // IPv4-compatible
		"::ffff:0:a9fe:a9fe",   // SIIT
	}
	for _, a := range always {
		for _, allowPrivate := range []bool{true, false} {
			g := mustGuard(t, HttpClientConfig{AllowPrivateDestinations: boolPtr(allowPrivate)})
			if err := g.checkAddr(netip.MustParseAddr(a)); err == nil {
				t.Errorf("allowPrivate=%v allows metadata address %s", allowPrivate, a)
			}
		}
	}
	strict := mustGuard(t, HttpClientConfig{AllowPrivateDestinations: boolPtr(false)})
	for _, a := range []string{
		"64:ff9b::a00:1", "64:ff9b::7f00:1", "2002:c0a8:101::1", "::ffff:0:a00:1", "::10.0.0.1",
		"2001:0:4136:e378:8000:63bf:f5ff:fffe", // Teredo, client 10.0.0.1
		"0.1.2.3", "192.0.0.8", "198.18.0.1", "240.0.0.1", "255.255.255.255", "fec0::1",
	} {
		if err := strict.checkAddr(netip.MustParseAddr(a)); err == nil {
			t.Errorf("strict mode allows %s", a)
		}
	}
	for _, a := range []string{"8.8.8.8", "2001:4860:4860::8888", "64:ff9b::808:808", "2002:808:808::1"} {
		if err := strict.checkAddr(netip.MustParseAddr(a)); err != nil {
			t.Errorf("strict mode refuses public address %s: %v", a, err)
		}
	}
}

func TestDestinationGuardAllowlistConfig(t *testing.T) {
	for _, bad := range [][]string{{""}, {"  "}, {"https://bpp.example.com"}, {"bpp.example.com:443"}, {"[::1]"}, {"*"}, {"*."}, {"a.*.example.com"}} {
		if _, err := newDestinationGuard(&HttpClientConfig{AllowedDestinationHosts: bad}); err == nil {
			t.Errorf("accepted allowedDestinationHosts %q", bad)
		}
	}
}

// Behind an egress proxy the proxy resolves names, so a name this host cannot
// resolve must not be refused; literal addresses are still checked.
func TestDestinationGuardViaProxy(t *testing.T) {
	g := mustGuard(t, HttpClientConfig{AllowPrivateDestinations: boolPtr(false)})
	u, _ := url.Parse("https://partner.name-only-the-proxy-knows.invalid/bpp")
	if err := g.checkURL(context.Background(), u, net.DefaultResolver, true); err != nil {
		t.Fatalf("refused a proxied destination for a local resolution failure: %v", err)
	}
	u, _ = url.Parse("http://10.0.0.5/bpp")
	if err := g.checkURL(context.Background(), u, net.DefaultResolver, true); err == nil {
		t.Fatal("literal private address allowed because a proxy is in use")
	}
	// A name this host CAN resolve is checked even when a proxy is in use.
	u, _ = url.Parse("http://localhost/bpp")
	if addrs, lerr := net.DefaultResolver.LookupNetIP(context.Background(), "ip", "localhost"); lerr == nil && len(addrs) > 0 {
		if err := g.checkURL(context.Background(), u, net.DefaultResolver, true); err == nil {
			t.Fatal("a name that resolves to loopback was left to the proxy")
		}
	}
}

// With proxy variables set, a dial that is not to the proxy is still checked.
func TestGuardedClientChecksDirectDialsWhenProxyIsSet(t *testing.T) {
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(k, "")
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1") // applies to https only; http goes direct
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	g := mustGuard(t, HttpClientConfig{AllowPrivateDestinations: boolPtr(false)})
	dial := g.dialContext()
	if c, err := dial(context.Background(), "tcp", strings.TrimPrefix(srv.URL, "http://")); err == nil {
		c.Close()
		t.Fatal("direct dial to loopback was not checked while HTTPS_PROXY is set")
	}
	if !egressProxyAddrs()["127.0.0.1:1"] {
		t.Fatal("proxy address not recognised")
	}
}

func TestLooksNumeric(t *testing.T) {
	for host, want := range map[string]bool{
		"2130706433": true, "0177.0.0.1": true, "0x7f.0.0.1": true, "0x7f000001": true, "127.1": true, "10.213.19722": true, "0": true,
		"3f2a9c0b1d2e": false, "deadbeef": false, "1a.example.com": false, "bpp.example.com": false, "1.2.3.4.5": false, "0x": false, "abc.def": false,
	} {
		if got := looksNumeric(host); got != want {
			t.Errorf("looksNumeric(%q)=%v, want %v", host, got, want)
		}
	}
}
