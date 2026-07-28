// Package fetch retrieves + integrity-checks publisher artifacts: it fetches +
// parses the index (conditional-GET aware) and fetches + digest-verifies +
// decodes catalog files, SSRF-guarded and size-capped. Framework-agnostic;
// imports only catalog + decode.
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/catalogcrawler/decode"
)

// Client is the crawler's stdlib HTTP client for retrieval: it fetches + parses
// the index and fetches + digest-verifies catalog files. Transport compression
// is disabled so resp.Body is always the exact artifact bytes we hash and
// decode ourselves (no digest-over-decompressed ambiguity).
type Client struct {
	hc              *http.Client
	maxBytes        int64 // cap on the fetched (compressed, at-rest) artifact
	maxDecompressed int64 // cap on the decoded output (decompression-bomb guard)
	allowPrivate    bool  // allow loopback/private hosts (tests only)
}

// NewClient builds a retrieval client. allowPrivate must be false in production
// so fetches of publisher URLs can't be pointed at internal addresses (SSRF).
func NewClient(timeout time.Duration, maxBytes, maxDecompressed int64, allowPrivate bool) *Client {
	c := &Client{maxBytes: maxBytes, maxDecompressed: maxDecompressed, allowPrivate: allowPrivate}
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
func (c *Client) FetchIndex(ctx context.Context, indexURL string, cond catalog.IndexConditions) (catalog.IndexResult, error) {
	b, meta, err := c.getConditional(ctx, indexURL, cond)
	if err != nil {
		return catalog.IndexResult{}, err
	}
	if meta.notModified {
		// Echo the validators we sent so they stay stored for next time.
		return catalog.IndexResult{NotModified: true, ETag: cond.ETag, LastModified: cond.LastModified}, nil
	}
	decoded, err := decode.Decode(decode.EncodingFor("", indexURL), b, c.maxDecompressed)
	if err != nil {
		return catalog.IndexResult{}, err
	}
	var idx catalog.Index
	if err := json.Unmarshal(decoded, &idx); err != nil {
		return catalog.IndexResult{}, fmt.Errorf("catalogcrawler: parsing index %s: %w", indexURL, err)
	}
	return catalog.IndexResult{Index: idx, ETag: meta.etag, LastModified: meta.lastModified}, nil
}

// FetchFile GETs one file and verifies its bytes against the declared digest.
// The digest is mandatory — it's the only integrity check in Phase 1 (signature
// verification is Phase 2), so a missing digest fails closed rather than letting
// unverified bytes through.
func (c *Client) FetchFile(ctx context.Context, f catalog.FileEntry) ([]byte, error) {
	if f.Digest == "" {
		return nil, catalog.Permanentf("catalogcrawler: %s has no digest (integrity check required)", f.URL)
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
	return decode.Decode(decode.EncodingFor(f.Encoding, f.URL), b, c.maxDecompressed)
}

// respMeta carries conditional-GET response metadata.
type respMeta struct {
	notModified  bool
	etag         string
	lastModified string
}

// get performs a size-capped GET after the SSRF guard (unconditional).
func (c *Client) get(ctx context.Context, raw string) ([]byte, error) {
	b, _, err := c.getConditional(ctx, raw, catalog.IndexConditions{})
	return b, err
}

// getConditional is get with optional conditional-GET validators: it sends
// If-None-Match / If-Modified-Since when cond is set and reports a 304 via
// respMeta.notModified (no body), so the caller can skip the download. On a 200
// it returns the host's ETag / Last-Modified for the caller to store.
func (c *Client) getConditional(ctx context.Context, raw string, cond catalog.IndexConditions) ([]byte, respMeta, error) {
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
