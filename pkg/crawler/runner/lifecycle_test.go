package runner

import (
	"errors"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/crawler/catalog"
)

// classifyOutcome is the ONE place the push-outcome rule lives. External
// behavior (retry-vs-park, cursor advancement) is decided elsewhere; this only
// pins the persisted SyncOutcome string.
func TestClassifyOutcome(t *testing.T) {
	tests := []struct {
		name       string
		httpStatus int
		acked      int
		err        error
		want       catalog.SyncOutcome
	}{
		{"4xx rejection -> faulted", 400, 0, nil, catalog.OutcomeFaulted},
		{"4xx even with acked -> faulted", 409, 2, nil, catalog.OutcomeFaulted},
		{"transport error -> faulted", 0, 0, errors.New("boom"), catalog.OutcomeFaulted},
		{"5xx none acked -> faulted", 500, 0, nil, catalog.OutcomeFaulted},
		{"5xx some acked -> partial", 500, 1, nil, catalog.OutcomePartial},
		{"pre-push failure (status 0) -> faulted", 0, 0, nil, catalog.OutcomeFaulted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyOutcome(tt.httpStatus, tt.acked, tt.err); got != tt.want {
				t.Errorf("classifyOutcome(%d,%d,%v) = %q, want %q", tt.httpStatus, tt.acked, tt.err, got, tt.want)
			}
		})
	}
}

// The Catalog Sync's happy-path progression is declared, not implied.
func TestSyncPhaseTransitions(t *testing.T) {
	happy := []SyncPhase{SyncResolving, SyncVerifying, SyncValidating, SyncScoping, SyncPublishing}
	for i := 0; i+1 < len(happy); i++ {
		if !ValidSyncPhase(happy[i], happy[i+1]) {
			t.Errorf("declared progression %s -> %s should be legal", happy[i], happy[i+1])
		}
	}
	if ValidSyncPhase(SyncResolving, SyncPublishing) {
		t.Error("skipping straight from resolving to publishing must be illegal")
	}
	if ValidSyncPhase(SyncPublishing, SyncResolving) {
		t.Error("a Catalog Sync must not run backwards")
	}
	// validating (§9a) is not implemented yet, so the live sync skips it:
	// verifying → scoping must be a legal shortcut while validating stays
	// reachable (verifying → validating) for when §9a lands.
	if !ValidSyncPhase(SyncVerifying, SyncScoping) {
		t.Error("verifying -> scoping must be legal while validating (§9a) is unimplemented")
	}
	if !ValidSyncPhase(SyncVerifying, SyncValidating) {
		t.Error("verifying -> validating must stay reachable for §9a")
	}
}

// Any running sub-state may fault; scoping may drop; publishing settles as
// pushed/partial/skipped. retired is reached via the retire path, not a phase.
func TestSyncPhaseOutcomes(t *testing.T) {
	for _, p := range []SyncPhase{SyncResolving, SyncVerifying, SyncValidating, SyncScoping, SyncPublishing} {
		if !ValidSyncOutcome(p, catalog.OutcomeFaulted) {
			t.Errorf("phase %s must be able to fault", p)
		}
	}
	if !ValidSyncOutcome(SyncScoping, catalog.OutcomeDropped) {
		t.Error("scoping must be able to drop")
	}
	if ValidSyncOutcome(SyncResolving, catalog.OutcomeDropped) {
		t.Error("only scoping may drop")
	}
	for _, o := range []catalog.SyncOutcome{catalog.OutcomePushed, catalog.OutcomePartial, catalog.OutcomeSkipped} {
		if !ValidSyncOutcome(SyncPublishing, o) {
			t.Errorf("publishing must be able to settle as %s", o)
		}
	}
	if ValidSyncOutcome(SyncResolving, catalog.OutcomePushed) {
		t.Error("only publishing may push")
	}
}

func TestRunnerLifecycleWireValues(t *testing.T) {
	daemon := map[DaemonState]string{
		DaemonReady: "ready", DaemonStopping: "stopping",
		DaemonStopped: "stopped", DaemonStartFailed: "start_failed",
	}
	for d, want := range daemon {
		if d.String() != want {
			t.Errorf("DaemonState %v = %q, want %q", d, d.String(), want)
		}
	}
	index := map[IndexOutcome]string{
		IndexUnchanged: "unchanged", IndexEnqueued: "enqueued", IndexFailed: "failed",
	}
	for o, want := range index {
		if o.String() != want {
			t.Errorf("IndexOutcome %v = %q, want %q", o, o.String(), want)
		}
	}
	phase := map[SyncPhase]string{
		SyncResolving: "resolving", SyncVerifying: "verifying", SyncValidating: "validating",
		SyncScoping: "scoping", SyncPublishing: "publishing",
	}
	for p, want := range phase {
		if p.String() != want {
			t.Errorf("SyncPhase %v = %q, want %q", p, p.String(), want)
		}
	}
}
