// Package crawler retrieves and integrity-checks a remote artifact: fetch
// (conditional-GET aware, SSRF-guarded, size-capped), digest-check, decode,
// and self-signature verification against a registry-resolved key. It knows
// nothing about what the artifact contains -- a catalog index, a catalog
// file, a DeDi manifest, or any other self-signed document all go through the
// same primitives here. A caller that understands one of those formats layers
// its own parsing/unwrapping on top (see pkg/catalog for the catalog-shaped
// caller).
//
// There is exactly one correct way to fetch, verify, and decode an artifact
// per the file spec it is checked against -- this is deliberately plain Go,
// not an onix plugin: making "skip verification" a config choice would turn a
// protocol violation into a deployment option.
package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is the stdlib HTTP client for retrieval: GET with an SSRF guard,
// size cap, and optional conditional-GET support. Transport compression is
// disabled so resp.Body is always the exact artifact bytes a caller hashes
// and decodes itself (no digest-over-decompressed ambiguity).
type Client struct {
	hc           *http.Client
	timeout      time.Duration // whole-attempt budget, applied as a ctx deadline
	maxBytes     int64         // cap on the fetched (at-rest, possibly compressed) artifact
	allowPrivate bool          // allow loopback/private hosts (tests only)
}

// Option customizes a Client at construction. Nothing is mutated after
// NewClient returns, so a built client is safe to share.
type Option func(*Client)

// NewClient builds a retrieval client. allowPrivate must be false in
// production so a fetch can't be pointed at internal addresses (SSRF).
func NewClient(timeout time.Duration, maxBytes int64, allowPrivate bool, opts ...Option) *Client {
	c := &Client{timeout: timeout, maxBytes: maxBytes, allowPrivate: allowPrivate}
	for _, opt := range opts {
		opt(c)
	}
	c.hc = &http.Client{
		Timeout: timeout,
		// DisableCompression: resp.Body is the exact artifact bytes a caller
		// hashes/decodes. DialContext: the authoritative SSRF guard — it
		// validates the IP actually being connected to (closing the
		// DNS-rebinding TOCTOU that a URL-level pre-check alone leaves open);
		// see guardedDialContext.
		Transport: &http.Transport{
			DisableCompression: true,
			DialContext:        guardedDialContext(allowPrivate, timeout),
		},
		// Re-run the URL-level guard on every redirect hop too (fast, clear
		// rejection before the dial); the dialer re-guards the connection anyway.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("crawler: stopped after 10 redirects")
			}
			if !allowPrivate {
				// req.Context() is the original request's context, so the redirect
				// guard's DNS lookup stays inside the caller's fetch deadline.
				return checkPublicURL(req.Context(), req.URL.String())
			}
			return nil
		},
	}
	return c
}

// Conditions are the conditional-GET validators a caller may have stored from
// a prior fetch of the same URL.
type Conditions struct {
	ETag         string
	LastModified string
}

// Result is one GetConditional call's outcome.
type Result struct {
	Body         []byte // nil when NotModified
	NotModified  bool
	ETag         string
	LastModified string
}

// Get performs an unconditional, size-capped GET after the SSRF guard.
func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	res, err := c.GetConditional(ctx, url, Conditions{})
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

// GetConditional performs a size-capped GET after the SSRF guard, sending
// cond as If-None-Match / If-Modified-Since when set. A host that honours
// them can answer 304 (Result.NotModified, no body); a host that sends no
// validators simply returns 200. On a 200 the response's ETag/Last-Modified
// are returned for the caller to store for next time.
func (c *Client) GetConditional(ctx context.Context, url string, cond Conditions) (Result, error) {
	// Bound the whole attempt here, at the top of the request path, before the
	// SSRF pre-check resolves an untrusted host. A caller typically passes a
	// long-lived context with no deadline of its own, and the SSRF pre-check's
	// DNS lookup runs BEFORE the request object exists, so a host whose
	// resolver hangs would otherwise stall the caller forever.
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	if !c.allowPrivate {
		if err := checkPublicURL(ctx, url); err != nil {
			return Result{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, err
	}
	if cond.ETag != "" {
		req.Header.Set("If-None-Match", cond.ETag)
	}
	if cond.LastModified != "" {
		req.Header.Set("If-Modified-Since", cond.LastModified)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		// Echo the validators the caller sent so they stay stored for next time.
		return Result{NotModified: true, ETag: cond.ETag, LastModified: cond.LastModified}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("crawler: GET %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("crawler: reading %s: %w", url, err)
	}
	if int64(len(b)) > c.maxBytes {
		// Permanent: the artifact has to be published smaller, so a caller
		// should give up until it is, rather than re-downloading an over-cap
		// artifact forever.
		return Result{}, PermanentFaultf(FaultOversize, "crawler: %s exceeds max %d bytes", url, c.maxBytes)
	}
	return Result{Body: b, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}, nil
}

// bounded derives a context carrying the client's configured timeout as a
// deadline. A non-positive timeout means "no limit" and is honoured as such,
// matching http.Client.Timeout.
func (c *Client) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}
