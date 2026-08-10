package catalog

// change.go — change detection: compares a catalog's index entry against our
// stored cursors and yields a typed Decision (sync / skip_unchanged / retire /
// rollback). Pure, no I/O.
//
// Two independent cursors, per RFC NFH-014's §Versioning: entryVersion (bumps
// on ANY change to the entry, content or metadata) is the cheap first check;
// the content-lineage version (baseline/changes[].version, via
// CatalogEntry.LatestVersion) is what decides how much content actually needs
// resolving. A metadata-only change (e.g. networkIds edited, no new file)
// bumps entryVersion with the content version unchanged -- still an
// ActionSync, so the entry is re-scoped/re-evaluated, but resolve.go's delta
// range for it is empty.

// Action is what the index job decides to do with a catalog on a pass. (This
// is the typed change-detection vocabulary §6b calls "Decision".)
type Action string

const (
	ActionSync          Action = "sync"           // entryVersion (or content) advanced; enqueue a sync
	ActionSkipUnchanged Action = "skip_unchanged" // entryVersion cursor already at latest
	ActionRetire        Action = "retire"         // catalog carries retiredAt
	ActionRollback      Action = "rollback"       // entryVersion or content version went backwards; flag, don't apply
)

// Decision is the outcome of change detection for one catalog.
type Decision struct {
	Action       Action
	ToVersion    int64 // content-lineage target (CatalogEntry.LatestVersion())
	EntryVersion int64 // the entry-level cursor to persist once this decision is acted on
}

// DetectChange compares a catalog's index entry against our stored cursors and
// decides what to do. seen=false means we have never synced this catalog.
// entryCursor/contentCursor are the last-persisted CatalogState.EntryVersion /
// CatalogState.Version.
func DetectChange(entry CatalogEntry, entryCursor, contentCursor int64, seen bool) Decision {
	if seen && entry.EntryVersion < entryCursor {
		// A regression in the cheap entry-level check is flagged before even
		// looking at content lineage -- CON-TBD-11 covers a regression in
		// EITHER version independently.
		return Decision{Action: ActionRollback, ToVersion: entry.LatestVersion(), EntryVersion: entry.EntryVersion}
	}
	if entry.IsRetired() {
		return Decision{Action: ActionRetire, EntryVersion: entry.EntryVersion}
	}
	latest := entry.LatestVersion()
	switch {
	case !seen:
		return Decision{Action: ActionSync, ToVersion: latest, EntryVersion: entry.EntryVersion}
	case latest < contentCursor:
		return Decision{Action: ActionRollback, ToVersion: latest, EntryVersion: entry.EntryVersion}
	case entry.EntryVersion == entryCursor:
		return Decision{Action: ActionSkipUnchanged, ToVersion: contentCursor, EntryVersion: entryCursor}
	default:
		// entryVersion advanced: either new content (latest > contentCursor) or
		// a metadata-only edit (latest == contentCursor). Either way the entry
		// needs re-processing; resolve.go naturally produces an empty delta
		// range for the metadata-only case.
		return Decision{Action: ActionSync, ToVersion: latest, EntryVersion: entry.EntryVersion}
	}
}
