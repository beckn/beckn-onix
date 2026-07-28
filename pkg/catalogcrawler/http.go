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
	hc              *http.Client
	maxBytes        int64 // cap on the fetched (compressed, at-rest) artifact
	maxDecompressed int64 // cap on the decoded output (decompression-bomb guard)
	allowPrivate    bool  // allow loopback/private hosts (tests only)
}

// NewHTTPClient builds a client. allowPrivate must be false in production so
// fetches of publisher URLs can't be pointed at internal addresses (SSRF).
// Transport compression is disabled so resp.Body is always the exact artifact
// bytes we hash and decode ourselves (no digest-over-decompressed ambiguity).
func NewHTTPClient(timeout time.Duration, maxBytes, maxDecompressed int64, allowPrivate bool) *HTTPClient {
	c := &HTTPClient{maxBytes: maxBytes, maxDecompressed: maxDecompressed, allowPrivate: allowPrivate}
	c.hc = &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DisableCompression: true},
		// Re-run the SSRF guard on every redirect hop; a public host must not be
		// able to bounce us to an internal address via 3xx Location.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("catalogcrawler: stopped after 10 redirects")
			}
			if !allowPrivate {
				return checkPublicURL(req.URL.String())
			}
			return nil
		},
	}
	return c
}

// FetchIndex GETs, decodes (the index itself may be compressed — encoding is
// inferred from the URL suffix), and parses a publisher's catalog index. cond's
// validators are sent as If-None-Match / If-Modified-Since; a host that honours
// them can answer 304 (NotModified, no body). A host that sends no validators
// simply returns 200 and the caller falls back to its version-based gate.
func (c *HTTPClient) FetchIndex(ctx context.Context, indexURL string, cond IndexCond) (IndexResult, error) {
	b, meta, err := c.getCond(ctx, indexURL, cond)
	if err != nil {
		return IndexResult{}, err
	}
	if meta.notModified {
		// Echo the validators we sent so they stay stored for next time.
		return IndexResult{NotModified: true, ETag: cond.ETag, LastModified: cond.LastModified}, nil
	}
	decoded, err := decode(encodingFor("", indexURL), b, c.maxDecompressed)
	if err != nil {
		return IndexResult{}, err
	}
	var idx Index
	if err := json.Unmarshal(decoded, &idx); err != nil {
		return IndexResult{}, fmt.Errorf("catalogcrawler: parsing index %s: %w", indexURL, err)
	}
	return IndexResult{Index: idx, ETag: meta.etag, LastModified: meta.lastModified}, nil
}

// FetchFile GETs one file and verifies its bytes against the declared digest.
// The digest is mandatory — it's the only integrity check in Phase 1 (signature
// verification is Phase 2), so a missing digest fails closed rather than
// letting unverified bytes through.
func (c *HTTPClient) FetchFile(ctx context.Context, f FileEntry) ([]byte, error) {
	if f.Digest == "" {
		return nil, permanentf("catalogcrawler: %s has no digest (integrity check required)", f.URL)
	}
	b, err := c.get(ctx, f.URL)
	if err != nil {
		return nil, err
	}
	// Digest covers the artifact at rest (the compressed bytes) — verify BEFORE
	// spending CPU/memory inflating, so a tampered or bomb file is rejected on a
	// cheap hash. Only authenticated bytes are ever decoded.
	if !digestMatches(b, f.Digest) {
		return nil, fmt.Errorf("catalogcrawler: digest mismatch for %s", f.URL)
	}
	return decode(encodingFor(f.Encoding, f.URL), b, c.maxDecompressed)
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

// respMeta carries conditional-GET response metadata.
type respMeta struct {
	notModified  bool
	etag         string
	lastModified string
}

// get performs a size-capped GET after the SSRF guard (unconditional).
func (c *HTTPClient) get(ctx context.Context, raw string) ([]byte, error) {
	b, _, err := c.getCond(ctx, raw, IndexCond{})
	return b, err
}

// getCond is get with optional conditional-GET validators: it sends
// If-None-Match / If-Modified-Since when cond is set and reports a 304 via
// respMeta.notModified (no body), so the caller can skip the download. On a
// 200 it returns the host's ETag / Last-Modified for the caller to store.
func (c *HTTPClient) getCond(ctx context.Context, raw string, cond IndexCond) ([]byte, respMeta, error) {
	if !c.allowPrivate {
		if err := checkPublicURL(raw); err != nil {
			return nil, respMeta{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, respMeta{}, err
	}
	if cond.ETag != "" {
		req.Header.Set("If-None-Match", cond.ETag)
	}
	if cond.LastModified != "" {
		req.Header.Set("If-Modified-Since", cond.LastModified)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, respMeta{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, respMeta{notModified: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, respMeta{}, fmt.Errorf("catalogcrawler: GET %s: status %d", raw, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, respMeta{}, fmt.Errorf("catalogcrawler: reading %s: %w", raw, err)
	}
	if int64(len(b)) > c.maxBytes {
		return nil, respMeta{}, fmt.Errorf("catalogcrawler: %s exceeds max %d bytes", raw, c.maxBytes)
	}
	meta := respMeta{etag: resp.Header.Get("ETag"), lastModified: resp.Header.Get("Last-Modified")}
	return b, meta, nil
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
