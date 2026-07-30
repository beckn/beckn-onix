package catalog

// change_test.go — tests for DetectChange (change detection): the sync / skip /
// retire / rollback decisions and placeholder-change (no-URL) handling.

import "testing"

// active builds an ACTIVE catalog entry with a baseline version and any
// number of change-file versions (all carrying a URL).
func active(baseline int64, changes ...int64) CatalogEntry {
	e := CatalogEntry{CatalogID: "p/c", Status: StatusActive, Baseline: FileEntry{Version: baseline, URL: "u", Digest: "d"}}
	for _, v := range changes {
		e.Changes = append(e.Changes, FileEntry{Version: v, URL: "u", Digest: "d"})
	}
	return e
}

func TestDecide(t *testing.T) {
	// A change entry that carries no URL yet (spec's illustrative
	// placeholder) must not count as available content.
	placeholder := active(40, 41)
	placeholder.Changes = append(placeholder.Changes, FileEntry{Version: 42})

	tests := []struct {
		name    string
		entry   CatalogEntry
		cursor  int64
		seen    bool
		want    Action
		wantVer int64
	}{
		{"new catalog enqueues sync to latest", active(40, 41, 42), 0, false, ActionSync, 42},
		{"newer version enqueues sync to latest", active(40, 41, 42), 40, true, ActionSync, 42},
		{"unchanged is skipped", active(40, 41, 42), 42, true, ActionSkipUnchanged, 0},
		{"backwards version is rollback", active(40), 42, true, ActionRollback, 0},
		{"retired enqueues retire", CatalogEntry{CatalogID: "p/c", Status: StatusRetired}, 41, true, ActionRetire, 0},
		{"placeholder change without url is ignored", placeholder, 41, true, ActionSkipUnchanged, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectChange(tt.entry, tt.cursor, tt.seen)
			if got.Action != tt.want {
				t.Fatalf("Action = %q, want %q", got.Action, tt.want)
			}
			if got.Action == ActionSync && got.ToVersion != tt.wantVer {
				t.Fatalf("ToVersion = %d, want %d", got.ToVersion, tt.wantVer)
			}
		})
	}
}
