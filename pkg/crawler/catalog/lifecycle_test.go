package catalog

// lifecycle_test.go — pins the stable wire values of the lifecycle enums
// (SyncOutcome, CatalogStatus, DropReason) that the store and logs depend on.

import "testing"

// The persisted/rendered wire values are the contract the store and logs speak;
// pin them so a rename can't silently change what lands in push_status.
func TestSyncOutcomeWireValues(t *testing.T) {
	cases := map[SyncOutcome]string{
		OutcomePushed:  "pushed",
		OutcomePartial: "partial",
		OutcomeSkipped: "skipped",
		OutcomeDropped: "dropped",
		OutcomeRetired: "retired",
		OutcomeFaulted: "faulted",
	}
	for o, want := range cases {
		if o.String() != want {
			t.Errorf("SyncOutcome %v String() = %q, want %q", o, o.String(), want)
		}
	}
}

func TestCatalogStatusAndDropReasonWireValues(t *testing.T) {
	if CatalogActive.String() != "active" || CatalogRetired.String() != "retired" {
		t.Errorf("CatalogStatus wire values drifted: %q / %q", CatalogActive, CatalogRetired)
	}
	if DropNotAMember.String() != "not_a_member" || DropScopeNotApproved.String() != "scope_not_approved" {
		t.Errorf("DropReason wire values drifted: %q / %q", DropNotAMember, DropScopeNotApproved)
	}
}
