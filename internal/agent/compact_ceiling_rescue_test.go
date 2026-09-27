package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/event"
)

// ceilingRejectReceipts counts maintenance receipts whose reason carries the
// physical-ceiling rejection - the exact text the desktop log shows for the
// 17:50 event (checkpoint candidate rejected ... physical ceiling, three in a
// row with no rescue).
func ceilingRejectReceipts(a *Agent) int {
	n := 0
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.ContextMaintenanceEvent && e.Maintenance != nil &&
			strings.Contains(e.Maintenance.Reason, "physical ceiling") {
			n++
		}
	})
	return n
}

// preparePressure runs one pressure-triggered prepare (force folds even when
// the cached prepared view still sits under the trigger - the view only
// refreshes on a real turn, while these fixtures grow the session directly)
// and reports how many physical-ceiling rejections it produced.
func preparePressure(ctx context.Context, a *Agent, force bool) (error, int) {
	count := 0
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.ContextMaintenanceEvent && e.Maintenance != nil &&
			strings.Contains(e.Maintenance.Reason, "physical ceiling") {
			count++
		}
	})
	_, err := a.contextManager().Prepare(ctx, ContextPreparePolicy{Trigger: CompactionTriggerPressure, Force: force})
	return err, count
}

// Task 307: a summary candidate at or above the physical ceiling used to fall
// through rescueOrFail's below-hard path (return the stale view, nil) and was
// rejected again on every following round - the 17:50 event showed three
// back-to-back rejections while the context grew to 3.8M tokens. Round 1
// injects the ceiling rejection exactly as acceptCheckpointCandidate produces
// it (the fixture cannot force candidate-in-[hard, source) through the full
// ladder without tripping the summary input budget first) and must end in the
// truncation rescue on the spot. Round 2 then runs a real prepare: the
// truncated view sits under the fold trigger, so no second ceiling rejection
// may be logged - two in a row is the acceptance criterion's forbidden shape.
func TestCeilingRejectionEndsInTruncationOnFirstReject(t *testing.T) {
	sess := foldableSessionOverForce(6)
	a := agentOverForceWindow(t, &fakeProvider{reply: "digest"}, sess, 6000)

	hard := a.hardInputCeiling()
	if hard <= 0 {
		t.Fatalf("hardInputCeiling = %d, fixture cannot exercise the ceiling path", hard)
	}
	// The view must sit above the fold (so truncation has something to cut)
	// but below the ceiling - the below-hard state the 17:50 rounds started
	// from, where rescueOrFail used to hand the stale view back instead of
	// rescuing.
	est, fold := a.estimatedPromptTokens(sess.Messages), a.compactTrigger()
	if est <= fold || est >= hard {
		t.Fatalf("fixture tokens=%d not in (fold=%d, hard=%d); adjust turns", est, fold, hard)
	}

	// Round 1 (rejected): the ceiling rejection as acceptCheckpointCandidate
	// emits it. It must end in truncation this round - not in the stale-view
	// return that let the 17:50 loop run three times.
	ceilErr := fmt.Errorf("%w: candidate 6611 still at or above physical ceiling %d", errCheckpointCeiling, hard)
	var rejects int
	a.svc.sink = event.FuncSink(func(e event.Event) {
		if e.Kind == event.ContextMaintenanceEvent && e.Maintenance != nil &&
			strings.Contains(e.Maintenance.Reason, "physical ceiling") {
			rejects++
		}
	})
	m := a.contextManager()
	got, err1 := m.summaryFailed(ContextPreparePolicy{Trigger: CompactionTriggerPressure},
		"round1", hard, false, ceilErr)
	if err1 != nil {
		t.Fatalf("round 1 summaryFailed = %v, want the truncation rescue to succeed", err1)
	}
	if !truncatedRescue(a) {
		t.Fatalf("receipt = %+v, want the ceiling rejection to end in a truncation rescue", a.sess.compactionState.LastReceipt)
	}
	if got.InputTokens >= hard {
		t.Fatalf("rescued view estimates %d tokens, still at/above ceiling %d", got.InputTokens, hard)
	}
	if rejects != 1 {
		t.Fatalf("physical-ceiling rejections = %d, want exactly 1 in the rejecting round", rejects)
	}

	// Round 2: a real prepare over the truncated view. Under the fold trigger
	// it must pass through with no summary and no second ceiling rejection.
	_, rejects2 := preparePressure(context.Background(), a, false)
	if rejects2 != 0 {
		t.Fatalf("round 2 ceiling rejections = %d, want 0 (no consecutive blocked)", rejects2)
	}
	if rejects+rejects2 != 1 {
		t.Fatalf("physical-ceiling rejections across two rounds = %d, want exactly 1", rejects+rejects2)
	}
}
