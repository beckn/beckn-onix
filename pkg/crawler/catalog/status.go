package catalog

// status.go — the persisted status/outcome enums the store and Discovery care
// about (a store/DB contract, separate from logging). The subject is a Catalog
// Sync — one catalog moving one version jump — and these are the terminal
// outcome + persisted status that both the store (persists) and the runner
// (sets) speak. Each enum's String() is the STABLE WIRE VALUE used for DB
// persistence and log/metric rendering.

// SyncOutcome is how a Catalog Sync ends — a NAMED terminal outcome, never a
// bare "completed". Persisted in a catalog's push_status history.
//
//	pushed   landed in Discovery
//	partial  some batches acked, some not (retried; cursor not advanced)
//	skipped  nothing new to send
//	dropped  not a member / out of approved scope (P2 — scope enforcement)
//	retired  catalog tombstoned
//	faulted  failed with a FaultClass explaining why (see fault.go)
type SyncOutcome string

const (
	OutcomePushed  SyncOutcome = "pushed"
	OutcomePartial SyncOutcome = "partial"
	OutcomeSkipped SyncOutcome = "skipped"
	OutcomeDropped SyncOutcome = "dropped"
	OutcomeRetired SyncOutcome = "retired"
	OutcomeFaulted SyncOutcome = "faulted"
)

func (o SyncOutcome) String() string { return string(o) }

// CatalogStatus is a catalog's stored lifecycle status (crawler_catalog.status)
// — lowercase, distinct from the index/ION wire status (StatusActive/Retired in
// index.go).
type CatalogStatus string

const (
	CatalogActive  CatalogStatus = "active"
	CatalogRetired CatalogStatus = "retired"
)

func (c CatalogStatus) String() string { return string(c) }

// DropReason says why an in-scope-looking catalog was excluded from a sync
// (membership / scope) — the *why* behind a `dropped` outcome. Reserved for
// scope enforcement (P2): the sync's scope step (ResolveScope) resolves
// visibility today but does not yet drop, so nothing produces a DropReason yet.
type DropReason string

const (
	DropNotAMember       DropReason = "not_a_member"
	DropScopeNotApproved DropReason = "scope_not_approved"
)

func (d DropReason) String() string { return string(d) }
