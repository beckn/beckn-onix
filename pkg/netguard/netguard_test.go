package netguard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	always := []string{"169.254.169.254", "::ffff:169.254.169.254", "fe80::1", "0.0.0.0", "::", "224.0.0.1",
		"fd00:ec2::254", "fd20:ce::254", "100.100.100.200", "64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::1"}
	for _, a := range always {
		for _, allowPrivate := range []bool{true, false} {
			if Check(netip.MustParseAddr(a), allowPrivate) == nil {
				t.Errorf("allowPrivate=%v allows %s", allowPrivate, a)
			}
		}
	}
	private := []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "100.64.0.1", "fd00::1", "64:ff9b::a00:1", "198.18.0.1"}
	for _, a := range private {
		if Check(netip.MustParseAddr(a), false) == nil {
			t.Errorf("strict mode allows %s", a)
		}
		if err := Check(netip.MustParseAddr(a), true); err != nil {
			t.Errorf("allowPrivate refuses %s: %v", a, err)
		}
	}
	for _, a := range []string{"8.8.8.8", "2001:4860:4860::8888", "64:ff9b::808:808"} {
		if err := Check(netip.MustParseAddr(a), false); err != nil {
			t.Errorf("strict mode refuses public %s: %v", a, err)
		}
	}
}

func TestDialerRefusesAtDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	if c, err := Dialer(false).DialContext(context.Background(), "tcp", addr); err == nil {
		c.Close()
		t.Fatal("strict dialler connected to loopback")
	}
	c, err := Dialer(true).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatalf("permissive dialler refused loopback: %v", err)
	}
	c.Close()
	if _, err := Dialer(true).DialContext(context.Background(), "tcp", net.JoinHostPort("169.254.169.254", "80")); err == nil || !strings.Contains(err.Error(), "destination guard") {
		t.Fatalf("metadata address not refused by the guard: %v", err)
	}
}
