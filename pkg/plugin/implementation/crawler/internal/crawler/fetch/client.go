// Package fetch retrieves + integrity-checks publisher artifacts: it fetches +
// parses the index (conditional-GET aware) and fetches + signature-verifies +
// digest-verifies + decodes catalog files, SSRF-guarded and size-capped.
// Framework-agnostic; imports only catalog + decode + the shared
// artifactverifier primitives.
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/catalog"
	"github.com/beckn-one/beckn-onix/pkg/plugin/implementation/crawler/internal/crawler/decode"
)

// Client is the crawler's stdlib HTTP client for retrieval: it fetches + parses
// the index and fetches + digest-verifies catalog files. Transport compression
// is disabled so resp.Body is always the exact artifact bytes we hash and
// decode ourselves (no digest-over-decompressed ambiguity).
type Client struct {
	hc              *http.Client
	timeout         time.Duration // FetchTimeout: the whole-attempt budget, applied as a ctx deadline
	maxBytes        int64         // cap on the fetched (compressed, at-rest) artifact
	maxDecompressed int64         // cap on the decoded output (decompression-bomb guard)
	allowPrivate    bool          // allow loopback/private hosts (tests only)
	keys            KeySource
}

// Option customizes a Client at construction. Nothing is mutated after NewClient
// returns, so a built client is safe to share.
type Option func(*Client)

// WithTrustedKeys injects the publisher keys entry/file self-signatures are
// verified against. Without it the client has no trust anchor and FetchIndex
// drops every entry and FetchFile rejects every file (fail closed) — see
// verify.go.
func WithTrustedKeys(keys KeySource) Option {
	return func(c *Client) { c.keys = keys }
}

