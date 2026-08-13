package source

// dediquery.go — a RegistryClient backed by the DeDi registry /query endpoint.
// It resolves a networkId to the index URLs of the provider nodes that publish
// a catalog. Framework-agnostic (net/http + encoding/json only).
//
// The {base} registry URL is TRUSTED deployment config, so the crawler's SSRF
// guard is deliberately NOT applied to this GET — that guard exists for the
// UNtrusted publisher artifact URLs this lookup discovers, and it still runs on
// them later in the fetch package. Nothing here weakens that.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxQueryBytes caps the /query response we read into memory, so a runaway
// registry cannot exhaust the crawler. The response is metadata (one record per
// network participant), so a generous few MiB is far more than any real network
// needs.
const maxQueryBytes = 8 << 20

// DediQueryClient resolves a networkId to its provider index URLs via
// GET {base}/query/{networkId}.
type DediQueryClient struct {
	base    string // registry base URL, trailing slash trimmed
	hc      *http.Client
	timeout time.Duration // per-lookup budget, applied as a ctx deadline
}

// NewDediQueryClient builds a query client against the DeDi registry rooted at
// baseURL (e.g. https://fabric.nfh.global/registry/dedi). timeout is the
// crawler's FetchTimeout, applied to each lookup.
func NewDediQueryClient(baseURL string, timeout time.Duration) *DediQueryClient {
	return &DediQueryClient{
		base:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		hc:      &http.Client{Timeout: timeout},
		timeout: timeout,
	}
}

// queryResponse is the subset of the /query envelope the crawler consumes.
// Details and Meta are pointers so a null field decodes to nil (not a panic)
// rather than a zero struct that would be indistinguishable from present-but-
// empty.
type queryResponse struct {
	Data struct {
		Records []queryRecord `json:"records"`
	} `json:"data"`
}

type queryRecord struct {
	State   string        `json:"state"`
	Details *queryDetails `json:"details"`
	Meta    *queryMeta    `json:"meta"`
}

type queryDetails struct {
	SubscriberID string `json:"subscriber_id"`
}

// queryMeta is the registry's generic, schema-agnostic meta object (RFC
// NFH-014 CON-TBD-33: "a PN MUST place catalog_index_urls in its Beckn
// Subscriber record's meta object, and a DS MUST look for it there"). It is a
// LIST of {url} objects, not a single URL string -- a node may host more than
// one catalog index (e.g. separating a fast-moving retail catalog from a
// slow-moving mobility one).
//
// Kept as a raw JSON value rather than decoded straight into the array: some
// records double-encode it (a JSON-stringified array, e.g.
// `"catalog_index_urls": "[{\"url\": \"...\"}]"`, from whatever wrote the
// record serializing it twice) instead of a native array. parseCatalogIndexURLs
// tolerates both shapes.
type queryMeta struct {
	CatalogIndexURLs json.RawMessage `json:"catalog_index_urls"`
}

type catalogIndexURLEntry struct {
	URL string `json:"url"`
}

// parseCatalogIndexURLs decodes meta.catalog_index_urls in either shape seen
// on the wire: a native JSON array of {url} objects (the spec-compliant
// shape), or a JSON string whose content IS that array (some records
// double-encode it). Malformed either way returns nil -- a bad record is
// skipped, same as a record with no catalog_index_urls at all, rather than
// failing the whole /query lookup over one bad record.
func parseCatalogIndexURLs(raw json.RawMessage) []catalogIndexURLEntry {
	if len(raw) == 0 {
		return nil
	}
	var entries []catalogIndexURLEntry
	if err := json.Unmarshal(raw, &entries); err == nil {
		return entries
	}
	// Not a native array -- try the flattened/double-encoded string shape.
	var flattened string
	if err := json.Unmarshal(raw, &flattened); err != nil {
		return nil
	}
	if err := json.Unmarshal([]byte(flattened), &entries); err != nil {
		return nil
	}
	return entries
}

// Providers GETs {base}/query/{networkID} and returns one Provider per
// catalog index URL any live record declares in meta.catalog_index_urls (a
// record with more than one URL yields more than one Provider, one per
// catalog per node -- see NFH-014 "a node may host more than one catalog
// index"). networkID is used verbatim as the "{namespace}/{registry}" path
// segment (e.g. "beckn.one/testnet").
//
// A record is skipped unless it is state=="live" AND carries at least one
// non-empty meta.catalog_index_urls[].url — that is what distinguishes a
// provider node that publishes catalogs from a BAP/DS/CS or a provider with
// no index. Records with a null meta or details are handled gracefully
// (skipped / empty ParticipantID).
func (c *DediQueryClient) Providers(ctx context.Context, networkID string) ([]Provider, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()

	url := c.base + "/query/" + networkID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("crawler: registry query %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("crawler: registry query %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxQueryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("crawler: reading registry query %s: %w", url, err)
	}
	if int64(len(b)) > maxQueryBytes {
		return nil, fmt.Errorf("crawler: registry query %s exceeds max %d bytes", url, maxQueryBytes)
	}

	var qr queryResponse
	if err := json.Unmarshal(b, &qr); err != nil {
		return nil, fmt.Errorf("crawler: parsing registry query %s: %w", url, err)
	}

	var provs []Provider
	for _, rec := range qr.Data.Records {
		if rec.State != "live" || rec.Meta == nil {
			continue
		}
		var id string
		if rec.Details != nil {
			id = rec.Details.SubscriberID
		}
		for _, entry := range parseCatalogIndexURLs(rec.Meta.CatalogIndexURLs) {
			idx := strings.TrimSpace(entry.URL)
			if idx == "" {
				continue
			}
			provs = append(provs, Provider{ParticipantID: id, IndexURL: idx})
		}
	}
	return provs, nil
}

// bounded derives a context carrying the client's per-lookup timeout as a
// deadline; a non-positive timeout means no limit. It mirrors the fetch
// client's own budgeting so a registry that never answers cannot stall a pass.
func (c *DediQueryClient) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.timeout)
}
