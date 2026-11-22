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
		g := &destinationGuard{allowPrivate: tt.allowPrivate}
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
		{"metadata address", "http://169.254.169.254/latest/meta-data/", destinationGuard{}, "link-local"},
		{"metadata address with private allowed", "http://169.254.169.254/", destinationGuard{allowPrivate: true}, "link-local"},
		{"loopback", "http://127.0.0.1:8080/x", destinationGuard{}, "private or loopback"},
		{"localhost name", "http://localhost:8080/x", destinationGuard{}, "private or loopback"},
		{"rfc1918", "https://10.0.0.7/bpp", destinationGuard{}, "private or loopback"},
		{"ipv6 loopback", "http://[::1]:9000/", destinationGuard{}, "private or loopback"},
		{"decimal ip form resolves to loopback or fails", "http://2130706433/", destinationGuard{}, ""},
		{"file scheme", "file:///etc/passwd", destinationGuard{}, "scheme"},
		{"gopher scheme", "gopher://example.com/", destinationGuard{}, "scheme"},
		{"credentials", "https://user:pw@8.8.8.8/", destinationGuard{}, "credentials"},
		{"no host", "http:///path", destinationGuard{}, "no host"},
		{"public ip", "https://8.8.8.8/bpp", destinationGuard{}, "-"},
		{"loopback allowed", "http://127.0.0.1:8080/x", destinationGuard{allowPrivate: true}, "-"},
		{"host not in allowlist", "https://8.8.8.8/", destinationGuard{allowedHosts: []string{"bpp.example.com"}}, "allowedDestinationHosts"},
		{"host in allowlist", "https://8.8.8.8/", destinationGuard{allowedHosts: []string{"8.8.8.8"}}, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = tt.guard.checkURL(ctx, u, net.DefaultResolver)
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
	g := newDestinationGuard(&HttpClientConfig{AllowedDestinationHosts: []string{"BPP.example.com", "*.partner.net", " "}})
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

	guarded := newHTTPClient(&HttpClientConfig{}, nil, newDestinationGuard(&HttpClientConfig{}))
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
		"allowPrivate": newHTTPClient(&HttpClientConfig{}, nil, newDestinationGuard(&HttpClientConfig{AllowPrivateDestinations: true})),
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
		guard := newDestinationGuard(&cfg)
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

	rr := serve(true, HttpClientConfig{})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), codeDestinationNotAllowed) {
		t.Fatalf("message-supplied loopback destination: got %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "127.0.0.1") {
		t.Fatalf("NACK leaks the refused address: %s", rr.Body.String())
	}
	if hits != 0 {
		t.Fatalf("upstream was contacted %d times", hits)
	}

	if rr := serve(false, HttpClientConfig{}); rr.Code != http.StatusOK || hits != 1 {
		t.Fatalf("config-defined destination: got %d %s, hits=%d", rr.Code, rr.Body.String(), hits)
	}
	if rr := serve(true, HttpClientConfig{AllowPrivateDestinations: true}); rr.Code != http.StatusOK || hits != 2 {
		t.Fatalf("allowPrivateDestinations: got %d %s, hits=%d", rr.Code, rr.Body.String(), hits)
	}
}