// NewClient builds a retrieval client. allowPrivate must be false in production
// so fetches of publisher URLs can't be pointed at internal addresses (SSRF).
// Pass WithTrustedKeys to supply the signature gate's trust anchor.
func NewClient(timeout time.Duration, maxBytes, maxDecompressed int64, allowPrivate bool, opts ...Option) *Client {
	c := &Client{
		timeout:         timeout,
		maxBytes:        maxBytes,
		maxDecompressed: maxDecompressed,
		allowPrivate:    allowPrivate,
	}
	for _, opt := range opts {
		opt(c)
	}
	c.hc = &http.Client{
		Timeout: timeout,
		// DisableCompression: resp.Body is the exact artifact bytes we hash/decode.
		// DialContext: the authoritative SSRF guard — it validates the IP actually
		// being connected to (closing the DNS-rebinding TOCTOU that a URL-level
		// pre-check alone leaves open); see guardedDialContext.
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
	// Parsed generically first, so each catalog entry's self-signature (file
	// spec: "each catalog entry signs itself") is verified against its OWN raw
	// JSON bytes as received — never a Go-struct re-marshal, which can
	// silently drop/reorder fields and break canonicalization.
	var raw struct {
		NodeID     string            `json:"nodeId"`
		NextUpdate string            `json:"next_update"`
		Catalogs   []json.RawMessage `json:"catalogs"`
	}
	if err := json.Unmarshal(decoded, &raw); err != nil {
		return catalog.IndexResult{}, fmt.Errorf("crawler: parsing index %s: %w", indexURL, err)
	}
	idx := catalog.Index{NodeID: raw.NodeID, NextUpdate: raw.NextUpdate}
	var dropped []catalog.DroppedEntry
	for _, entryRaw := range raw.Catalogs {
		var entry catalog.CatalogEntry
		if err := json.Unmarshal(entryRaw, &entry); err != nil {
			// malformed entry: not trustworthy, drop it (fail closed). Reported so
			// the caller doesn't see a clean poll with silently fewer catalogs.
			dropped = append(dropped, catalog.DroppedEntry{Reason: fmt.Sprintf("parsing entry: %v", err)})
			continue
		}
		if err := verifyEntrySignature(ctx, c.keys, raw.NodeID, entryRaw, entry.Signature); err != nil {
			// unverifiable entry: drop it rather than trust an unsigned/forged one.
			dropped = append(dropped, catalog.DroppedEntry{CatalogID: entry.CatalogID, Reason: err.Error()})
			continue
		}
		idx.Catalogs = append(idx.Catalogs, entry)
	}
	return catalog.IndexResult{Index: idx, ETag: meta.etag, LastModified: meta.lastModified, Dropped: dropped}, nil
}

// FetchFile GETs one file and verifies it end to end: the fetched bytes
// against the declared digest, then the file's OWN embedded self-signature
// (file spec: baseline and change files alike sign their own content). Both
// are mandatory and both fail closed — the digest says these are the bytes
// the index named, the signature says the publisher genuinely produced them.
// A digest alone would let anyone who can serve the URL swap in their own
// signed-by-nobody content.
//
// nodeID is the publishing node's identity (the enclosing index's nodeId),
// used to resolve the file's signing key through the registry. catalogID is
// the enclosing index entry's own catalogId, cross-checked (CON-TBD-12)
// against the file's internal catalogId/version once its signature verifies.
func (c *Client) FetchFile(ctx context.Context, nodeID, catalogID string, f catalog.FileEntry) ([]byte, error) {
	if f.Digest == "" {
		return nil, catalog.Permanentf("crawler: %s has no digest (integrity check required)", f.URL)
	}
	b, err := c.get(ctx, f.URL)
	if err != nil {
		return nil, err
	}
	// Decode BEFORE the digest check: RFC NFH-014 CON-TBD-29 requires the digest
	// (and signature) to cover the canonical DECOMPRESSED content, never the
	// compressed bytes at rest -- gzip's own output isn't guaranteed byte-stable
	// across tool versions for identical input, so digesting the compressed form
	// would make a legitimate re-publish indistinguishable from tampering. The
	// decompression-bomb guard still runs first regardless: decode.Decode caps
	// the inflated size (reject, not truncate) before any digest/signature work
	// touches the result, so this reordering does not weaken that protection.
	decoded, err := decode.Decode(decode.EncodingFor(f.Encoding, f.URL), b, c.maxDecompressed)
	if err != nil {
		return nil, err
	}
	// Permanent, not transient: re-fetching the same URL yields the same bad
	// bytes. This is the spec's "treat as tampering and flag it" signal, so it
	// must park + alert (ERROR) rather than retry on a 5-minute loop forever,
	// logged as a network blip.
	if !digestMatches(decoded, f.Digest) {
		return nil, catalog.PermanentFaultf(catalog.FaultDigestMismatch, "crawler: digest mismatch for %s", f.URL)
	}
	// Signature gate on the decoded (plain-JSON) content: the embedded signature
	// covers the document as authored, not its at-rest packaging. Bounded on its
	// own budget because a KeySource can do I/O (a registry lookup) and must not
	// hang the queue drain.
	return c.verifyFileBounded(ctx, nodeID, catalogID, f.URL, decoded, f.EffectiveVersion())
}

// respMeta carries conditional-GET response metadata.
type respMeta struct {
	notModified  bool
	etag         string
	lastModified string
}

// bounded derives a context carrying the client's configured FetchTimeout as a
// deadline.
//
// Every entry point applies this itself rather than trusting the caller. The
// callers pass the engine's Start context, which has no deadline, and
// http.Client.Timeout cannot help: the SSRF pre-check and its DNS lookup run
// BEFORE the request object exists, so a publisher host whose resolver hangs
// would stall the pass forever while holding that index's lock, or stall the
// whole queue drain from a FetchFile. The deadline covers the pre-check, the
// dial (which re-resolves), every redirect hop, and the body read.
//
// A non-positive timeout means "no limit" and is honoured as such, matching
// http.Client.Timeout.
func (c *Client) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}

// verifyFileBounded runs the file self-signature gate under its own
// FetchTimeout budget, so a KeySource that never answers cannot block the
// caller indefinitely.
func (c *Client) verifyFileBounded(ctx context.Context, nodeID, catalogID, url string, decoded []byte, wantVersion int64) ([]byte, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	return verifyFileSignature(ctx, c.keys, nodeID, url, decoded, catalogID, wantVersion)
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
	// Bound the whole attempt here, at the top of the request path, before the
	// SSRF pre-check resolves an untrusted host. See bounded.
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	if !c.allowPrivate {
		if err := checkPublicURL(ctx, raw); err != nil {
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
		return nil, respMeta{}, fmt.Errorf("crawler: GET %s: status %d", raw, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, respMeta{}, fmt.Errorf("crawler: reading %s: %w", raw, err)
	}
	// Permanent for the same reason the decompressed cap is (decode/registry.go):
	// the publisher has to publish a smaller artifact, so park until they do
	// instead of re-downloading an over-cap file forever. Note this is NOT the
	// non-200 branch above — a 5xx must stay transient and retry.
	if int64(len(b)) > c.maxBytes {
		return nil, respMeta{}, catalog.PermanentFaultf(catalog.FaultOversize, "crawler: %s exceeds max %d bytes", raw, c.maxBytes)
	}
	meta := respMeta{etag: resp.Header.Get("ETag"), lastModified: resp.Header.Get("Last-Modified")}
	return b, meta, nil
}
