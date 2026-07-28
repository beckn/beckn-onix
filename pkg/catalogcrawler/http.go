package catalogcrawler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPClient is the crawler's stdlib HTTP client: it fetches + parses the
// index, fetches + digest-verifies catalog files (SSRF-guarded, size-
// capped), and POSTs push bodies to Discovery. Framework-agnostic.
type HTTPClient struct {
	hc           *http.Client
	maxBytes     int64
	allowPrivate bool // allow loopback/private hosts (tests only)
}

// NewHTTPClient builds a client. allowPrivate must be false in production so
// fetches of publisher URLs can't be pointed at internal addresses (SSRF).
func NewHTTPClient(timeout time.Duration, maxBytes int64, allowPrivate bool) *HTTPClient {
	return &HTTPClient{hc: &http.Client{Timeout: timeout}, maxBytes: maxBytes, allowPrivate: allowPrivate}
}

// FetchIndex GETs and parses a publisher's catalog index.
func (c *HTTPClient) FetchIndex(ctx context.Context, indexURL string) (Index, error) {
	b, err := c.get(ctx, indexURL)
	if err != nil {
		return Index{}, err
	}
	var idx Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return Index{}, fmt.Errorf("catalogcrawler: parsing index %s: %w", indexURL, err)
	}
	return idx, nil
}

// FetchFile GETs one file and verifies its bytes against the declared digest.
func (c *HTTPClient) FetchFile(ctx context.Context, f FileEntry) ([]byte, error) {
	b, err := c.get(ctx, f.URL)
	if err != nil {
		return nil, err
	}
	if f.Digest != "" && !digestMatches(b, f.Digest) {
		return nil, fmt.Errorf("catalogcrawler: digest mismatch for %s", f.URL)
	}
	return b, nil
}

// Push POSTs a /push body to the (trusted, operator-configured) Discovery
// endpoint. 200 = accepted; anything else is a non-ack with the body as the
// reason. No SSRF guard here — the endpoint is config, not attacker input.
func (c *HTTPClient) Push(ctx context.Context, endpoint string, body []byte) (PartOutcome, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return PartOutcome{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return PartOutcome{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	out := PartOutcome{Acked: resp.StatusCode == http.StatusOK, HTTPStatus: resp.StatusCode}
	if !out.Acked {
		out.Reason = strings.TrimSpace(string(respBody))
	}
	return out, nil
}

// get performs a size-capped GET after the SSRF guard.
func (c *HTTPClient) get(ctx context.Context, raw string) ([]byte, error) {
	if !c.allowPrivate {
		if err := checkPublicURL(raw); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalogcrawler: GET %s: status %d", raw, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("catalogcrawler: reading %s: %w", raw, err)
	}
	if int64(len(b)) > c.maxBytes {
		return nil, fmt.Errorf("catalogcrawler: %s exceeds max %d bytes", raw, c.maxBytes)
	}
	return b, nil
}

// digestMatches compares body's SHA-256 to a "sha-256:<hex>" digest.
func digestMatches(body []byte, expected string) bool {
	e := strings.ToLower(strings.TrimSpace(expected))
	const prefix = "sha-256:"
	if !strings.HasPrefix(e, prefix) {
		return false
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]) == e[len(prefix):]
}

// checkPublicURL rejects non-HTTP(S) schemes and hosts that resolve to
// loopback/private/link-local addresses (the spec's "refuses private-
// address URLs" rule for untrusted publisher content).
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
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("catalogcrawler: refusing private/loopback host %q", host)
		}
	}
	return nil
}
