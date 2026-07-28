package catalog

// Action is what the index job decides to do with a catalog on a pass. (This
// is the typed change-detection vocabulary §6b calls "Decision".)
type Action string

const (
	ActionSync          Action = "sync"           // content advanced; enqueue a sync
	ActionSkipUnchanged Action = "skip_unchanged" // cursor already at latest
	ActionRetire        Action = "retire"         // catalog went RETIRED
	ActionRollback      Action = "rollback"       // version went backwards; flag, don't apply
)

// Decision is the outcome of change detection for one catalog.
type Decision struct {
	Action    Action
	ToVersion int64
}

// Decide compares a catalog's index entry against our stored cursor and
// decides what to do. seen=false means we have never synced this catalog.
func Decide(entry CatalogEntry, cursor int64, seen bool) Decision {
	if entry.Status == StatusRetired {
		return Decision{Action: ActionRetire}
	}
	latest := entry.LatestVersion()
	switch {
	case !seen || latest > cursor:
		return Decision{Action: ActionSync, ToVersion: latest}
	case latest < cursor:
		return Decision{Action: ActionRollback, ToVersion: latest}
	default:
		return Decision{Action: ActionSkipUnchanged, ToVersion: cursor}
	}
}
