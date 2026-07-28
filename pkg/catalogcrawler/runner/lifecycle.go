package runner

// This file is the orchestration-only half of the lifecycle vocabulary (§6b),
// recentred on the Catalog Sync as the subject. It declares the states that
// live entirely inside the runner (no layer below it speaks them): the Catalog
// Sync's running sub-states, the index crawl's terminal, and the crawler
// process supervisor. The Catalog Sync's *terminal* outcome (SyncOutcome) and
// persisted status (CatalogStatus) live in package catalog (the shared bottom),
// because store/ persists them and may not import runner/ (§7).
//
// Legal transitions are DECLARED here (not implied), so "clearly defined" is
// literal and testable — an illegal transition is a bug the machine rejects.

import "github.com/beckn-one/beckn-onix/pkg/catalogcrawler/catalog"

// SyncPhase is a Catalog Sync's running sub-state — where one catalog is in its
// version jump. Transient (traces/logs), never persisted; the sync settles into
// a named catalog.SyncOutcome.
type SyncPhase string

const (
	SyncResolving  SyncPhase = "resolving"  // download baseline + changes
	SyncVerifying  SyncPhase = "verifying"  // digest
	SyncValidating SyncPhase = "validating" // schema of the content
	SyncScoping    SyncPhase = "scoping"    // member? in approved scope?
	SyncPublishing SyncPhase = "publishing" // push to Discovery
)

func (p SyncPhase) String() string { return string(p) }

// syncPhaseTransitions declares the happy-path progression of a Catalog Sync:
//
//	started → resolving → verifying → [validating] → scoping → publishing → SyncOutcome
//
// "started" is the sync's birth (entry at resolving); the terminal SyncOutcome
// leaves the SyncPhase space (see syncPhaseOutcomes).
//
// validating (inbound content-validation, §9a) is not implemented yet, so the
// runner skips straight from verifying → scoping. The transition table keeps
// validating REACHABLE (verifying may still go there) so re-enabling §9a needs
// no vocabulary change — but the live sync only emits phases backed by a real
// step, so verifying → scoping is the path taken today.
var syncPhaseTransitions = map[SyncPhase][]SyncPhase{
	SyncResolving:  {SyncVerifying},
	SyncVerifying:  {SyncValidating, SyncScoping},
	SyncValidating: {SyncScoping},
	SyncScoping:    {SyncPublishing},
	SyncPublishing: {},
}

// syncPhaseOutcomes declares the terminal SyncOutcomes each phase may settle
// into: any running sub-state may `faulted` (with a FaultClass); scoping may
// `dropped`; publishing lands as `pushed`/`partial`, or `skipped` when there is
// nothing to send. (`retired` is reached via the retire path, not from a phase.)
var syncPhaseOutcomes = map[SyncPhase][]catalog.SyncOutcome{
	SyncResolving:  {catalog.OutcomeFaulted},
	SyncVerifying:  {catalog.OutcomeFaulted},
	SyncValidating: {catalog.OutcomeFaulted},
	SyncScoping:    {catalog.OutcomeFaulted, catalog.OutcomeDropped},
	SyncPublishing: {catalog.OutcomeFaulted, catalog.OutcomePushed, catalog.OutcomePartial, catalog.OutcomeSkipped},
}

// ValidSyncPhase reports whether from -> to is a declared phase progression.
func ValidSyncPhase(from, to SyncPhase) bool {
	for _, n := range syncPhaseTransitions[from] {
		if n == to {
			return true
		}
	}
	return false
}

// ValidSyncOutcome reports whether a Catalog Sync in phase p may legally settle
// as out.
func ValidSyncOutcome(p SyncPhase, out catalog.SyncOutcome) bool {
	for _, o := range syncPhaseOutcomes[p] {
		if o == out {
			return true
		}
	}
	return false
}

// IndexOutcome is how an index crawl ends. An index crawl *feeds* the queue
// ("which of a participant's catalogs changed?") — it decides *what to sync*;
// the Catalog Sync *does* the sync.
type IndexOutcome string

const (
	IndexUnchanged IndexOutcome = "unchanged" // 304 / same version — nothing enqueued
	IndexEnqueued  IndexOutcome = "enqueued"  // N catalogs queued for sync
	IndexFailed    IndexOutcome = "failed"    // fetch/parse error
)

func (o IndexOutcome) String() string { return string(o) }

// DaemonState is the crawler process lifecycle — the supervisor (the "driver").
// It has NO "completed": a supervisor never completes, it just launches Catalog
// Syncs until stopped.
type DaemonState string

const (
	DaemonReady       DaemonState = "ready"
	DaemonStopping    DaemonState = "stopping"
	DaemonStopped     DaemonState = "stopped"
	DaemonStartFailed DaemonState = "start_failed"
)

func (d DaemonState) String() string { return string(d) }

// classifyOutcome is the ONE place the push-outcome rule lives (it replaced the
// duplicated logic at the old engine fail sites): a 4xx push rejection or any
// error is `faulted` (the FaultClass — push_rejected for a 4xx — is decided
// separately by catalog.ClassifyFault and drives park-vs-retry); a push that
// acked some batches but not all is `partial`; anything else (5xx / all
// unacked) is `faulted`. Success (all acked) and skipped are decided by their
// own sites, not here.
func classifyOutcome(httpStatus, ackedBatches int, err error) catalog.SyncOutcome {
	switch {
	case httpStatus >= 400 && httpStatus < 500:
		return catalog.OutcomeFaulted
	case err != nil:
		return catalog.OutcomeFaulted
	case ackedBatches > 0:
		return catalog.OutcomePartial
	default:
		return catalog.OutcomeFaulted
	}
}
