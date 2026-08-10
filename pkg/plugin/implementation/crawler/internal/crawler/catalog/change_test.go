package catalog

// change_test.go — tests for DetectChange (change detection): the sync / skip /
// retire / rollback decisions and placeholder-change (no-URL) handling.

import "testing"

// active builds an entry with the given entryVersion, a baseline version, and
// any number of change-file versions (all carrying a URL).
func active(entryVersion, baseline int64, changes ...int64) CatalogEntry {
	e := CatalogEntry{CatalogID: "p/c", EntryVersion: entryVersion, Baseline: FileEntry{Version: baseline, URL: "u", Digest: "d"}}
	for _, v := range changes {
		e.Changes = append(e.Changes, FileEntry{Version: v, URL: "u", Digest: "d"})
	}
	return e
}

func TestDecide(t *testing.T) {
	// A change entry that carries no URL yet (spec's illustrative
	// placeholder) must not count as available content.
	placeholder := active(5, 40, 41)
	placeholder.Changes = append(placeholder.Changes, FileEntry{Version: 42})

	tests := []struct {
		name          string
		entry         CatalogEntry
		entryCursor   int64
		contentCursor int64
		seen          bool
		want          Action
		wantVer       int64
	}{
		{"new catalog enqueues sync to latest", active(1, 40, 41, 42), 0, 0, false, ActionSync, 42},
		{"entryVersion advanced (new content) enqueues sync to latest", active(2, 40, 41, 42), 1, 40, true, ActionSync, 42},
		{"entryVersion advanced with no new content (metadata-only) still syncs", active(2, 40), 1, 40, true, ActionSync, 40},
		{"unchanged entryVersion is skipped", active(4, 40, 41, 42), 4, 42, true, ActionSkipUnchanged, 0},
		{"entryVersion regression is rollback", active(3, 40), 4, 40, true, ActionRollback, 0},
		{"content version regression at same entryVersion is rollback", active(4, 40), 4, 42, true, ActionRollback, 40},
		{"retired enqueues retire", CatalogEntry{CatalogID: "p/c", EntryVersion: 5, RetiredAt: "2026-01-31T00:00:00Z"}, 4, 41, true, ActionRetire, 0},
		{"placeholder change without url is ignored", placeholder, 5, 40, true, ActionSkipUnchanged, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectChange(tt.entry, tt.entryCursor, tt.contentCursor, tt.seen)
			if got.Action != tt.want {
				t.Fatalf("Action = %q, want %q", got.Action, tt.want)
			}
			if got.Action == ActionSync && got.ToVersion != tt.wantVer {
				t.Fatalf("ToVersion = %d, want %d", got.ToVersion, tt.wantVer)
			}
		})
	}
}
