package catalog

// This file holds the small boundary types that both the runner's ports and
// the adapters speak, kept in the shared-bottom package so neither side has to
// import the other (same reasoning as the lifecycle enums).

// IndexConditions carries the conditional-GET validators from the last
// successful index fetch. Both empty means an unconditional GET (the host sent
// none, or we've never fetched this index).
type IndexConditions struct {
	ETag         string
	LastModified string
}

// IndexResult is one index fetch. NotModified is true when the host answered
// 304 (the index is unchanged and Index is zero — skip it). ETag/LastModified
// are the validators to store for next time (echoed back on a 304). Dropped
// names every catalog entry the parser saw but excluded from Index.Catalogs
// (malformed JSON, or a self-signature that failed to verify) — fail-closed
// silently drops the entry from processing, but the caller needs this to log
// WHY a poll that fetched fine still queued nothing, rather than looking
// identical to "nothing changed".
type IndexResult struct {
	Index        Index
	NotModified  bool
	ETag         string
	LastModified string
	Dropped      []DroppedEntry
}

// DroppedEntry is one catalog entry excluded from a fetched index, and why.
type DroppedEntry struct {
	CatalogID string // best-effort: empty if the entry didn't even parse enough to have one
	Reason    string
}
